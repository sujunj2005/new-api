package service

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件为出账任务调度壳（契约 01-CONTRACT.md 附录 D2 冻结：subscription_reset 模式）
// 的单元测试：payoutDayClamped 纯函数（短月 clamp，Pitfall 2）+
// runStatementGenerationOnce 参数化时间调度判定（宽进补跑 + 幂等 + D5.1 不受门控）。
//
// 测试库纪律（Phase 3 教训）：service 包 TestMain（task_billing_test.go）为共享库
// 且不含 statement 三表，本文件自建独立内存库并覆盖 model.DB/LOG_DB，t.Cleanup 恢复；
// 禁 SetMaxOpenConns(1)（出账事务内嵌套查询需多连接，单连接必死锁）。

// statementSvcTestSeq 测试数据序列号：保证 Username/AffCode/BillingNo 唯一。
var statementSvcTestSeq atomic.Int64

// setupStatementServiceTestDB 独立内存 SQLite 库（_busy_timeout=30000），
// AutoMigrate 出账链路全部表，覆盖全局 model.DB/LOG_DB，随 t.Cleanup 恢复并关闭。
func setupStatementServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	origDB, origLogDB := model.DB, model.LOG_DB
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.TopUp{}, &model.CommissionFlow{}, &model.CommissionRate{},
		&model.CommissionStatement{}, &model.CommissionStatementItem{}, &model.StatementAdjustment{}, &model.Log{},
	))
	t.Cleanup(func() {
		// 先恢复全局 DB 再关闭本测试库：后续测试依赖共享 TestMain 的连接
		model.DB, model.LOG_DB = origDB, origLogDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// withPayoutDay 临时设置出账日并在测试结束时恢复（service 包无法复用 model 包私有 fixture）。
func withPayoutDay(t *testing.T, n int) {
	t.Helper()
	orig := common.CommissionPayoutDay
	common.CommissionPayoutDay = n
	t.Cleanup(func() { common.CommissionPayoutDay = orig })
}

// seedStatementSvcUser 建最小用户行（分销商/客户通用）。
func seedStatementSvcUser(t *testing.T, role int) *model.User {
	t.Helper()
	seq := statementSvcTestSeq.Add(1)
	u := &model.User{
		Username: fmt.Sprintf("svcu%d", seq),
		Password: "testpass1234",
		Role:     role,
		Status:   common.UserStatusEnabled,
		AffCode:  fmt.Sprintf("SB%d", seq),
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// seedStatementFlow 直插一条 pending 佣金流水（调度测试不依赖充值链路）。
func seedStatementFlow(t *testing.T, distributorId, customerId int, period string) model.CommissionFlow {
	t.Helper()
	seq := statementSvcTestSeq.Add(1)
	f := model.CommissionFlow{
		BillingNo:     fmt.Sprintf("SVC-T%d", seq),
		FlowType:      model.CommissionFlowCommission,
		TradeNo:       fmt.Sprintf("SVC-T%d", seq),
		CustomerId:    customerId,
		DistributorId: distributorId,
		TopupMoneyCents: 10000,
		RateBp:        1000,
		AmountCents:   1000,
		Status:        model.CommissionFlowPending,
		Period:        period,
	}
	require.NoError(t, model.DB.Create(&f).Error)
	return f
}

// countSvcStatements 当前库的对账单行数。
func countSvcStatements(t *testing.T) int64 {
	t.Helper()
	var cnt int64
	require.NoError(t, model.DB.Model(&model.CommissionStatement{}).Count(&cnt).Error)
	return cnt
}

// TestPayoutDayClamp 短月 clamp（契约 §3.4：当月天数不足取最后一天，Pitfall 2）：
// payoutDay=31 的 2 月缩到 28/29（否则 2 月出账永久跳过），3 月不变；payoutDay=8 恒 8。
func TestPayoutDayClamp(t *testing.T) {
	cases := []struct {
		name      string
		payoutDay int
		now       time.Time
		want      int
	}{
		{"feb_non_leap_clamps_to_28", 31, time.Date(2026, 2, 15, 0, 0, 0, 0, time.Local), 28},
		{"feb_leap_clamps_to_29", 31, time.Date(2024, 2, 15, 0, 0, 0, 0, time.Local), 29},
		{"march_31_unchanged", 31, time.Date(2026, 3, 15, 0, 0, 0, 0, time.Local), 31},
		{"day_8_always_8_even_feb", 8, time.Date(2026, 2, 3, 0, 0, 0, 0, time.Local), 8},
		{"dec_31_unchanged", 31, time.Date(2026, 12, 31, 0, 0, 0, 0, time.Local), 31},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			withPayoutDay(t, tc.payoutDay)
			require.Equal(t, tc.want, payoutDayClamped(tc.now))
		})
	}
}

// TestStatementTaskSchedule 调度判定矩阵（STMT-01 + 附录 D2 宽进补跑 + D5.1）：
// day < payoutDay 不出账；出账日当天及当月任意时刻（补跑）均出账上期账单；
// 同月多次调用仅首次产生账单；分佣总开关关闭时出账照常执行。
func TestStatementTaskSchedule(t *testing.T) {
	seedDistributorWithFlow := func(t *testing.T) (*model.User, model.CommissionFlow) {
		dist := seedStatementSvcUser(t, common.RoleDistributorUser)
		cust := seedStatementSvcUser(t, common.RoleCommonUser)
		flow := seedStatementFlow(t, dist.Id, cust.Id, "2026-08")
		return dist, flow
	}

	t.Run("before_payout_day_no_statement", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		dist, flow := seedDistributorWithFlow(t)

		runStatementGenerationOnce(time.Date(2026, 9, 1, 8, 0, 0, 0, time.Local))

		require.Zero(t, countSvcStatements(t), "day < payoutDay must not generate statements")
		var got model.CommissionFlow
		require.NoError(t, model.DB.First(&got, flow.Id).Error)
		require.Equal(t, model.CommissionFlowPending, got.Status, "flow untouched before payout day")
		require.Zero(t, got.StatementId)
	})

	t.Run("on_payout_day_generates_prev_period", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		dist, flow := seedDistributorWithFlow(t)

		runStatementGenerationOnce(time.Date(2026, 9, 8, 0, 1, 0, 0, time.Local))

		require.EqualValues(t, 1, countSvcStatements(t))
		var stmt model.CommissionStatement
		require.NoError(t, model.DB.Where("distributor_id = ?", dist.Id).First(&stmt).Error)
		require.Equal(t, "2026-08", stmt.Period, "statement period = previous calendar month")
		require.Positive(t, stmt.LockedAt)
		require.Equal(t, model.StatementPayable, stmt.Status)
		var got model.CommissionFlow
		require.NoError(t, model.DB.First(&got, flow.Id).Error)
		require.Equal(t, model.CommissionFlowAvailable, got.Status)
		require.Equal(t, stmt.Id, got.StatementId)
	})

	t.Run("catch_up_mid_month_after_downtime", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		dist, _ := seedDistributorWithFlow(t)

		// 进程在出账日后才启动/恢复：宽进语义（day >= clamp(payoutDay)）当月内任意时刻补齐
		runStatementGenerationOnce(time.Date(2026, 9, 20, 15, 0, 0, 0, time.Local))

		require.EqualValues(t, 1, countSvcStatements(t), "catch-up run must generate the missed period")
		var stmt model.CommissionStatement
		require.NoError(t, model.DB.Where("distributor_id = ?", dist.Id).First(&stmt).Error)
		require.Equal(t, "2026-08", stmt.Period)
	})

	t.Run("idempotent_across_same_month_reruns", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		seedDistributorWithFlow(t)

		// 同月内多次调用（9/8、9/15、9/20）：仅首次产生账单，其余 no-op 零重复
		runStatementGenerationOnce(time.Date(2026, 9, 8, 0, 1, 0, 0, time.Local))
		require.EqualValues(t, 1, countSvcStatements(t))
		runStatementGenerationOnce(time.Date(2026, 9, 15, 10, 0, 0, 0, time.Local))
		runStatementGenerationOnce(time.Date(2026, 9, 20, 23, 0, 0, 0, time.Local))
		require.EqualValues(t, 1, countSvcStatements(t), "reruns within the same month must be no-ops")

		var itemCount int64
		require.NoError(t, model.DB.Model(&model.CommissionStatementItem{}).Count(&itemCount).Error)
		require.EqualValues(t, 1, itemCount, "no duplicate items")
	})

	t.Run("runs_with_commission_gate_off", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		dist, _ := seedDistributorWithFlow(t)

		// D5.1：出账不受分佣总开关门控（开关只控制新增记账入口）
		origGate := common.CommissionEnabled
		common.CommissionEnabled = false
		t.Cleanup(func() { common.CommissionEnabled = origGate })

		runStatementGenerationOnce(time.Date(2026, 9, 8, 0, 1, 0, 0, time.Local))

		require.EqualValues(t, 1, countSvcStatements(t), "statement generation must ignore the master commission switch (D5.1)")
		var stmt model.CommissionStatement
		require.NoError(t, model.DB.Where("distributor_id = ?", dist.Id).First(&stmt).Error)
		require.Equal(t, "2026-08", stmt.Period)
	})

	t.Run("concurrent_entry_cas_single_effect", func(t *testing.T) {
		setupStatementServiceTestDB(t)
		withPayoutDay(t, 8)
		seedDistributorWithFlow(t)

		// statementTaskRunning CAS：并发二次进入仅一次生效（或被幂等预检兜住）
		var wg sync.WaitGroup
		wg.Add(2)
		for i := 0; i < 2; i++ {
			go func() {
				defer wg.Done()
				runStatementGenerationOnce(time.Date(2026, 9, 8, 0, 1, 0, 0, time.Local))
			}()
		}
		wg.Wait()

		require.EqualValues(t, 1, countSvcStatements(t), "exactly one statement under concurrent entry")
		var flowCount int64
		require.NoError(t, model.DB.Model(&model.CommissionFlow{}).
			Where("status = ?", model.CommissionFlowAvailable).Count(&flowCount).Error)
		require.EqualValues(t, 1, flowCount, "flow flipped exactly once")
	})
}
