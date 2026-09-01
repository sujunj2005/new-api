package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// TestCommissionRateSnapshot RATE-02 快照不变性集成测试（03-02，复用 03-01 记账链路）：
// 先记账（rate_bp=1000）→ UpsertRateWithHistory 改为 500 → 再记账 →
// 两笔流水各自持有写入时刻的比例快照且互不影响（写入即定，无任何更新路径）；
// GetRateHistories 返回 2 行历史（operator/old/new 链条 0→1000→500，RATE-03）。
func TestCommissionRateSnapshot(t *testing.T) {
	setupCommissionTestDB(t)
	withCommissionEnabled(t, true)

	// 分销商 + 客户 + 待记账充值单（不直建 rate 行，走 UpsertRateWithHistory 建立 1000，
	// 使历史链条从首次设置开始完整可断言）
	f := seedCommissionFixture(t, seedCommissionOpts{
		paymentProvider: PaymentProviderEpay,
		money:           100.00,
		amount:          1000,
		rateBp:          0,
	})
	require.NoError(t, UpsertRateWithHistory(DB, f.Distributor.Id, 1000, 1))

	// 第一笔记账：rate_bp=1000
	require.NoError(t, RecordCommissionTx(DB, f.TopUp))
	var flow1 CommissionFlow
	require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&flow1).Error)
	require.Equal(t, 1000, flow1.RateBp, "第一笔流水快照 1000")
	require.Equal(t, int64(10000), flow1.TopupMoneyCents)
	require.Equal(t, int64(1000), flow1.AmountCents, "10000 分 × 1000bp / 10000 = 1000 分")

	// 改比例 1000 → 500（rate + history 同事务，先 rate 后 history）
	require.NoError(t, UpsertRateWithHistory(DB, f.Distributor.Id, 500, 2))

	// 第二笔记账：同客户同分销商的新充值单 → 使用新比例 500
	topup2 := &TopUp{
		UserId:          f.Customer.Id,
		Amount:          1000,
		Money:           200.00,
		TradeNo:         f.TopUp.TradeNo + "-B",
		PaymentProvider: PaymentProviderEpay,
		PaymentMethod:   PaymentProviderEpay,
		CreateTime:      common.GetTimestamp(),
		Status:          common.TopUpStatusPending,
	}
	require.NoError(t, DB.Create(topup2).Error)
	require.NoError(t, RecordCommissionTx(DB, topup2))
	var flow2 CommissionFlow
	require.NoError(t, DB.Where("trade_no = ?", topup2.TradeNo).First(&flow2).Error)
	require.Equal(t, 500, flow2.RateBp, "第二笔流水使用变更后的比例 500")
	require.Equal(t, int64(20000), flow2.TopupMoneyCents)
	require.Equal(t, int64(1000), flow2.AmountCents, "20000 分 × 500bp / 10000 = 1000 分")

	// 重读第一笔：快照不变（RATE-02 写入即定，改比例不影响任何已生成流水）
	var flow1ReRead CommissionFlow
	require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&flow1ReRead).Error)
	require.Equal(t, 1000, flow1ReRead.RateBp, "改比例不得影响已生成流水（RATE-02）")
	require.Equal(t, flow1.AmountCents, flow1ReRead.AmountCents)

	// 历史：2 行（operator/old/new 链条 0→1000→500）
	hists, total, err := GetRateHistories(f.Distributor.Id, 0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), total)
	require.Len(t, hists, 2)
	// id DESC：最新在前（1000→500，operator 2），最早在后（0→1000，operator 1）
	require.Equal(t, 1000, hists[0].OldRateBp)
	require.Equal(t, 500, hists[0].NewRateBp)
	require.Equal(t, 2, hists[0].OperatorId)
	require.Equal(t, 0, hists[1].OldRateBp)
	require.Equal(t, 1000, hists[1].NewRateBp)
	require.Equal(t, 1, hists[1].OperatorId)
}
