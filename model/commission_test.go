package model

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件为佣金记账引擎 RecordCommissionTx（契约 01-CONTRACT.md §3.5）的单元测试与测试基建。
//
// 测试库纪律（Pitfall 1，02-04 死锁教训）：
//   - 每个测试函数/子测试自建独立内存库（DSN 带 _busy_timeout=30000 防 "database is locked"）
//   - 禁止 SetMaxOpenConns(1)：记账在调用方事务内嵌套查询，需要多连接，单连接必死锁
//   - model 包共享 TestMain（task_cas_test.go）MaxOpenConns=1 且 AutoMigrate 不含
//     commission_flows 等新表，故本文件不依赖共享 TestMain，一律使用 setupCommissionTestDB

// commissionTestSeq 测试数据序列号：保证 Username/AffCode/TradeNo 在同一测试库内唯一
// （users.username/aff_code 为 uniqueIndex，topups.trade_no unique）。
var commissionTestSeq atomic.Int64

// setupCommissionTestDB 初始化独立内存 SQLite 测试库并覆盖全局 DB/LOG_DB。
// 库随 t.Cleanup 关闭；同一测试内的所有读写均走该库。
func setupCommissionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	// _txlock=immediate：事务开始即取写锁（BEGIN IMMEDIATE）。deferred 模式下两个并发
	// 事务可同时持 shared-cache 读锁再升级写锁 → SQLITE_LOCKED(6) 死锁，busy_timeout
	// 不处理该类死锁；immediate 使并发事务在 BEGIN 处排队，串行化语义与生产行锁一致
	// （并发调整测试依赖此参数，04-03）。
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000&_txlock=immediate", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	// 注意：此处禁止 SetMaxOpenConns(1)——RecordCommissionTx 运行在调用方的
	// DB.Transaction 事务内，嵌套查询需要第二个连接，单连接会死锁（02-04 教训）
	origDB, origLogDB := DB, LOG_DB
	DB = db
	LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&User{}, &TopUp{}, &CommissionFlow{}, &CommissionRate{}, &CommissionRateHistory{}, &Log{},
		// Phase 4 出账引擎三表（Wave 0 补齐）：缺表时 statement 测试必 panic（04-RESEARCH Pitfall 7）
		&CommissionStatement{}, &CommissionStatementItem{}, &StatementAdjustment{},
		// Phase 5 提现单表（附录 E1）：缺表时提现测试必 panic（04-RESEARCH Pitfall 7 同源）
		&CommissionWithdrawal{},
	))
	t.Cleanup(func() {
		// 先恢复全局 DB 再关闭本测试库：后续测试（如 TestCommissionOptions_*）
		// 依赖共享 TestMain 的 DB，不得拿到已关闭的连接
		DB, LOG_DB = origDB, origLogDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// withCommissionEnabled 临时设置佣金总开关并在测试结束时恢复（t.Cleanup）。
func withCommissionEnabled(t *testing.T, enabled bool) {
	t.Helper()
	orig := common.CommissionEnabled
	common.CommissionEnabled = enabled
	t.Cleanup(func() { common.CommissionEnabled = orig })
}

// withCommissionPayoutDay 临时设置出账日并在测试结束时恢复（t.Cleanup，
// 照抄 withCommissionEnabled 模式；供出账/调度类测试使用，04-02/04-03 可复用）。
func withCommissionPayoutDay(t *testing.T, n int) {
	t.Helper()
	orig := common.CommissionPayoutDay
	common.CommissionPayoutDay = n
	t.Cleanup(func() { common.CommissionPayoutDay = orig })
}

// seedFlowTopUpOpts seedFlow 可选同步建 TopUp 行的字段（statement_items 明细快照来源，STMT-02）。
type seedFlowTopUpOpts struct {
	paymentMethod   string
	paymentProvider string
	completeTime    int64
}

// seedFlowOpts seedFlow 的构造选项。
type seedFlowOpts struct {
	distributorId   int
	customerId      int
	flowType        string // "" = commission
	tradeNo         string // "" = 人工流水（manual_credit/manual_debit，BillingNo 自动生成）
	topupMoneyCents int64
	rateBp          int
	amountCents     int64              // 正负均可（reversal/manual_debit 为负）
	status          string             // "" = pending
	period          string             // 必传："2026-08"（出账测试不依赖充值链路，直接铺账期）
	withTopUp       *seedFlowTopUpOpts // 可选：同步建 TopUp 行（明细快照 join 源）
}

// seedFlow 绕过记账入口直插 CommissionFlow 行（契约 §3.1 冻结字段）。
// 传 withTopUp（且 tradeNo 非空）时同步建一行 TopUp 供明细快照 join；
// 返回建好的流水（含 DB 回填的 Id/CreatedAt）。
func seedFlow(t *testing.T, opts seedFlowOpts) CommissionFlow {
	t.Helper()
	require.NotEmpty(t, opts.period, "period is required for seeded flows")
	seq := commissionTestSeq.Add(1)
	flowType := opts.flowType
	if flowType == "" {
		flowType = CommissionFlowCommission
	}
	status := opts.status
	if status == "" {
		status = CommissionFlowPending
	}
	billingNo := opts.tradeNo
	if billingNo == "" {
		// 人工流水无充值单：BillingNo=MAN-{ts}-{rand}（契约 §3.1.1），测试用 seq 保证唯一
		billingNo = fmt.Sprintf("MAN-TEST%d", seq)
	}
	if opts.withTopUp != nil && opts.tradeNo != "" {
		require.NoError(t, DB.Create(&TopUp{
			UserId:          opts.customerId,
			TradeNo:         opts.tradeNo,
			PaymentMethod:   opts.withTopUp.paymentMethod,
			PaymentProvider: opts.withTopUp.paymentProvider,
			CreateTime:      opts.withTopUp.completeTime,
			CompleteTime:    opts.withTopUp.completeTime,
			Status:          common.TopUpStatusSuccess,
		}).Error)
	}
	f := CommissionFlow{
		BillingNo:       billingNo,
		FlowType:        flowType,
		TradeNo:         opts.tradeNo,
		CustomerId:      opts.customerId,
		DistributorId:   opts.distributorId,
		TopupMoneyCents: opts.topupMoneyCents,
		RateBp:          opts.rateBp,
		AmountCents:     opts.amountCents,
		Status:          status,
		Period:          opts.period,
	}
	require.NoError(t, DB.Create(&f).Error)
	return f
}

// seedStatementOpts seedStatement 的构造选项（Phase 5 提现测试铺账单用，
// 平移 controller 包 seedStmtRow 语义到 model 包——提现测试不依赖出账引擎铺账单）。
type seedStatementOpts struct {
	distributorId        int
	period               string // "" = 按 seq 生成唯一期（uk_stmt_period 防撞）
	status               string // "" = StatementPayable
	totalCommissionCents int64
	adjustedCents        int64
	lockedAt             int64 // 0 = common.GetTimestamp()
}

// seedStatement 直插一张对账单（settle = total + adjusted 服务端算好落库）。
func seedStatement(t *testing.T, opts seedStatementOpts) *CommissionStatement {
	t.Helper()
	seq := commissionTestSeq.Add(1)
	period := opts.period
	if period == "" {
		// seq 驱动的唯一期号（同测试库内不撞 uk_stmt_period）
		period = fmt.Sprintf("%04d-%02d", 2000+seq/12, seq%12+1)
	}
	status := opts.status
	if status == "" {
		status = StatementPayable
	}
	lockedAt := opts.lockedAt
	if lockedAt == 0 {
		lockedAt = common.GetTimestamp()
	}
	s := &CommissionStatement{
		DistributorId:        opts.distributorId,
		Period:               period,
		TotalTopupCents:      (opts.totalCommissionCents - opts.adjustedCents) * 10,
		TotalCommissionCents: opts.totalCommissionCents,
		AdjustedCents:        opts.adjustedCents,
		SettleAmountCents:    opts.totalCommissionCents + opts.adjustedCents,
		Status:               status,
		LockedAt:             lockedAt,
	}
	require.NoError(t, DB.Create(s).Error)
	return s
}

// seedWithdrawalOpts seedWithdrawal 的构造选项（铺五态任意前置态供迁移测试）。
type seedWithdrawalOpts struct {
	statementId   int64
	distributorId int
	status        string // "" = WithdrawalPending
	reason        string
	voucherNo     string
	withdrawalNo  string // "" = WD-TEST-{seq}（uk_wd_no 防撞）
	operatorId    int
	reviewedAt    int64
	approvedAt    int64
	paidAt        int64
	rejectedAt    int64
}

// seedWithdrawal 直插一张提现单行（绕过申请事务，任意前置态）。
func seedWithdrawal(t *testing.T, opts seedWithdrawalOpts) *CommissionWithdrawal {
	t.Helper()
	seq := commissionTestSeq.Add(1)
	status := opts.status
	if status == "" {
		status = WithdrawalPending
	}
	withdrawalNo := opts.withdrawalNo
	if withdrawalNo == "" {
		withdrawalNo = fmt.Sprintf("WD-TEST-%d", seq)
	}
	wd := &CommissionWithdrawal{
		WithdrawalNo:  withdrawalNo,
		StatementId:   opts.statementId,
		DistributorId: opts.distributorId,
		Status:        status,
		Reason:        opts.reason,
		VoucherNo:     opts.voucherNo,
		OperatorId:    opts.operatorId,
		ReviewedAt:    opts.reviewedAt,
		ApprovedAt:    opts.approvedAt,
		PaidAt:        opts.paidAt,
		RejectedAt:    opts.rejectedAt,
	}
	require.NoError(t, DB.Create(wd).Error)
	return wd
}

// commissionFixture 一键分销链路种子：分销商 + 客户（inviter_id 归属）+ 比例行 + 充值单。
type commissionFixture struct {
	Distributor *User
	Customer    *User
	TopUp       *TopUp
}

// seedCommissionOpts seedCommissionFixture 的构造选项。
type seedCommissionOpts struct {
	distributorRole int     // 归属者角色；0 = RoleDistributorUser（默认分销商）
	rateBp          int     // 比例（万分比）；0 = 不建 commission_rates 行
	paymentProvider string  // TopUp.PaymentProvider
	topupStatus     string  // TopUp.Status；"" = pending
	money           float64 // TopUp.Money（实付金额，元）
	amount          int64   // TopUp.Amount（额度数）
}

// seedCommissionFixture 按选项构造完整分销链路并落库。
func seedCommissionFixture(t *testing.T, opts seedCommissionOpts) *commissionFixture {
	t.Helper()
	seq := commissionTestSeq.Add(1)

	distributorRole := opts.distributorRole
	if distributorRole == 0 {
		distributorRole = common.RoleDistributorUser
	}
	f := &commissionFixture{
		Distributor: &User{
			Username: fmt.Sprintf("dist%d", seq),
			Password: "testpass1234",
			Role:     distributorRole,
			Status:   common.UserStatusEnabled,
			AffCode:  fmt.Sprintf("DA%d", seq),
		},
		Customer: &User{
			Username: fmt.Sprintf("cust%d", seq),
			Password: "testpass1234",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
			AffCode:  fmt.Sprintf("CA%d", seq),
		},
	}
	// 客户归属：inviter_id = 分销商 Id（分销商 Id 须先生成）
	require.NoError(t, DB.Create(f.Distributor).Error)
	f.Customer.InviterId = f.Distributor.Id
	require.NoError(t, DB.Create(f.Customer).Error)

	if opts.rateBp > 0 {
		require.NoError(t, DB.Create(&CommissionRate{
			DistributorId: f.Distributor.Id,
			RateBp:        opts.rateBp,
		}).Error)
	}

	status := opts.topupStatus
	if status == "" {
		status = common.TopUpStatusPending
	}
	f.TopUp = &TopUp{
		UserId:          f.Customer.Id,
		Amount:          opts.amount,
		Money:           opts.money,
		TradeNo:         fmt.Sprintf("T%d%d", time.Now().UnixNano(), seq),
		PaymentProvider: opts.paymentProvider,
		PaymentMethod:   opts.paymentProvider,
		CreateTime:      common.GetTimestamp(),
		Status:          status,
	}
	require.NoError(t, DB.Create(f.TopUp).Error)
	return f
}

// TestCommissionGate 门控矩阵（契约 §3.4 + Pitfall 3）：
// CommissionEnabled=false 时记账引擎直调与 ManualCompleteTopUp 全链路均零流水，
// 且充值本身必须成功——门控不得把「无需记账」放大为「充值失败」。
func TestCommissionGate(t *testing.T) {
	setupCommissionTestDB(t)
	withCommissionEnabled(t, false)

	f := seedCommissionFixture(t, seedCommissionOpts{
		paymentProvider: PaymentProviderEpay,
		money:           100.00,
		amount:          1000,
		rateBp:          1000, // 即使比例已配置，开关关也必须零流水
	})

	// 直调：返回 nil 且零流水
	require.NoError(t, RecordCommissionTx(DB, f.TopUp))
	var count int64
	require.NoError(t, DB.Model(&CommissionFlow{}).Count(&count).Error)
	require.Zero(t, count, "gate off: direct call must produce zero flows")

	// 全链路：ManualCompleteTopUp 充值成功且零流水
	require.NoError(t, ManualCompleteTopUp(f.TopUp.TradeNo, "127.0.0.1"))
	var topup TopUp
	require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&topup).Error)
	require.Equal(t, common.TopUpStatusSuccess, topup.Status, "gate off: topup itself must succeed (Pitfall 3)")
	require.NoError(t, DB.Model(&CommissionFlow{}).Count(&count).Error)
	require.Zero(t, count, "gate off: full chain must produce zero flows")

	// 开关打开后重放同一单：幂等门（Status==success 静默 return）在挂载之前，仍零流水
	withCommissionEnabled(t, true)
	require.NoError(t, ManualCompleteTopUp(f.TopUp.TradeNo, "127.0.0.1"))
	require.NoError(t, DB.Model(&CommissionFlow{}).Count(&count).Error)
	require.Zero(t, count, "replay after success must stay unreachable to commission")
}

// TestRecordCommissionTx 分支矩阵（契约 §3.5 步骤 1-4；开关开，table-driven）。
// 门控分支（客户不存在/无归属/非分销商/无比例配置）一律 return nil 且零流水；
// happy path 断言全部快照字段；金额与账期断言精度与本地时区语义。
func TestRecordCommissionTx(t *testing.T) {
	withCommissionEnabled(t, true)

	cases := []struct {
		name            string
		opts            seedCommissionOpts
		mutate          func(t *testing.T, f *commissionFixture)
		wantFlow        bool
		wantMoneyCents  int64
		wantRateBp      int
		wantAmountCents int64
		wantPeriod      string
	}{
		{
			name: "customer_not_found",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
			mutate: func(t *testing.T, f *commissionFixture) {
				f.TopUp.UserId = 99999999 // 库中不存在的客户 → 不 panic、零流水
			},
		},
		{
			name: "no_attribution",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
			mutate: func(t *testing.T, f *commissionFixture) {
				require.NoError(t, DB.Model(f.Customer).Update("inviter_id", 0).Error)
			},
		},
		{
			// 普通邀请（role=1）→ 零流水（ATTR-04，ROADMAP 验收标准 6）
			name: "inviter_role_1_common",
			opts: seedCommissionOpts{distributorRole: common.RoleCommonUser, paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
		},
		{
			// admin=10 精确排除（禁阈值式 >=5）
			name: "inviter_role_10_admin",
			opts: seedCommissionOpts{distributorRole: common.RoleAdminUser, paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
		},
		{
			// root=100 精确排除
			name: "inviter_role_100_root",
			opts: seedCommissionOpts{distributorRole: common.RoleRootUser, paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
		},
		{
			// 无比例配置 = 未启用（契约 §7#3 冻结）→ 零流水，不是 0 金额流水
			name: "no_rate_row",
			opts: seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 0},
		},
		{
			name:            "happy_path_full_snapshot",
			opts:            seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 100.00, amount: 1000, rateBp: 1000},
			wantFlow:        true,
			wantMoneyCents:  10000,
			wantRateBp:      1000,
			wantAmountCents: 1000,
			wantPeriod:      "2026-08",
			mutate: func(t *testing.T, f *commissionFixture) {
				f.TopUp.CompleteTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local).Unix()
			},
		},
		{
			// 整除向下取整：1 分 × 333bp / 10000 = 0 分，尾差留平台侧（契约 §3.0）
			name:            "floor_division_remainder_to_platform",
			opts:            seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 0.01, amount: 1, rateBp: 333},
			wantFlow:        true,
			wantMoneyCents:  1,
			wantRateBp:      333,
			wantAmountCents: 0,
			wantPeriod:      "2026-08",
			mutate: func(t *testing.T, f *commissionFixture) {
				f.TopUp.CompleteTime = time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local).Unix()
			},
		},
		{
			// 月末 23:59:59（本地时区）→ 归当月（契约 C3）
			name:            "period_month_end_local_tz",
			opts:            seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 50.00, amount: 500, rateBp: 1000},
			wantFlow:        true,
			wantMoneyCents:  5000,
			wantRateBp:      1000,
			wantAmountCents: 500,
			wantPeriod:      "2026-08",
			mutate: func(t *testing.T, f *commissionFixture) {
				f.TopUp.CompleteTime = time.Date(2026, 8, 31, 23, 59, 59, 0, time.Local).Unix()
			},
		},
		{
			// 次月 00:00:00（本地时区）→ 归次月
			name:            "period_next_month_first_second",
			opts:            seedCommissionOpts{paymentProvider: PaymentProviderEpay, money: 50.00, amount: 500, rateBp: 1000},
			wantFlow:        true,
			wantMoneyCents:  5000,
			wantRateBp:      1000,
			wantAmountCents: 500,
			wantPeriod:      "2026-09",
			mutate: func(t *testing.T, f *commissionFixture) {
				f.TopUp.CompleteTime = time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local).Unix()
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			setupCommissionTestDB(t)

			f := seedCommissionFixture(t, tc.opts)
			if tc.mutate != nil {
				tc.mutate(t, f)
			}

			err := RecordCommissionTx(DB, f.TopUp)
			require.NoError(t, err, "all gate branches must return nil (Pitfall 3)")

			var flows []CommissionFlow
			require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).Find(&flows).Error)
			if !tc.wantFlow {
				require.Empty(t, flows, "gate branch must produce zero flows")
				return
			}
			require.Len(t, flows, 1, "exactly one flow per topup")
			flow := flows[0]
			require.Equal(t, f.TopUp.TradeNo, flow.BillingNo, "billing_no = trade_no（幂等键）")
			require.Equal(t, CommissionFlowCommission, flow.FlowType)
			require.Equal(t, f.TopUp.TradeNo, flow.TradeNo)
			require.Equal(t, f.Customer.Id, flow.CustomerId)
			require.Equal(t, f.Distributor.Id, flow.DistributorId)
			require.Equal(t, tc.wantMoneyCents, flow.TopupMoneyCents, "topup_money_cents 快照（分）")
			require.Equal(t, tc.wantRateBp, flow.RateBp, "rate_bp 快照（万分比）")
			require.Equal(t, tc.wantAmountCents, flow.AmountCents, "amount_cents（int64 整除向下取整）")
			require.Equal(t, CommissionFlowPending, flow.Status)
			require.Equal(t, tc.wantPeriod, flow.Period, "period = CompleteTime 所在月（本地时区）")
			require.Zero(t, flow.OperatorId, "系统流水 operator_id=0")
			require.Zero(t, flow.RelatedFlowId, "正向流水 related_flow_id=0")
		})
	}
}

// TestRecordCommissionTx_SnapshotImmunity 快照不变性预埋（RATE-02）：
// 流水生成后修改 CommissionRate.RateBp，旧流水快照不变。
func TestRecordCommissionTx_SnapshotImmunity(t *testing.T) {
	setupCommissionTestDB(t)
	withCommissionEnabled(t, true)

	f := seedCommissionFixture(t, seedCommissionOpts{
		paymentProvider: PaymentProviderEpay,
		money:           100.00,
		amount:          1000,
		rateBp:          1000,
	})
	require.NoError(t, RecordCommissionTx(DB, f.TopUp))

	require.NoError(t, DB.Model(&CommissionRate{}).Where("distributor_id = ?", f.Distributor.Id).Update("rate_bp", 2000).Error)

	var flow CommissionFlow
	require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&flow).Error)
	require.Equal(t, 1000, flow.RateBp, "recorded snapshot must not change with rate updates (RATE-02)")
}
