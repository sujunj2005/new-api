package model

import (
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 本文件为 5 个事务型收敛点挂载的集成测试（契约 01-CONTRACT.md §3.5 事务型语义 + ATTR-03）。
//
// 挂载语义验证目标：
//   - 每个事务型收敛点在「quota/用户更新成功后、return nil 前」同事务调用 RecordCommissionTx（同生共死）
//   - 首次到账 → 恰好 1 条 pending 流水（含 money_cents 快照）
//   - 重放第二次调用 → 幂等门在挂载之前（静默型 return nil / 报错型 return error），两种形态下流水数均仍为 1
//
// 测试库纪律：复用 commission_test.go 的 setupCommissionTestDB（独立内存库 +
// _busy_timeout=30000，禁 MaxOpenConns(1)——Pitfall 1），勿依赖共享 TestMain。
// 收敛点均为纯 DB 函数，无需网关（RESEARCH Assumption A1）。

func TestCommissionMounting(t *testing.T) {
	cases := []struct {
		name      string
		opts      seedCommissionOpts
		run       func(tradeNo string) error
		replayErr bool // 重放第二次调用：报错型（Stripe/Creem 幂等门）true；静默型 false
	}{
		{
			name: "manual_complete_topup",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
			run:  func(tradeNo string) error { return ManualCompleteTopUp(tradeNo, "127.0.0.1") },
		},
		{
			name:      "recharge_stripe",
			opts:      seedCommissionOpts{paymentProvider: PaymentProviderStripe, money: 100.00, amount: 1000, rateBp: 1000},
			run:       func(tradeNo string) error { return Recharge(tradeNo, "cus_test_mounting", "127.0.0.1") },
			replayErr: true,
		},
		{
			name:      "recharge_creem",
			opts:      seedCommissionOpts{paymentProvider: PaymentProviderCreem, money: 100.00, amount: 1000, rateBp: 1000},
			run:       func(tradeNo string) error { return RechargeCreem(tradeNo, "", "", "127.0.0.1") }, // customerEmail 传空串跳过邮箱回填
			replayErr: true,
		},
		{
			name: "recharge_waffo",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderWaffo, money: 100.00, amount: 1000, rateBp: 1000},
			run:  func(tradeNo string) error { return RechargeWaffo(tradeNo, "127.0.0.1") },
		},
		{
			name: "recharge_waffo_pancake",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderWaffoPancake, money: 100.00, amount: 1000, rateBp: 1000},
			run:  func(tradeNo string) error { return RechargeWaffoPancake(tradeNo) },
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			setupCommissionTestDB(t)
			withCommissionEnabled(t, true)

			f := seedCommissionFixture(t, tc.opts)

			// 首次到账：充值成功 + 恰好 1 条 pending 流水（快照正确）
			require.NoError(t, tc.run(f.TopUp.TradeNo), "first convergence call must succeed")

			var flows []CommissionFlow
			require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).Find(&flows).Error)
			require.Len(t, flows, 1, "exactly one flow after first completion")
			flow := flows[0]
			require.Equal(t, f.TopUp.TradeNo, flow.BillingNo)
			require.Equal(t, CommissionFlowCommission, flow.FlowType)
			require.Equal(t, f.Customer.Id, flow.CustomerId)
			require.Equal(t, f.Distributor.Id, flow.DistributorId)
			require.Equal(t, int64(10000), flow.TopupMoneyCents, "topup_money_cents = Money*100 快照")
			require.Equal(t, 1000, flow.RateBp)
			require.Equal(t, int64(1000), flow.AmountCents)
			require.Equal(t, CommissionFlowPending, flow.Status)
			// Period 与落库 CompleteTime 自洽（本地时区自然月，契约 C3）
			var topupRow TopUp
			require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&topupRow).Error)
			require.NotZero(t, topupRow.CompleteTime, "convergence point must set CompleteTime")
			require.Equal(t, time.Unix(topupRow.CompleteTime, 0).Format("2006-01"), flow.Period)

			// 重放第二次调用：幂等门在挂载之前，不可达记账——流水数仍为 1
			err2 := tc.run(f.TopUp.TradeNo)
			if tc.replayErr {
				require.Error(t, err2, "error-shaped gate (Stripe/Creem) must reject replay")
			} else {
				require.NoError(t, err2, "silent gate (Manual/Waffo/WaffoPancake) must swallow replay")
			}
			var count int64
			require.NoError(t, DB.Model(&CommissionFlow{}).Where("trade_no = ?", f.TopUp.TradeNo).Count(&count).Error)
			require.Equal(t, int64(1), count, "replay must stay unreachable to commission (zero duplicate flows)")
		})
	}
}

// TestCommissionMounting_GateOff 门控关闭时全链路零流水且充值正常完成（Pitfall 3 不放大）。
// 在 TestCommissionGate 已覆盖 ManualCompleteTopUp；此处补 Stripe 渠道抽查。
func TestCommissionMounting_GateOff(t *testing.T) {
	setupCommissionTestDB(t)
	withCommissionEnabled(t, false)

	f := seedCommissionFixture(t, seedCommissionOpts{
		paymentProvider: PaymentProviderStripe,
		money:           100.00,
		amount:          1000,
		rateBp:          1000,
	})
	require.NoError(t, Recharge(f.TopUp.TradeNo, "cus_test_gateoff", "127.0.0.1"), "gate off: topup itself must succeed")

	var topupRow TopUp
	require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&topupRow).Error)
	require.Equal(t, common.TopUpStatusSuccess, topupRow.Status)

	var count int64
	require.NoError(t, DB.Model(&CommissionFlow{}).Count(&count).Error)
	require.Zero(t, count, "gate off: zero flows")
}
