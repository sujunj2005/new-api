package model

import (
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 本文件为 Phase 2 佣金/归属 7 张新表的三库迁移兼容测试（契约 01-CONTRACT.md §6）。
//
// model 包 TestMain 已由 task_cas_test.go 占用（内存库 + initCol），本文件不新增
// TestMain：SQLite 用例在测试函数内创建局部内存库；MySQL/PostgreSQL 用例由
// TEST_MYSQL_DSN / TEST_POSTGRES_DSN 环境变量门控（无 DSN 自动 skip，
// 惯例见 .planning/codebase/TESTING.md「迁移兼容性测试」小节）。
//
// 契约 §6 三库兼容性约定（本测试的验证目标）：
//   - 主键 int/int64 自增、时间 int64 Unix 秒、枚举 varchar + Go 常量；
//     无 partial index、无 generated column、无 JSON 列 → 三库 AutoMigrate 直接可用
//   - 联合唯一索引 uk_flow_billing(billing_no, flow_type)、
//     uk_stmt_period(distributor_id, period) 三库语义一致
//   - 金额列 bigint（int64）：SQLite 动态类型按整数存储，无精度问题

// commissionMigrationTables 返回 Phase 2 新增 7 表（与 model/main.go migrateDB
// 第 302-309 行注册一致，契约 §2.3/§3.1-3.3 冻结）。
func commissionMigrationTables() []any {
	return []any{
		&AttributionChange{},
		&CommissionFlow{},
		&CommissionRate{},
		&CommissionRateHistory{},
		&CommissionStatement{},
		&CommissionStatementItem{},
		&StatementAdjustment{},
	}
}

// commissionMigrationModels 配对 7 表结构与 TableName 冻结值（契约 §2.3/§3.1-3.3），
// 用于断言 AutoMigrate 后表真实存在且可查询。表存在性优先走结构体驱动的
// Migrator().HasTable（经 Tabler.TableName() 解析，跨方言可靠）；
// SELECT COUNT(*) 冒烟则直接验证字符串表名可执行。
var commissionMigrationModels = []struct {
	table string
	model any
}{
	{"attribution_changes", &AttributionChange{}},
	{"commission_flows", &CommissionFlow{}},
	{"commission_rates", &CommissionRate{}},
	{"commission_rate_histories", &CommissionRateHistory{}},
	{"commission_statements", &CommissionStatement{}},
	{"commission_statement_items", &CommissionStatementItem{}},
	{"commission_statement_adjustments", &StatementAdjustment{}},
}

// assertCommissionMigrationShared 断言三库通用的迁移结果：
//  1. AutoMigrate 7 表无 error；
//  2. 7 表存在且 SELECT COUNT(*) 冒烟可执行；
//  3. 契约 §6 联合唯一索引名三库一致（uk_flow_billing / uk_stmt_period）。
func assertCommissionMigrationShared(t *testing.T, db *gorm.DB) {
	t.Helper()

	// 1. AutoMigrate 无 error（对应 model/main.go migrateDB 的 7 表注册）
	require.NoError(t, db.AutoMigrate(commissionMigrationTables()...),
		"AutoMigrate of 7 commission tables should not error")

	// 2. 表存在 + SELECT COUNT(*) 冒烟（契约 §6：无 partial index/generated/JSON 列，
	//    纯 DDL 迁移后三库均可直接查询）
	for _, m := range commissionMigrationModels {
		require.True(t, db.Migrator().HasTable(m.model),
			"table %s should exist after AutoMigrate", m.table)
		var cnt int64
		require.NoError(t, db.Raw("SELECT COUNT(*) FROM "+m.table).Scan(&cnt).Error,
			"SELECT COUNT(*) FROM %s should not error", m.table)
	}

	// 3. 联合唯一索引名三库一致（契约 §6 冻结索引名）
	require.True(t, db.Migrator().HasIndex(&CommissionFlow{}, "uk_flow_billing"),
		"uk_flow_billing(billing_no, flow_type) should exist")
	require.True(t, db.Migrator().HasIndex(&CommissionStatement{}, "uk_stmt_period"),
		"uk_stmt_period(distributor_id, period) should exist")
}

// assertBigintRoundTrip 验证 bigint 金额列（契约 §6：金额一律 int64 cents）
// 经写入-读回不丢精度。取 2^53+1（超出 float64 精度范围）：若底层落成浮点
// 类型则末位丢失；SQLite 动态类型按整数存储、MySQL/PG 为 bigint，应原样返回。
func assertBigintRoundTrip(t *testing.T, db *gorm.DB) {
	t.Helper()
	billingNo := "MIGTEST-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	const largeCents int64 = 9007199254740993 // 2^53 + 1
	flow := &CommissionFlow{
		BillingNo:       billingNo,
		FlowType:        CommissionFlowCommission,
		CustomerId:      1,
		DistributorId:   1,
		TopupMoneyCents: largeCents,
		RateBp:          500,
		AmountCents:     largeCents,
		Status:          CommissionFlowPending,
		Period:          "2026-08",
	}
	require.NoError(t, db.Create(flow).Error, "insert flow with bigint amount should not error")
	t.Cleanup(func() {
		db.Where("billing_no = ?", billingNo).Delete(&CommissionFlow{})
	})

	var got CommissionFlow
	require.NoError(t, db.Where("billing_no = ?", billingNo).First(&got).Error)
	require.Equal(t, largeCents, got.AmountCents,
		"bigint amount_cents must round-trip without precision loss")
	require.Equal(t, largeCents, got.TopupMoneyCents,
		"bigint topup_money_cents must round-trip without precision loss")
}

// TestCommissionMigration_SQLite 默认执行：内存 SQLite 迁移 7 表。
// 注意：glebarez/sqlite 的 ":memory:" 每连接独立，必须 SetMaxOpenConns(1)
// 保证迁移与后续查询复用同一连接（与 task_cas_test.go TestMain 模式一致）；
// 本测试无嵌套事务，无 02-04 fix(5ae01ba7) 修复的单连接死锁风险。
func TestCommissionMigration_SQLite(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	assertCommissionMigrationShared(t, db)
	assertBigintRoundTrip(t, db)
}

// TestCommissionMigration_MySQL 由 TEST_MYSQL_DSN 门控：无 DSN 自动 skip；
// 有 DSN 时验证 AutoMigrate 7 表可执行 + 表/索引存在 + bigint 精度无损。
// DSN 示例（docker-compose.dev.yml 的 mysql 容器）：
//
//	root:password@tcp(localhost:3306)/newapi_test?charset=utf8mb4
func TestCommissionMigration_MySQL(t *testing.T) {
	dsn := os.Getenv("TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("TEST_MYSQL_DSN not set, skipping MySQL migration test")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	require.NoError(t, err)

	assertCommissionMigrationShared(t, db)
	assertBigintRoundTrip(t, db)
}

// TestCommissionMigration_PostgreSQL 由 TEST_POSTGRES_DSN 门控：无 DSN 自动 skip；
// 有 DSN 时验证 AutoMigrate 7 表可执行 + 表/索引存在 + bigint 精度无损。
// DSN 示例（docker-compose.dev.yml 的 postgres 容器）：
//
//	host=localhost user=postgres password=password dbname=newapi_test port=5432 sslmode=disable
func TestCommissionMigration_PostgreSQL(t *testing.T) {
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping PostgreSQL migration test")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	require.NoError(t, err)

	assertCommissionMigrationShared(t, db)
	assertBigintRoundTrip(t, db)
}
