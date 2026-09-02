package model

import (
	"fmt"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 本文件为月度出账引擎 GenerateDueStatements（契约 01-CONTRACT.md §3.1.2/§3.3/§5
// + 附录 D2/D5.1，STMT-01/02/03）的集成测试。
//
// 纪律（04-RESEARCH Pitfall 7）：
//   - 每测试自建独立内存库（setupCommissionTestDB，AutoMigrate 已含 statement 三表）
//   - 断言时间全部参数化（now 显式喂入），不依赖运行时钟（Pitfall 8）
//   - 出账边界断言 period 字段过滤（研究裁决 A1，禁 created_at）

// seedStatementUser 建一个最小用户行（statement 测试专用，避免与 seedCommissionFixture 耦合）。
func seedStatementUser(t *testing.T, role int) *User {
	t.Helper()
	seq := commissionTestSeq.Add(1)
	u := &User{
		Username: fmt.Sprintf("stmtu%d", seq),
		Password: "testpass1234",
		Role:     role,
		Status:   common.UserStatusEnabled,
		AffCode:  fmt.Sprintf("SA%d", seq),
	}
	require.NoError(t, DB.Create(u).Error)
	return u
}

// statementNow 出账日参考时刻：2026-09-08 00:01（本地时区），出账期标签 = "2026-08"。
func statementNow() time.Time {
	return time.Date(2026, 9, 8, 0, 1, 0, 0, time.Local)
}

// TestStatementPeriodLabel 账期标签 = now 上一自然月（月初-1天法，Pitfall 1）：
// 月末日期（3/31、1/29）与闰年 2 月必须得到正确的上月标签，禁 AddDate(0,-1,0) 归一化错误。
func TestStatementPeriodLabel(t *testing.T) {
	cases := []struct {
		name string
		now  time.Time
		want string
	}{
		{"mar_31_gives_feb", time.Date(2026, 3, 31, 23, 59, 59, 0, time.Local), "2026-02"},
		{"jan_29_gives_prev_dec", time.Date(2026, 1, 29, 12, 0, 0, 0, time.Local), "2025-12"},
		{"leap_year_mar_31_gives_feb_29", time.Date(2024, 3, 31, 0, 0, 0, 0, time.Local), "2024-02"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, statementPeriodOf(tc.now))
		})
	}
}

// TestGenerateDueStatements 汇总生成矩阵（STMT-01 + 验收 1/5）：
// 多分销商各一张账单、Σ 流水 == Totals、负数冲销抵减、零流水分销商跳过（Pitfall 5）、
// 冻结期（period=出账当月）不入账、available 流水不重复纳入。
func TestGenerateDueStatements(t *testing.T) {
	setupCommissionTestDB(t)

	now := statementNow()
	stmtPeriod := "2026-08"

	distA := seedStatementUser(t, common.RoleDistributorUser)
	distB := seedStatementUser(t, common.RoleDistributorUser)
	distC := seedStatementUser(t, common.RoleDistributorUser) // 零 pending 流水 → 不生成
	custA := seedStatementUser(t, common.RoleCommonUser)
	custB := seedStatementUser(t, common.RoleCommonUser)

	// Dist A：1 正 + 1 负冲销（同期净额抵减）+ 1 条已出账 available（不得重复纳入）
	completeA := time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local).Unix()
	fA1 := seedFlow(t, seedFlowOpts{
		distributorId: distA.Id, customerId: custA.Id, tradeNo: "TGEN-A1",
		topupMoneyCents: 10000, rateBp: 1000, amountCents: 1000, period: stmtPeriod,
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "stripe", paymentProvider: "stripe", completeTime: completeA},
	})
	fA2 := seedFlow(t, seedFlowOpts{
		distributorId: distA.Id, customerId: custA.Id, flowType: CommissionFlowReversal, tradeNo: "TGEN-A1",
		topupMoneyCents: 10000, rateBp: 1000, amountCents: -300, period: stmtPeriod,
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "stripe", paymentProvider: "stripe", completeTime: completeA},
	})
	fA3 := seedFlow(t, seedFlowOpts{
		distributorId: distA.Id, customerId: custA.Id, tradeNo: "TGEN-A3",
		topupMoneyCents: 9999, rateBp: 1000, amountCents: 999, period: stmtPeriod, status: CommissionFlowAvailable,
	})

	// Dist B：1 条上期流水 + 1 条冻结期流水（period=出账当月 → 天然冻结，验收 5）
	fB1 := seedFlow(t, seedFlowOpts{
		distributorId: distB.Id, customerId: custB.Id, tradeNo: "TGEN-B1",
		topupMoneyCents: 5000, rateBp: 1000, amountCents: 500, period: stmtPeriod,
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "epay", paymentProvider: "epay", completeTime: completeA},
	})
	fB2 := seedFlow(t, seedFlowOpts{
		distributorId: distB.Id, customerId: custB.Id, tradeNo: "TGEN-B2",
		topupMoneyCents: 8000, rateBp: 1000, amountCents: 800, period: "2026-09",
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "epay", paymentProvider: "epay", completeTime: completeA},
	})

	n, err := GenerateDueStatements(now)
	require.NoError(t, err)
	require.Equal(t, 2, n, "exactly one statement per distributor with pending flows (A and B)")

	// statements 表恰 2 行，均在 "2026-08"
	var stmts []CommissionStatement
	require.NoError(t, DB.Order("distributor_id ASC").Find(&stmts).Error)
	require.Len(t, stmts, 2)
	require.Equal(t, distA.Id, stmts[0].DistributorId)
	require.Equal(t, distB.Id, stmts[1].DistributorId)
	stmtA, stmtB := stmts[0], stmts[1]

	// Dist A：Total 含负数冲销抵减（1000-300=700；available 流水 999 不纳入）
	require.Equal(t, stmtPeriod, stmtA.Period)
	require.EqualValues(t, 20000, stmtA.TotalTopupCents, "topup sums of A's two selected flows only")
	require.EqualValues(t, 700, stmtA.TotalCommissionCents, "1000 + (-300) net; available flow excluded")
	require.EqualValues(t, 700, stmtA.SettleAmountCents, "settle = total + adjusted(0)")
	require.Zero(t, stmtA.AdjustedCents)
	require.Equal(t, StatementPayable, stmtA.Status)
	require.Positive(t, stmtA.LockedAt, "locked at generation (STMT-03)")

	// Dist A items：len==len(选中 flows)，Σ items == Totals（验收 1 对账恒等式）
	itemsA := statementItemsOf(t, stmtA.Id)
	require.Len(t, itemsA, 2, "one item per selected flow (available flow excluded)")
	var sumTopupA, sumCommissionA int64
	for _, it := range itemsA {
		sumTopupA += it.TopupMoneyCents
		sumCommissionA += it.CommissionCents
	}
	require.Equal(t, stmtA.TotalTopupCents, sumTopupA)
	require.Equal(t, stmtA.TotalCommissionCents, sumCommissionA)

	// Dist B：冻结期流水（2026-09）不入当期
	require.Equal(t, stmtPeriod, stmtB.Period)
	require.EqualValues(t, 5000, stmtB.TotalTopupCents)
	require.EqualValues(t, 500, stmtB.TotalCommissionCents)
	itemsB := statementItemsOf(t, stmtB.Id)
	require.Len(t, itemsB, 1)

	// 零流水分销商：无账单（Pitfall 5 空账单污染）
	var cntC int64
	require.NoError(t, DB.Model(&CommissionStatement{}).Where("distributor_id = ?", distC.Id).Count(&cntC).Error)
	require.Zero(t, cntC, "distributor without pending flows must not get an empty statement")

	// 流水终态：选中的翻 available + statement_id 回填；未选中的原状
	var gotA1, gotA2, gotA3, gotB1, gotB2 CommissionFlow
	require.NoError(t, DB.First(&gotA1, fA1.Id).Error)
	require.NoError(t, DB.First(&gotA2, fA2.Id).Error)
	require.NoError(t, DB.First(&gotA3, fA3.Id).Error)
	require.NoError(t, DB.First(&gotB1, fB1.Id).Error)
	require.NoError(t, DB.First(&gotB2, fB2.Id).Error)
	require.Equal(t, CommissionFlowAvailable, gotA1.Status)
	require.Equal(t, stmtA.Id, gotA1.StatementId)
	require.Equal(t, CommissionFlowAvailable, gotA2.Status)
	require.Equal(t, stmtA.Id, gotA2.StatementId)
	require.Equal(t, CommissionFlowAvailable, gotA3.Status, "pre-existing available flow untouched")
	require.Zero(t, gotA3.StatementId, "available flow must not be re-statemented")
	require.Equal(t, CommissionFlowAvailable, gotB1.Status)
	require.Equal(t, stmtB.Id, gotB1.StatementId)
	require.Equal(t, CommissionFlowPending, gotB2.Status, "frozen-period flow stays pending (acceptance 5)")
	require.Zero(t, gotB2.StatementId)
}

// TestStatementItemsSnapshot 明细快照（STMT-02 + 附录 D5.2）：
// commission/reversal 批量 join top_ups 快照支付方式/渠道/到账时间；
// manual 流水（trade_no=""）缺省 PaymentMethod="manual"、PaymentProvider=""、CompleteTime=CreatedAt；
// 每笔流水一条 item（不做净额合并），FlowId 可回溯，负数冲销原样入 items。
func TestStatementItemsSnapshot(t *testing.T) {
	setupCommissionTestDB(t)

	dist := seedStatementUser(t, common.RoleDistributorUser)
	cust := seedStatementUser(t, common.RoleCommonUser)

	complete1 := time.Date(2026, 8, 10, 10, 0, 0, 0, time.Local).Unix()
	complete2 := time.Date(2026, 8, 20, 18, 30, 0, 0, time.Local).Unix()
	f1 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, tradeNo: "TSNAP-1",
		topupMoneyCents: 10000, rateBp: 1000, amountCents: 1000, period: "2026-08",
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "stripe", paymentProvider: "stripe", completeTime: complete1},
	})
	f2 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, tradeNo: "TSNAP-2",
		topupMoneyCents: 5000, rateBp: 1000, amountCents: 500, period: "2026-08",
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "balance", paymentProvider: "creem", completeTime: complete2},
	})
	f3 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, flowType: CommissionFlowManualCredit, tradeNo: "",
		topupMoneyCents: 0, rateBp: 0, amountCents: 200, period: "2026-08",
	})
	f4 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, flowType: CommissionFlowReversal, tradeNo: "TSNAP-1",
		topupMoneyCents: 10000, rateBp: 1000, amountCents: -400, period: "2026-08",
	})

	n, err := GenerateDueStatements(statementNow())
	require.NoError(t, err)
	require.Equal(t, 1, n)

	var stmt CommissionStatement
	require.NoError(t, DB.Where("distributor_id = ?", dist.Id).First(&stmt).Error)
	items := statementItemsOf(t, stmt.Id)
	require.Len(t, items, 4, "one item per flow: 2 commission + 1 manual + 1 reversal (no netting)")

	byFlow := make(map[int64]CommissionStatementItem, len(items))
	for _, it := range items {
		byFlow[it.FlowId] = it
	}

	// 快照与 TopUp 一致（批量 trade_no IN 查询，非逐条）
	it1 := byFlow[f1.Id]
	require.Equal(t, "TSNAP-1", it1.BillingNo)
	require.Equal(t, "stripe", it1.PaymentMethod)
	require.Equal(t, "stripe", it1.PaymentProvider)
	require.Equal(t, complete1, it1.CompleteTime)
	require.Equal(t, f1.CustomerId, it1.CustomerId)
	require.EqualValues(t, 10000, it1.TopupMoneyCents)
	require.EqualValues(t, 1000, it1.CommissionCents)

	it2 := byFlow[f2.Id]
	require.Equal(t, "balance", it2.PaymentMethod)
	require.Equal(t, "creem", it2.PaymentProvider)
	require.Equal(t, complete2, it2.CompleteTime)

	// manual 流水缺省（D5.2）
	it3 := byFlow[f3.Id]
	require.Equal(t, "manual", it3.PaymentMethod)
	require.Empty(t, it3.PaymentProvider)
	require.Equal(t, f3.CreatedAt, it3.CompleteTime, "manual flows use record time as display complete_time")
	require.EqualValues(t, 200, it3.CommissionCents)

	// reversal 正常 join top_ups，负数原样入 items
	it4 := byFlow[f4.Id]
	require.Equal(t, "stripe", it4.PaymentMethod, "reversal joins top_ups via original trade_no")
	require.Equal(t, "stripe", it4.PaymentProvider)
	require.Equal(t, complete1, it4.CompleteTime)
	require.EqualValues(t, -400, it4.CommissionCents, "negative reversal enters items as-is")

	// 对账恒等式
	var sumCommission, sumTopup int64
	for _, it := range items {
		sumCommission += it.CommissionCents
		sumTopup += it.TopupMoneyCents
	}
	require.Equal(t, stmt.TotalCommissionCents, sumCommission, "1300 = 1000+500+200-400")
	require.Equal(t, stmt.TotalTopupCents, sumTopup)
}

// TestStatementIdempotency 幂等与防重（STMT-03 + 契约 §5 三层防重）：
// 首次出账流水全翻 available + statement_id 回填、LockedAt 生成即定；
// 同 now 二次调用零新账单零新 items（预检 skip 路径）；
// 唯一索引兜底由迁移测试 HasIndex("uk_stmt_period") 双证（冲突 skip 路径见实现代码审查）。
func TestStatementIdempotency(t *testing.T) {
	setupCommissionTestDB(t)

	now := statementNow()
	dist := seedStatementUser(t, common.RoleDistributorUser)
	cust := seedStatementUser(t, common.RoleCommonUser)

	f1 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, tradeNo: "TIDEM-1",
		topupMoneyCents: 10000, rateBp: 1000, amountCents: 1000, period: "2026-08",
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "epay", paymentProvider: "epay", completeTime: 1755000000},
	})
	f2 := seedFlow(t, seedFlowOpts{
		distributorId: dist.Id, customerId: cust.Id, tradeNo: "TIDEM-2",
		topupMoneyCents: 2000, rateBp: 1000, amountCents: 200, period: "2026-08",
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "epay", paymentProvider: "epay", completeTime: 1755000000},
	})

	n1, err := GenerateDueStatements(now)
	require.NoError(t, err)
	require.Equal(t, 1, n1)

	var stmt CommissionStatement
	require.NoError(t, DB.Where("distributor_id = ?", dist.Id).First(&stmt).Error)
	require.Positive(t, stmt.LockedAt, "LockedAt set at generation (STMT-03)")

	// 终态断言（翻转在 generateStatementTx 内部按 ID 列表执行 + RowsAffected 断言，此处验证终态）
	var flows []CommissionFlow
	require.NoError(t, DB.Where("distributor_id = ?", dist.Id).Order("id ASC").Find(&flows).Error)
	require.Len(t, flows, 2)
	for _, f := range flows {
		require.Equal(t, CommissionFlowAvailable, f.Status)
		require.Equal(t, stmt.Id, f.StatementId)
	}
	items := statementItemsOf(t, stmt.Id)
	require.Len(t, items, 2)

	// 二次调用：全部预检 skip → count=0，零新账单零新 items，流水终态不变
	n2, err := GenerateDueStatements(now)
	require.NoError(t, err)
	require.Zero(t, n2, "second call must be a no-op (precheck skip)")

	var stmtCount, itemCount int64
	require.NoError(t, DB.Model(&CommissionStatement{}).Count(&stmtCount).Error)
	require.EqualValues(t, 1, stmtCount, "no duplicate statement")
	require.NoError(t, DB.Model(&CommissionStatementItem{}).Count(&itemCount).Error)
	require.EqualValues(t, 2, itemCount, "no duplicate items")

	var flowsAgain []CommissionFlow
	require.NoError(t, DB.Where("distributor_id = ?", dist.Id).Order("id ASC").Find(&flowsAgain).Error)
	require.Equal(t, flows, flowsAgain, "flow terminal state unchanged")

	// 供回溯断言（编译期使用，防止 f1/f2 未使用告警）
	require.NotZero(t, f1.Id)
	require.NotZero(t, f2.Id)
}

// statementItemsOf 按账单 Id 取明细（id ASC，稳定断言顺序）。
func statementItemsOf(t *testing.T, statementId int64) []CommissionStatementItem {
	t.Helper()
	var items []CommissionStatementItem
	require.NoError(t, DB.Where("statement_id = ?", statementId).Order("id ASC").Find(&items).Error)
	return items
}
