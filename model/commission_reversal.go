package model

import (
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 冲销链路（契约 01-CONTRACT.md §3.5 ReverseCommissionTx + 附录 C1 充值单作废，COMM-04）。
// ErrTopUpNotFound / ErrTopUpStatusInvalid 复用 topup.go 既有 sentinel（勿重复定义）。

// ErrAlreadyStatemented 原 commission 流水已出账（available）时冲销返回此哨兵
// （契约 §3.5 + §7#4：已出账退款走人工调整单，不自动改历史）。
// 调用方 VoidTopUp 据此整体回滚作废事务，禁止半作废。
var ErrAlreadyStatemented = errors.New("commission flow already statemented")

// ReverseCommissionTx 生成负数冲销流水（契约 §3.5 冻结签名）。
// 必须在调用方事务内执行（全程只触碰传入 tx，禁止绕行全局 DB——Pitfall 2）：
//   - 无原 commission 流水（开关期充值/未记账单）→ no-op 返回 nil
//   - 原流水已出账（available）→ 返回 ErrAlreadyStatemented，调用方整体回滚
//   - pending 原流水 → 恰好生成 1 条负数 reversal 流水，原记录保留不动（COMM-04 不删除语义）
//
// reversal BillingNo=tradeNo：同单冲销唯一由 uk_flow_billing(billing_no, flow_type) 唯一索引兜底（§3.1.1）；
// Period 照抄原流水：出账时同单净额抵减，跨月缓冲期作废不会把正负流水拆进两期账单。
func ReverseCommissionTx(tx *gorm.DB, tradeNo string, reason string) error {
	original := &CommissionFlow{}
	// 先查任意状态的原 commission 流水（勿只查 pending）：已出账单必须显式拒绝而非静默 no-op
	err := tx.Where("trade_no = ? AND flow_type = ?", tradeNo, CommissionFlowCommission).
		Order("id DESC").First(original).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // 未记账（开关期充值/无佣金归属）→ 无需冲销
		}
		return err
	}
	if original.Status == CommissionFlowAvailable {
		return ErrAlreadyStatemented // 已出账 → 禁止自动改历史（契约 §7#4）
	}
	if original.Status != CommissionFlowPending {
		return nil // 防御分支：未知状态不产生冲销
	}
	return tx.Create(&CommissionFlow{
		BillingNo:       tradeNo, // §3.1.1：reversal billing_no = 原 trade_no
		FlowType:        CommissionFlowReversal,
		TradeNo:         tradeNo,
		CustomerId:      original.CustomerId,
		DistributorId:   original.DistributorId,
		TopupMoneyCents: original.TopupMoneyCents,
		RateBp:          original.RateBp,
		AmountCents:     -original.AmountCents, // 负数冲销
		Status:          CommissionFlowPending,
		Period:          original.Period, // 同账期净额抵减（勿用当前月）
		RelatedFlowId:   original.Id,
		Reason:          reason,
		OperatorId:      0, // 系统流水
	}).Error
	// 原流水任何字段均不修改（原记录保留）
}

// VoidTopUp 充值单作废（契约 01-CONTRACT.md 附录 C1 冻结：单事务同生共死）。
// ① Status → refunded ② 扣回当初入账额度（渠道矩阵镜像各渠道加账公式，见 topupQuotaToCredit：
// Creem 裸 Amount、Stripe Money×QuotaPerUnit、其余 Amount×QuotaPerUnit；允许扣成负数，禁钳制）
// ③ ReverseCommissionTx 冲销（ErrAlreadyStatemented → 整体回滚，禁止半作废）
// ④ 审计留痕在事务外（RecordLog 内部走全局 DB；且审计不应随回滚消失）。
func VoidTopUp(tradeNo string, reason string, operatorId int) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	refCol := "`trade_no`"
	if common.UsingMainDatabase(common.DatabaseTypePostgreSQL) {
		refCol = `"trade_no"`
	}

	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		// 锁纪律统一（契约附录 D5.4 + checker m-3 修正）：出账事务 generateStatementTx
		// 的第一把锁是分销商 users 行——作废写路径在业务写动作之前对同一分销商行取锁，
		// 闭合「出账 SELECT pending 后、提交前并发的作废读到 pending 原流水产生跨期冲销」
		// 毫秒竞态（T-04-03-06）。锁目标 = 原佣金流水的 DistributorId，而非当前邀请人归属
		// 列：void 与改绑并发时该列可能已被改写，按当前值加锁护不住原流水归属分销商。
		// 顺序：无锁读 tradeNo 定位 topUp → 无锁查原流水取 DistributorId → 对该 users 行
		// 取锁（原流水不存在即无佣金链路，跳过）→ 下方按原样行锁加载 topUp 继续既有逻辑。
		// 定位读为无锁快照，仅用于确定锁目标；Status 门在行锁加载后才判定，无 TOCTOU 危害。
		topUpPre := &TopUp{}
		if err := tx.Where(refCol+" = ?", tradeNo).Select("id, user_id").First(topUpPre).Error; err != nil {
			return ErrTopUpNotFound
		}
		lockDistId := 0
		var original CommissionFlow
		if err := tx.Where("trade_no = ? AND flow_type = ?", tradeNo, CommissionFlowCommission).
			Order("id DESC").First(&original).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			// 无原佣金流水（开关期充值/未记账单）→ 无佣金链路，跳过分销商锁
		} else {
			lockDistId = original.DistributorId
		}
		if lockDistId > 0 {
			// 分销商行已删除等异常不阻断作废（锁不可得时仅失去互斥，业务门禁不受影响）
			var lockUser User
			if err := tx.Set("gorm:query_option", "FOR UPDATE").
				Select("id").First(&lockUser, "id = ?", lockDistId).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}

		topUp := &TopUp{}
		// 行级锁，串行化同单并发作废（T-03-03-05）
		if err := tx.Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp).Error; err != nil {
			return ErrTopUpNotFound
		}
		// 前置校验：仅 success 可作废（pending/failed/expired/refunded=重复作废 一并拒绝，契约 C1）
		if topUp.Status != common.TopUpStatusSuccess {
			return ErrTopUpStatusInvalid
		}

		// ① 状态置 refunded
		topUp.Status = common.TopUpStatusRefunded
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		// ② 扣回当初入账额度：调渠道矩阵唯一事实源 topupQuotaToCredit，镜像各渠道加账公式
		// （Creem=裸 Amount、Stripe=Money×QuotaPerUnit、Epay/Waffo/WaffoPancake=Amount×QuotaPerUnit）。
		// 原恒 Amount×QuotaPerUnit 公式对 Creem 订单多扣 50 万倍、对 Stripe 用错折扣口径（6.3 Fix 1）。
		// 契约 C1：允许扣成负数——预扣信任额度可透支、SQL 无下限钳制，负余额后新请求被预扣检查自然拦截；
		// 禁止 max(0,...) 钳制——那会破坏「扣回」语义且与加账不对称。
		quotaToDeduct := topupQuotaToCredit(topUp)
		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", gorm.Expr("quota - ?", quotaToDeduct)).Error; err != nil {
			return err
		}

		// ③ 冲销（契约 §3.5）：ErrAlreadyStatemented → 返回 error → 整个作废事务回滚
		// （status 恢复 success、quota 不扣、零流水——三点一致，禁止半作废，T-03-03-03）
		if err := ReverseCommissionTx(tx, tradeNo, reason); err != nil {
			return err
		}

		userId = topUp.UserId
		return nil
	})
	if err != nil {
		return err
	}

	// ④ 审计留痕（事务外执行）
	RecordLog(userId, LogTypeTopup, fmt.Sprintf("充值单已作废 trade_no=%s operator_id=%d reason=%s", tradeNo, operatorId, reason))
	return nil
}
