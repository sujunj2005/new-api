package model

import (
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 月度出账引擎（契约 01-CONTRACT.md §3.1.2/§3.3/§5 + 附录 D2/D5.1，STMT-01/02/03）。
//
// 生成语义：statementPeriod = now 上一自然月（月初-1天法）；对每个「有 pending 流水
// （period <= statementPeriod）且该期未出账」的分销商，在独立事务内生成一张对账单，
// 并把选中流水翻转为 available + 回填 statement_id。
//
// 防重三层（契约 §5/D2 冻结）：
//  1. 调度层 IsMasterNode 门（service 层，挡 slave 节点）
//  2. uk_stmt_period(distributor_id, period) 唯一索引（DB 级最终防线）
//  3. 分销商 users 行 FOR UPDATE（事务内串行化同分销商竞争）
//
// 边界纪律（研究裁决 A1）：出账边界一律 period 字段过滤，禁用创建时间列判定——
// 跨月冲销的 Period 照抄原流水（commission_reversal.go 冻结注释），若按流水创建时间
// 过滤会把跨月冲销与原流水拆进两期账单。period 为 varchar(7) 字典序比较，三库通用
// 且命中 idx_flow_period 索引。
//
// 门控纪律（附录 D5.1）：出账任务不受分佣总开关门控——纠错与对账是账本完整性操作，
// 开关只控制新增记账入口。

// statementPeriodOf 计算出账期标签 = now 所在月的上一自然月（"2006-01"）。
// 月初-1天法：取当月 1 号再减一天，得上月最后一天后取其月份。
// 禁用 AddDate(0,-1,0)：Go 对目标月不存在的日期做进位归一化（3/31 → 2/31 → 3/3），
// 会把 3 月 31 日算出 "2006-03"（当月）标签（04-RESEARCH Pitfall 1）。
func statementPeriodOf(now time.Time) string {
	return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).
		AddDate(0, 0, -1).Format("2006-01")
}

// GenerateDueStatements 出账入口（参数化 now 供测试与补跑）。
// 逐分销商单事务生成；单分销商失败即中断整批并返回错误（已提交的分销商不回滚——
// 部分成功合法，下次调用按幂等预检续跑）；skip/冲突/空流水均不计入返回值。
// 返回本次实际新建的对账单张数。
func GenerateDueStatements(now time.Time) (int, error) {
	stmtPeriod := statementPeriodOf(now)

	// 候选分销商：有 pending 流水者（验收标准 1「有可用佣金的分销商」，Pitfall 5）。
	// period 字段过滤（裁决 A1，禁用创建时间列判定）。
	var distIds []int
	if err := DB.Model(&CommissionFlow{}).
		Where("status = ? AND period <= ?", CommissionFlowPending, stmtPeriod).
		Distinct("distributor_id").Pluck("distributor_id", &distIds).Error; err != nil {
		return 0, err
	}

	generated := 0
	for _, distId := range distIds {
		created := false
		err := DB.Transaction(func(tx *gorm.DB) error {
			var txErr error
			created, txErr = generateStatementTx(tx, distId, stmtPeriod)
			return txErr
		})
		if err != nil {
			return generated, err
		}
		if created {
			generated++
		}
	}
	return generated, nil
}

// generateStatementTx 单分销商出账七步事务（全程只用传入 tx，禁全局 DB——
// 事务内绕行全局句柄破坏原子性，且在单连接测试库下死锁，Phase 3 Pitfall 2/7 同源）。
// 返回 created=true 仅当真正新建了对账单；预检命中/空流水/唯一索引冲突均 (false, nil)。
func generateStatementTx(tx *gorm.DB, distributorId int, stmtPeriod string) (bool, error) {
	// ① 分销商行锁（契约 §5 冻结：出账任务对目标分销商行加锁串行；
	//    SQLite 单写者天然串行，Set 方言与 topup.go/commission_reversal.go 先例一致）
	var lockUser User
	if err := tx.Set("gorm:query_option", "FOR UPDATE").
		Select("id").First(&lockUser, "id = ?", distributorId).Error; err != nil {
		return false, nil // 分销商不存在/已删除等异常 → skip 不阻断整批
	}

	// ② 幂等预检：该期已出账 → skip（uk_stmt_period 唯一索引为最终防线）
	var cnt int64
	if err := tx.Model(&CommissionStatement{}).
		Where("distributor_id = ? AND period = ?", distributorId, stmtPeriod).
		Count(&cnt).Error; err != nil {
		return false, err
	}
	if cnt > 0 {
		return false, nil
	}

	// ③ 读 pending 流水：period 字段过滤（裁决 A1，禁用创建时间列判定），按 id 稳定排序
	var flows []CommissionFlow
	if err := tx.Where("distributor_id = ? AND status = ? AND period <= ?",
		distributorId, CommissionFlowPending, stmtPeriod).
		Order("id ASC").Find(&flows).Error; err != nil {
		return false, err
	}
	if len(flows) == 0 {
		return false, nil // 候选清单与行锁之间的竞争窗口内被他节点出账 → 无空账单（Pitfall 5）
	}

	// ④ 批量补 TopUp 快照：PaymentMethod/PaymentProvider/CompleteTime 只存在于 top_ups
	//    （STMT-02），此后 items 永不回读 top_ups（生成即锁定语义）
	tradeNos := make([]string, 0, len(flows))
	for _, f := range flows {
		if f.TradeNo != "" {
			tradeNos = append(tradeNos, f.TradeNo)
		}
	}
	topupMap := make(map[string]TopUp, len(tradeNos))
	if len(tradeNos) > 0 {
		var topups []TopUp
		if err := tx.Where("trade_no IN ?", tradeNos).
			Select("trade_no, payment_method, payment_provider, complete_time").
			Find(&topups).Error; err != nil {
			return false, err
		}
		for _, tp := range topups {
			topupMap[tp.TradeNo] = tp
		}
	}

	// ⑤ Σ 汇总 + INSERT statement（AmountCents 含负数冲销自然抵减；
	//    SettleAmountCents = TotalCommission + AdjustedCents(初始 0)）
	var totalTopup, totalCommission int64
	for _, f := range flows {
		totalTopup += f.TopupMoneyCents
		totalCommission += f.AmountCents
	}
	stmt := &CommissionStatement{
		DistributorId:        distributorId,
		Period:               stmtPeriod,
		TotalTopupCents:      totalTopup,
		TotalCommissionCents: totalCommission,
		SettleAmountCents:    totalCommission,
		Status:               StatementPayable,
		LockedAt:             common.GetTimestamp(), // 生成即锁定（STMT-03）
	}
	if err := tx.Create(stmt).Error; err != nil {
		// uk_stmt_period 冲突 = 他节点已出账（Pitfall 4）：记 Info 日志 skip，禁重试
		common.SysLog(fmt.Sprintf("commission statement create conflict (already statemented by another node): node=%s distributor_id=%d period=%s err=%v",
			common.NodeName, distributorId, stmtPeriod, err))
		return false, nil
	}

	// ⑥ 每笔流水一条明细（含负数冲销与人工流水，不做净额合并，FlowId 可回溯）；
	//    manual 流水（trade_no=""）无 TopUp 行 → 缺省（附录 D5.2）：
	//    PaymentMethod="manual"、PaymentProvider=""、CompleteTime=流水记账时间
	items := make([]CommissionStatementItem, 0, len(flows))
	for _, f := range flows {
		item := CommissionStatementItem{
			StatementId:     stmt.Id,
			FlowId:          f.Id,
			BillingNo:       f.BillingNo,
			TopupMoneyCents: f.TopupMoneyCents,
			CommissionCents: f.AmountCents,
			CustomerId:      f.CustomerId,
		}
		if tp, ok := topupMap[f.TradeNo]; ok {
			item.PaymentMethod = tp.PaymentMethod
			item.PaymentProvider = tp.PaymentProvider
			item.CompleteTime = tp.CompleteTime
		} else if f.TradeNo == "" {
			item.PaymentMethod = "manual"
			item.PaymentProvider = ""
			item.CompleteTime = f.CreatedAt
		}
		items = append(items, item)
	}
	if err := tx.CreateInBatches(items, 500).Error; err != nil {
		return false, err
	}

	// ⑦ 按 SELECT 出的 ID 列表翻转（Pitfall 3：禁 distributor_id+status 谓词重匹配——
	//    SELECT 与 UPDATE 之间并发提交的新流水会被误翻成无账单孤儿流水）；
	//    RowsAffected 断言 == len(ids)，不等即整体回滚
	ids := make([]int64, 0, len(flows))
	for _, f := range flows {
		ids = append(ids, f.Id)
	}
	res := tx.Model(&CommissionFlow{}).Where("id IN ?", ids).
		Updates(map[string]any{"status": CommissionFlowAvailable, "statement_id": stmt.Id})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected != int64(len(ids)) {
		return false, fmt.Errorf("commission flow flip mismatch: want %d got %d (distributor_id=%d period=%s)",
			len(ids), res.RowsAffected, distributorId, stmtPeriod)
	}

	// 对账校验（验收标准 1）：提交前 Go 侧双恒等式断言，不等 → return error 整体回滚
	var sumCommission, sumTopup int64
	for _, it := range items {
		sumCommission += it.CommissionCents
		sumTopup += it.TopupMoneyCents
	}
	if len(items) != len(flows) || sumCommission != stmt.TotalCommissionCents || sumTopup != stmt.TotalTopupCents {
		return false, fmt.Errorf("commission statement reconciliation mismatch: distributor_id=%d period=%s len(items)=%d len(flows)=%d sum_commission=%d total_commission=%d sum_topup=%d total_topup=%d",
			distributorId, stmtPeriod, len(items), len(flows), sumCommission, stmt.TotalCommissionCents, sumTopup, stmt.TotalTopupCents)
	}
	return true, nil
}
