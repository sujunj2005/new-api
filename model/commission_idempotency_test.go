package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 本文件为佣金幂等双路径测试（契约 01-CONTRACT.md §5 记账防重 + COMM-03）。
//
// 双保险设计验证（Pitfall 4：多实例 LockOrder 内存锁失效的穿透场景）：
//   路径 1（第一重）：收敛点幂等门——同 tradeNo 重放，Status==success 静默 return，
//                     记账挂载在幂等门之后，不可达（零重复）。
//   路径 2（最终防线）：uk_flow_billing(billing_no, flow_type) 唯一索引兜底——
//                     绕过幂等门直调 RecordCommissionTx 第二次 → INSERT 冲突返回 error，
//                     流水数仍为 1（拒绝任何分布式锁/查重-再插两步逻辑）。
//
// 测试库纪律：复用 commission_test.go 的 setupCommissionTestDB（Pitfall 1）。

func TestCommissionIdempotency(t *testing.T) {
	t.Run("replay_via_silent_gate", func(t *testing.T) {
		setupCommissionTestDB(t)
		withCommissionEnabled(t, true)

		f := seedCommissionFixture(t, seedCommissionOpts{
			paymentProvider: PaymentProviderEpay,
			money:           100.00,
			amount:          1000,
			rateBp:          1000,
		})

		// 首次补单：1 条流水
		require.NoError(t, ManualCompleteTopUp(f.TopUp.TradeNo, "127.0.0.1"))
		// 重放（模拟网关重发回调 / 重复补单）：幂等门静默 return
		require.NoError(t, ManualCompleteTopUp(f.TopUp.TradeNo, "127.0.0.1"))

		var count int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Where("trade_no = ?", f.TopUp.TradeNo).Count(&count).Error)
		require.Equal(t, int64(1), count, "silent-gate replay must produce zero duplicate flows")
	})

	t.Run("unique_index_fallback_bypassing_gate", func(t *testing.T) {
		setupCommissionTestDB(t)
		withCommissionEnabled(t, true)

		f := seedCommissionFixture(t, seedCommissionOpts{
			paymentProvider: PaymentProviderEpay,
			money:           100.00,
			amount:          1000,
			rateBp:          1000,
		})

		// 首次补单：1 条流水
		require.NoError(t, ManualCompleteTopUp(f.TopUp.TradeNo, "127.0.0.1"))

		// 绕过幂等门直调记账（模拟多实例部署 LockOrder 进程内内存锁失效的穿透）：
		// uk_flow_billing 唯一索引必须拒绝第二次 INSERT
		err := RecordCommissionTx(DB, f.TopUp)
		require.Error(t, err, "uk_flow_billing unique index must reject duplicate (billing_no, flow_type)")

		var count int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Where("trade_no = ?", f.TopUp.TradeNo).Count(&count).Error)
		require.Equal(t, int64(1), count, "unique-index fallback must keep exactly one flow")
	})
}
