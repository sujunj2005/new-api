package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 本文件为比例管理模型方法（03-02，契约 01-CONTRACT.md §3.2 冻结）的单元测试。
// 测试库纪律同 commission_test.go：setupCommissionTestDB（独立内存库 + _busy_timeout=30000，
// 禁 SetMaxOpenConns(1)——事务内嵌套查询需要多连接，02-04 死锁教训）。

// TestUpsertRateWithHistory 首次建行 / 更新 / 历史同事务分步断言（契约 §3.2）。
// 顺序语义（RESEARCH Anti-Pattern）：先 rate 写入、后 history——history 是 rate 变更的从属记录。
func TestUpsertRateWithHistory(t *testing.T) {
	setupCommissionTestDB(t)

	// 1. 首次设置（无行）：Create CommissionRate 1 行 + History{Old:0, New:rateBp} 1 行
	require.NoError(t, UpsertRateWithHistory(DB, 3, 500, 1))
	var rate CommissionRate
	require.NoError(t, DB.Where("distributor_id = ?", 3).First(&rate).Error)
	require.Equal(t, 500, rate.RateBp, "首次设置写入 rate_bp")
	require.Equal(t, 1, rate.UpdatedBy, "updated_by 记录操作者")

	var hists []CommissionRateHistory
	require.NoError(t, DB.Where("distributor_id = ?", 3).Order("id ASC").Find(&hists).Error)
	require.Len(t, hists, 1, "首次设置恰好 1 行历史")
	require.Equal(t, 0, hists[0].OldRateBp, "无行时 old=0")
	require.Equal(t, 500, hists[0].NewRateBp)
	require.Equal(t, 1, hists[0].OperatorId)

	// 2. 再次修改（500 → 1000，operator 换人）：Updates 同一行 + 新增 1 行历史
	require.NoError(t, UpsertRateWithHistory(DB, 3, 1000, 2))
	var rate2 CommissionRate
	require.NoError(t, DB.Where("distributor_id = ?", 3).First(&rate2).Error)
	require.Equal(t, 1000, rate2.RateBp, "更新后 rate_bp=1000")
	require.Equal(t, 2, rate2.UpdatedBy)
	require.Equal(t, rate.Id, rate2.Id, "更新走同一行（不新建，uk_rate_distributor 每分销商一行）")

	require.NoError(t, DB.Where("distributor_id = ?", 3).Order("id ASC").Find(&hists).Error)
	require.Len(t, hists, 2, "每次变更恰好新增 1 行历史")
	require.Equal(t, 500, hists[1].OldRateBp, "old = 上次生效值")
	require.Equal(t, 1000, hists[1].NewRateBp)
	require.Equal(t, 2, hists[1].OperatorId)
	// old/new 链条：0→500→1000 连续
	require.Equal(t, hists[0].NewRateBp, hists[1].OldRateBp, "old/new 链条连续")

	// 3. 每分销商恰好一行：另一分销商独立建行，不影响分销商 3
	require.NoError(t, UpsertRateWithHistory(DB, 4, 333, 1))
	var cnt int64
	require.NoError(t, DB.Model(&CommissionRate{}).Where("distributor_id = ?", 3).Count(&cnt).Error)
	require.Equal(t, int64(1), cnt, "每分销商恰好一行（uk_rate_distributor）")
}

// TestGetRateHistories 分页 + id DESC（operator/old/new 字段完整，RATE-03）。
func TestGetRateHistories(t *testing.T) {
	setupCommissionTestDB(t)

	require.NoError(t, UpsertRateWithHistory(DB, 7, 500, 1))
	require.NoError(t, UpsertRateWithHistory(DB, 7, 1000, 2))
	require.NoError(t, UpsertRateWithHistory(DB, 7, 2000, 3))
	// 无关分销商隔离
	require.NoError(t, UpsertRateWithHistory(DB, 8, 100, 1))

	hists, total, err := GetRateHistories(7, 0, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), total)
	require.Len(t, hists, 2, "分页大小 2")
	// id DESC：最新在前（2000 → 1000）
	require.Equal(t, 1000, hists[0].OldRateBp)
	require.Equal(t, 2000, hists[0].NewRateBp)
	require.Equal(t, 3, hists[0].OperatorId)
	require.Equal(t, 500, hists[1].OldRateBp)
	require.Equal(t, 1000, hists[1].NewRateBp)
	require.Equal(t, 2, hists[1].OperatorId)

	// 第二页：剩 1 行（最早 0→500）
	hists2, total2, err := GetRateHistories(7, 2, 2)
	require.NoError(t, err)
	require.Equal(t, int64(3), total2)
	require.Len(t, hists2, 1)
	require.Equal(t, 0, hists2[0].OldRateBp)
	require.Equal(t, 500, hists2[0].NewRateBp)
	require.Equal(t, 1, hists2[0].OperatorId)
}

// TestListRates A1 列表（契约 §4.3 A1）：username 冗余（LEFT JOIN users）+
// 干净 count + distributor_id ASC 分页。
func TestListRates(t *testing.T) {
	setupCommissionTestDB(t)

	dA := &User{Username: "listdist_a", Password: "testpass1234", Role: common.RoleDistributorUser, Status: common.UserStatusEnabled, AffCode: "LDA1"}
	require.NoError(t, DB.Create(dA).Error)
	dB := &User{Username: "listdist_b", Password: "testpass1234", Role: common.RoleDistributorUser, Status: common.UserStatusEnabled, AffCode: "LDB1"}
	require.NoError(t, DB.Create(dB).Error)
	// 普通用户无比例行 → 不入列表
	require.NoError(t, DB.Create(&User{Username: "plain_user", Password: "testpass1234", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AffCode: "LPU1"}).Error)

	// 先建 dB 再建 dA，断言排序按 distributor_id ASC 而非插入顺序
	require.NoError(t, UpsertRateWithHistory(DB, dB.Id, 500, 1))
	require.NoError(t, UpsertRateWithHistory(DB, dA.Id, 1000, 1))

	items, total, err := ListRates(0, 10)
	require.NoError(t, err)
	require.Equal(t, int64(2), total, "干净 count：仅 2 行比例配置")
	require.Len(t, items, 2)
	require.Equal(t, dA.Id, items[0].DistributorId, "distributor_id ASC")
	require.Equal(t, "listdist_a", items[0].Username, "A1 username 冗余（LEFT JOIN users）")
	require.Equal(t, 1000, items[0].RateBp)
	// CommissionRate.Id 是比例表自身主键（非 users 主键）——对照实际 rate 行
	var rateA CommissionRate
	require.NoError(t, DB.Where("distributor_id = ?", dA.Id).First(&rateA).Error)
	require.Equal(t, rateA.Id, items[0].Id, "嵌入结构体扁平扫描携带正确主键")
	require.Equal(t, dB.Id, items[1].DistributorId)
	require.Equal(t, "listdist_b", items[1].Username)
	require.Equal(t, 500, items[1].RateBp)
}
