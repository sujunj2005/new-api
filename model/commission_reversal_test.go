package model

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本文件为冲销链路（契约 01-CONTRACT.md §3.5 ReverseCommissionTx + 附录 C1 VoidTopUp）的单元测试。
// 复用 03-01 测试基建：setupCommissionTestDB / withCommissionEnabled / seedCommissionFixture（勿重建）。
//
// VoidTopUp 的端到端事务语义（同生共死/负余额/半作废回滚）在 controller/topup_void_test.go
// 集成测试覆盖；本文件仅覆盖 ReverseCommissionTx 四分支，VoidTopUp 以 go build 保证编译。

// seedStatementedCommission 通过真实记账路径产生一条 pending 原 commission 流水，
// 固定 CompleteTime 使 Period 可断言（"2026-08"）。
func seedStatementedCommission(t *testing.T) *commissionFixture {
	t.Helper()
	f := seedCommissionFixture(t, seedCommissionOpts{
		paymentProvider: PaymentProviderEpay,
		money:           100.00,
		amount:          1000,
		rateBp:          1000,
	})
	f.TopUp.CompleteTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local).Unix()
	require.NoError(t, DB.Model(f.TopUp).Update("complete_time", f.TopUp.CompleteTime).Error)
	require.NoError(t, RecordCommissionTx(DB, f.TopUp))
	return f
}

// TestReverseCommissionTx 四分支矩阵（契约 §3.5 冻结语义 + COMM-04）：
//  1. 无原 commission 流水（开关期充值/未记账）→ no-op 返回 nil
//  2. pending 原流水 → 恰好 1 条负数 reversal（同账期/快照照抄/RelatedFlowId/reason 透传），原流水保留不动
//  3. available 原流水（已出账）→ ErrAlreadyStatemented，零新流水（禁止自动改历史，§7#4）
//  4. 同单二次直调 → uk_flow_billing 唯一索引拒绝（同单冲销唯一，§3.1.1）
func TestReverseCommissionTx(t *testing.T) {
	withCommissionEnabled(t, true)

	t.Run("no_flow_no_op", func(t *testing.T) {
		setupCommissionTestDB(t)
		f := seedCommissionFixture(t, seedCommissionOpts{
			paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000,
		})

		require.NoError(t, ReverseCommissionTx(DB, f.TopUp.TradeNo, "测试作废"))

		var count int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Count(&count).Error)
		require.Zero(t, count, "未记账充值单冲销必须为 no-op（返回 nil 零流水）")
	})

	t.Run("pending_reversal_negative_flow", func(t *testing.T) {
		setupCommissionTestDB(t)
		f := seedStatementedCommission(t)

		require.NoError(t, ReverseCommissionTx(DB, f.TopUp.TradeNo, "客户退款作废"))

		var flows []CommissionFlow
		require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).Order("id ASC").Find(&flows).Error)
		require.Len(t, flows, 2, "恰好新增 1 条流水：原 commission + reversal")

		original, reversal := flows[0], flows[1]
		require.Equal(t, CommissionFlowCommission, original.FlowType)

		// reversal 全字段断言（契约 §3.5 + COMM-04）
		require.Equal(t, CommissionFlowReversal, reversal.FlowType)
		require.Equal(t, f.TopUp.TradeNo, reversal.BillingNo, "reversal billing_no = trade_no（§3.1.1 幂等键）")
		require.Equal(t, f.TopUp.TradeNo, reversal.TradeNo)
		require.Equal(t, -original.AmountCents, reversal.AmountCents, "冲销为负数（-原.amount_cents）")
		require.Equal(t, original.Id, reversal.RelatedFlowId, "RelatedFlowId 指向原流水")
		require.Equal(t, original.CustomerId, reversal.CustomerId, "customer_id 照抄原流水")
		require.Equal(t, original.DistributorId, reversal.DistributorId, "distributor_id 照抄原流水")
		require.Equal(t, original.TopupMoneyCents, reversal.TopupMoneyCents, "topup_money_cents 照抄原流水")
		require.Equal(t, original.RateBp, reversal.RateBp, "rate_bp 照抄原流水")
		require.Equal(t, original.Period, reversal.Period, "period 照抄原流水——同账期净额抵减（勿用当前月）")
		require.Equal(t, "2026-08", reversal.Period, "Period=原流水账期（2026-08）而非当前月")
		require.Equal(t, "客户退款作废", reversal.Reason, "reason 透传")
		require.Equal(t, CommissionFlowPending, reversal.Status, "reversal 初始 pending")
		require.Zero(t, reversal.OperatorId, "系统流水 operator_id=0")

		// 原流水保留不删不改（COMM-04 不删除语义）
		var after CommissionFlow
		require.NoError(t, DB.Where("id = ?", original.Id).First(&after).Error)
		require.Equal(t, CommissionFlowPending, after.Status, "原流水状态不得被修改")
		require.Equal(t, original.AmountCents, after.AmountCents, "原流水金额不得被修改")
	})

	t.Run("available_already_statemented", func(t *testing.T) {
		setupCommissionTestDB(t)
		f := seedStatementedCommission(t)
		// 直改流水行 status=available 模拟已出账
		require.NoError(t, DB.Model(&CommissionFlow{}).
			Where("trade_no = ? AND flow_type = ?", f.TopUp.TradeNo, CommissionFlowCommission).
			Update("status", CommissionFlowAvailable).Error)

		err := ReverseCommissionTx(DB, f.TopUp.TradeNo, "已出账作废尝试")
		require.ErrorIs(t, err, ErrAlreadyStatemented, "已出账 → ErrAlreadyStatemented（§7#4 禁止自动改历史）")

		var count int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Where("flow_type = ?", CommissionFlowReversal).Count(&count).Error)
		require.Zero(t, count, "已出账拒绝时零新流水")
	})

	t.Run("double_reverse_unique_index", func(t *testing.T) {
		setupCommissionTestDB(t)
		f := seedStatementedCommission(t)

		require.NoError(t, ReverseCommissionTx(DB, f.TopUp.TradeNo, "首次冲销"))
		// 二次直调：原 commission 流水仍 pending（冲销不改原记录），再次尝试生成 reversal
		// → uk_flow_billing(billing_no, flow_type) 唯一索引冲突（同单冲销唯一，§3.1.1）
		err := ReverseCommissionTx(DB, f.TopUp.TradeNo, "二次冲销")
		require.Error(t, err, "同单二次冲销必须被 uk_flow_billing 拒绝")

		var count int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Where("flow_type = ?", CommissionFlowReversal).Count(&count).Error)
		require.Equal(t, int64(1), count, "reversal 仍 1 条——同单冲销唯一由 DB 约束保证")
	})
}
