package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 佣金仪表盘聚合测试（06-01，契约 01-CONTRACT.md §4.3 A9 冻结四字段 + v1.5 附录 F2 笔数扩展）。
// 四项口径钉死（OQ-1/D-09 裁决）：
//   ① 当期未出账：仅未出账状态流水按佣金流水金额求和（已入账流水绝不混入，Pitfall 2 双向断言）
//   ② 历史已出账：全部状态账单按结算额汇总（含调整单最终应付）
//   ③ 已提现：仅已打款提现单按关联账单结算额汇总（其余四态不计入）
//   ④ 待提现：仅应付状态账单按结算额汇总（提现中/已结清不计入）
// 四卡对账恒等式：已出账 = 已提现 + 待提现 + 提现中。

// TestGetCommissionDashboard A9 四项聚合全分支（口径双向钉死 + 笔数对应 + 恒等式 + 零数据）。
func TestGetCommissionDashboard(t *testing.T) {
	t.Run("pending_predicate_both_ways", func(t *testing.T) {
		setupCommissionTestDB(t)
		// 同分销商：未出账 1 笔 1000 + 已入账 1 笔 500 → 只计未出账（双向钉死）
		seedFlow(t, seedFlowOpts{distributorId: 61, customerId: 62, amountCents: 1000, status: CommissionFlowPending, period: "2026-09"})
		seedFlow(t, seedFlowOpts{distributorId: 61, customerId: 62, amountCents: 500, status: CommissionFlowAvailable, period: "2026-08"})
		d, err := GetCommissionDashboard(61)
		require.NoError(t, err)
		require.Equal(t, int64(1000), d.CurrentPendingCents, "已入账流水绝不混入（谓词①）")
		require.Equal(t, int64(1), d.CurrentPendingCount)
	})

	t.Run("statement_scope_settle_all_statuses", func(t *testing.T) {
		setupCommissionTestDB(t)
		// OQ-1 settle 口径：结算额 = 佣金累计 + 调整额（102000 = 100000 + 2000，含调整单最终应付）
		seedStatement(t, seedStatementOpts{distributorId: 51, totalCommissionCents: 100000, adjustedCents: 2000, status: StatementPayable})
		seedStatement(t, seedStatementOpts{distributorId: 51, totalCommissionCents: 30000, status: StatementWithdrawing})
		seedStatement(t, seedStatementOpts{distributorId: 51, totalCommissionCents: 30000, status: StatementSettled})
		d, err := GetCommissionDashboard(51)
		require.NoError(t, err)
		// ② 应付/提现中/已结清多状态账单全部按结算额计入
		require.Equal(t, int64(162000), d.TotalStatementCents)
		require.Equal(t, int64(3), d.TotalStatementCount)
		// ④ 仅应付计入：提现中/已结清账单不混入
		require.Equal(t, int64(102000), d.PendingWithdrawCents)
		require.Equal(t, int64(1), d.PendingWithdrawCount)
	})

	t.Run("withdrawn_scope_paid_only", func(t *testing.T) {
		setupCommissionTestDB(t)
		payable := seedStatement(t, seedStatementOpts{distributorId: 41, totalCommissionCents: 102000, status: StatementPayable})
		settled := seedStatement(t, seedStatementOpts{distributorId: 41, totalCommissionCents: 30000, status: StatementSettled})
		// 已打款提现单计入（指向已结清账单，结算额 30000）
		seedWithdrawal(t, seedWithdrawalOpts{statementId: settled.Id, distributorId: 41, status: WithdrawalPaid})
		// 申请中/审核中/已批准/已驳回四态一律不计入（若误计会引入 payable 账单 102000 污染）
		seedWithdrawal(t, seedWithdrawalOpts{statementId: payable.Id, distributorId: 41, status: WithdrawalRejected})
		seedWithdrawal(t, seedWithdrawalOpts{statementId: payable.Id, distributorId: 41, status: WithdrawalPending})
		seedWithdrawal(t, seedWithdrawalOpts{statementId: payable.Id, distributorId: 41, status: WithdrawalReviewing})
		seedWithdrawal(t, seedWithdrawalOpts{statementId: payable.Id, distributorId: 41, status: WithdrawalApproved})
		d, err := GetCommissionDashboard(41)
		require.NoError(t, err)
		require.Equal(t, int64(30000), d.TotalWithdrawnCents, "仅已打款提现单计入（口径③）")
		require.Equal(t, int64(1), d.TotalWithdrawnCount)
	})

	t.Run("identity_equation_and_all_counts", func(t *testing.T) {
		setupCommissionTestDB(t)
		// 受控种子（D-09 恒等式）：应付 50000 + 提现中 30000 + 已结清 30000，
		// 已打款提现单指向已结清账单（paid⇒settled 状态机一致，禁指向提现中账单避免双计数）
		seedStatement(t, seedStatementOpts{distributorId: 31, totalCommissionCents: 50000, status: StatementPayable})
		seedStatement(t, seedStatementOpts{distributorId: 31, totalCommissionCents: 30000, status: StatementWithdrawing})
		settled := seedStatement(t, seedStatementOpts{distributorId: 31, totalCommissionCents: 30000, status: StatementSettled})
		seedWithdrawal(t, seedWithdrawalOpts{statementId: settled.Id, distributorId: 31, status: WithdrawalPaid, voucherNo: "V-ID-1", paidAt: 123})
		d, err := GetCommissionDashboard(31)
		require.NoError(t, err)
		// 四个 cents 字段与四个 count 字段一一对应（同种子分别断言）
		require.Equal(t, int64(0), d.CurrentPendingCents)
		require.Equal(t, int64(0), d.CurrentPendingCount)
		require.Equal(t, int64(110000), d.TotalStatementCents)
		require.Equal(t, int64(3), d.TotalStatementCount)
		require.Equal(t, int64(30000), d.TotalWithdrawnCents)
		require.Equal(t, int64(1), d.TotalWithdrawnCount)
		require.Equal(t, int64(50000), d.PendingWithdrawCents)
		require.Equal(t, int64(1), d.PendingWithdrawCount)
		// 恒等式：已出账 = 已提现 + 待提现 + 提现中（110000 = 30000 + 50000 + 30000）
		require.Equal(t, d.TotalStatementCents, d.TotalWithdrawnCents+d.PendingWithdrawCents+30000)
	})

	t.Run("zero_data_distributor_all_zero", func(t *testing.T) {
		setupCommissionTestDB(t)
		zero := &User{Username: "dash_zero_dist", Password: "testpass1234", Role: common.RoleDistributorUser, Status: common.UserStatusEnabled, AffCode: "DZ9"}
		require.NoError(t, DB.Create(zero).Error)
		d, err := GetCommissionDashboard(zero.Id)
		require.NoError(t, err)
		// 零数据分销商全 0（合计列空值包裹，禁 NULL 序列化）
		require.Equal(t, &CommissionDashboard{}, d)
	})
}

// TestGetCurrentPending A12 未出账实时预览（三冻结字段 + 与 A9-① 同谓词一致性）。
func TestGetCurrentPending(t *testing.T) {
	t.Run("period_format_and_predicate", func(t *testing.T) {
		setupCommissionTestDB(t)
		seedFlow(t, seedFlowOpts{distributorId: 81, customerId: 82, amountCents: 1000, status: CommissionFlowPending, period: "2026-09"})
		seedFlow(t, seedFlowOpts{distributorId: 81, customerId: 82, amountCents: 500, status: CommissionFlowAvailable, period: "2026-08"})
		cur, err := GetCurrentPending(81)
		require.NoError(t, err)
		require.Regexp(t, `^\d{4}-\d{2}$`, cur.Period, "period 为当前自然月标签")
		require.Equal(t, int64(1000), cur.PendingCents, "已入账流水不计入（与 A9-① 同谓词）")
		require.Equal(t, int64(1), cur.PendingFlows)
	})

	t.Run("same_predicate_as_dashboard", func(t *testing.T) {
		setupCommissionTestDB(t)
		seedFlow(t, seedFlowOpts{distributorId: 91, customerId: 92, amountCents: 300, status: CommissionFlowPending, period: "2026-09"})
		seedFlow(t, seedFlowOpts{distributorId: 91, customerId: 92, amountCents: 40, status: CommissionFlowPending, period: "2026-09"})
		d, err := GetCommissionDashboard(91)
		require.NoError(t, err)
		cur, err := GetCurrentPending(91)
		require.NoError(t, err)
		// A9-① 与 A12 谓词绑死：同种子双函数同值
		require.Equal(t, d.CurrentPendingCents, cur.PendingCents)
		require.Equal(t, d.CurrentPendingCount, cur.PendingFlows)
	})
}
