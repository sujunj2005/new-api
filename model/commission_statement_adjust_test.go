package model

import (
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 本文件为超管调账面（契约 01-CONTRACT.md §4.3 A7/A8 + 附录 D1/D5.1/D5.4，
// COMM-05/06/07）的单元测试与集成测试：
//   - TestCreateManualFlow A7 人工补录/冲销（校验链 + D1 缺省字段 + 无总开关门控）
//   - TestStatementAdjustmentTx A8 调整单（payable 白名单 + 同事务累计 + 原始账单不变 + 并发串行）
//   - TestNextPeriodSweep D1 下期纳入联测（settled 差错 → A7 补录 → 下期出账自动纳入）
//   - TestVoidTopUpLockDiscipline D5.4 锁纪律（分销商行锁 + 与出账同序 + 03-03 语义回归）
//
// 纪律（04-RESEARCH Pitfall 7）：每测试自建独立内存库（setupCommissionTestDB）。

// manualBillingNoPattern 幂等键格式：MAN-{14位时间戳}-{4位随机字母数字}（§3.1.1）。
var manualBillingNoPattern = regexp.MustCompile(`^MAN-\d{14}-[A-Za-z0-9]{4}$`)

// seedAdjustStatement 直插一张对账单（A8 测试铺底；Total/LockedAt 按 §3.3 形状生成即定）。
func seedAdjustStatement(t *testing.T, distributorId int, period, status string, totalCommission, adjusted int64) *CommissionStatement {
	t.Helper()
	s := &CommissionStatement{
		DistributorId:        distributorId,
		Period:               period,
		TotalTopupCents:      totalCommission * 10,
		TotalCommissionCents: totalCommission,
		AdjustedCents:        adjusted,
		SettleAmountCents:    totalCommission + adjusted,
		Status:               status,
		LockedAt:             common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(s).Error)
	return s
}

// seedAdjustItem 直插一条账单明细（不变性断言用）。
func seedAdjustItem(t *testing.T, statementId int64, billingNo string, commissionCents int64) CommissionStatementItem {
	t.Helper()
	it := CommissionStatementItem{
		StatementId:     statementId,
		FlowId:          0,
		BillingNo:       billingNo,
		PaymentMethod:   "epay",
		PaymentProvider: "epay",
		TopupMoneyCents: commissionCents * 10,
		CommissionCents: commissionCents,
		CustomerId:      0,
		CompleteTime:    common.GetTimestamp(),
	}
	require.NoError(t, DB.Create(&it).Error)
	return it
}

// TestCreateManualFlow A7 校验链与 D1 缺省字段矩阵（契约 §4.3 A7 + §3.1.1 + 附录 D1/D5.1）。
// 校验链 fail-fast 顺序平移 A2 先例：flow_type 白名单 → 金额恒正 → reason 必填/列宽 →
// 目标存在且角色精确 == 分销商（禁阈值式——admin/root 不能被补录）。
func TestCreateManualFlow(t *testing.T) {
	type testCase struct {
		name            string
		role            int
		distMissing     bool
		flowType        string
		amountCents     int64
		reason          string
		gateOff         bool // 分佣总开关关闭时 A7 仍须成功（附录 D5.1）
		wantErrContains string
		wantAmount      int64 // 成功分支落库断言（credit 为正、debit 落库取负）
	}

	cases := []testCase{
		{
			name:            "flow_type_commission_rejected",
			flowType:        CommissionFlowCommission,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "流水类型",
		},
		{
			name:            "flow_type_reversal_rejected",
			flowType:        CommissionFlowReversal,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "流水类型",
		},
		{
			name:            "flow_type_arbitrary_rejected",
			flowType:        "credit",
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "流水类型",
		},
		{
			name:            "amount_zero_rejected",
			flowType:        CommissionFlowManualCredit,
			amountCents:     0,
			reason:          "差错补录",
			wantErrContains: "金额",
		},
		{
			name:            "amount_negative_rejected",
			flowType:        CommissionFlowManualCredit,
			amountCents:     -100,
			reason:          "差错补录",
			wantErrContains: "金额",
		},
		{
			name:            "reason_empty_rejected",
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "",
			wantErrContains: "原因",
		},
		{
			name:            "reason_blank_rejected",
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "   ",
			wantErrContains: "原因",
		},
		{
			name:            "reason_over_255_rejected",
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          strings.Repeat("x", 256),
			wantErrContains: "原因",
		},
		{
			name:            "distributor_not_found_rejected",
			distMissing:     true,
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "分销商",
		},
		{
			// role=1 精确排除（禁阈值式 >=5）
			name:            "target_role_1_rejected",
			role:            common.RoleCommonUser,
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "分销商",
		},
		{
			// admin=10 精确排除
			name:            "target_role_10_rejected",
			role:            common.RoleAdminUser,
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "分销商",
		},
		{
			// root=100 精确排除
			name:            "target_role_100_rejected",
			role:            common.RoleRootUser,
			flowType:        CommissionFlowManualCredit,
			amountCents:     100,
			reason:          "差错补录",
			wantErrContains: "分销商",
		},
		{
			name:        "credit_ok_full_defaults",
			role:        common.RoleDistributorUser,
			flowType:    CommissionFlowManualCredit,
			amountCents: 100,
			reason:      "差错补录",
			wantAmount:  100,
		},
		{
			// 请求侧恒正，落库取负（契约 A7：manual_debit 落库为负数）
			name:        "debit_ok_stored_negative",
			role:        common.RoleDistributorUser,
			flowType:    CommissionFlowManualDebit,
			amountCents: 100,
			reason:      "多记冲销",
			wantAmount:  -100,
		},
		{
			// 附录 D5.1：调账入口不受分佣总开关门控（零门控分支）
			name:        "gate_off_still_succeeds",
			role:        common.RoleDistributorUser,
			flowType:    CommissionFlowManualCredit,
			amountCents: 100,
			reason:      "总开关关闭时的纠错补录",
			gateOff:     true,
			wantAmount:  100,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			setupCommissionTestDB(t)
			if tc.gateOff {
				withCommissionEnabled(t, false)
			}

			var distId int
			if tc.distMissing {
				distId = 99999999
			} else {
				distId = seedStatementUser(t, tc.role).Id
			}

			err := CreateManualFlow(distId, tc.flowType, tc.amountCents, tc.reason, 7)
			if tc.wantErrContains != "" {
				require.ErrorContains(t, err, tc.wantErrContains)
				var cnt int64
				require.NoError(t, DB.Model(&CommissionFlow{}).Count(&cnt).Error)
				require.Zero(t, cnt, "rejected branch must produce zero flows")
				return
			}
			require.NoError(t, err)

			var flow CommissionFlow
			require.NoError(t, DB.Where("distributor_id = ?", distId).First(&flow).Error)
			require.Equal(t, tc.flowType, flow.FlowType)
			require.Equal(t, tc.wantAmount, flow.AmountCents, "credit positive / debit stored negative")
			require.Equal(t, CommissionFlowPending, flow.Status, "pending until next sweep")
			require.Equal(t, distId, flow.DistributorId)
			// D1 缺省字段全套（附录 D1：manual 流水冻结缺省）
			require.Regexp(t, manualBillingNoPattern, flow.BillingNo, "MAN-{ts}-{rand} 幂等键")
			require.Empty(t, flow.TradeNo, "manual flow has no topup")
			require.Zero(t, flow.CustomerId)
			require.Zero(t, flow.TopupMoneyCents)
			require.Zero(t, flow.RateBp)
			require.Equal(t, time.Now().Format("2006-01"), flow.Period, "period = current month → swept into next statement")
			require.Equal(t, tc.reason, flow.Reason, "reason persisted for audit")
			require.Equal(t, 7, flow.OperatorId, "operator persisted for audit")
			require.Zero(t, flow.RelatedFlowId)
			require.Zero(t, flow.StatementId)
		})
	}
}

// TestStatementAdjustmentTx A8 调整单事务矩阵（契约 §4.3 A8 + §3.3 不可变规则 + Pitfall 6）。
// 白名单 status==payable（withdrawing/settled 一并拒绝，禁阈值式）；
// 同事务累计 adjusted_cents 并重算 settle（服务端计算）；原始账单 Total/items 零触碰。
func TestStatementAdjustmentTx(t *testing.T) {
	t.Run("payable_single_delta", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementPayable, 10000, 0)
		it1 := seedAdjustItem(t, stmt.Id, "TADJ-1", 6000)
		it2 := seedAdjustItem(t, stmt.Id, "TADJ-2", 4000)

		require.NoError(t, CreateStatementAdjustmentTx(stmt.Id, -50, "多记冲正", 7))

		var got CommissionStatement
		require.NoError(t, DB.First(&got, stmt.Id).Error)
		require.EqualValues(t, -50, got.AdjustedCents)
		require.EqualValues(t, 9950, got.SettleAmountCents, "settle = total + adjusted (server computed)")
		// 原始账单不变（COMM-06：Total/Period/Status/LockedAt 全部零触碰）
		require.EqualValues(t, 10000, got.TotalCommissionCents)
		require.EqualValues(t, 100000, got.TotalTopupCents)
		require.Equal(t, "2026-08", got.Period)
		require.Equal(t, StatementPayable, got.Status)
		require.Equal(t, stmt.LockedAt, got.LockedAt)
		// items 行数与内容不变
		items := statementItemsOf(t, stmt.Id)
		require.Len(t, items, 2)
		require.Equal(t, []CommissionStatementItem{it1, it2}, items)
		// 调整单行完整落库
		var adjs []StatementAdjustment
		require.NoError(t, DB.Where("statement_id = ?", stmt.Id).Find(&adjs).Error)
		require.Len(t, adjs, 1)
		require.EqualValues(t, -50, adjs[0].DeltaCents)
		require.Equal(t, "多记冲正", adjs[0].Reason)
		require.Equal(t, 7, adjs[0].OperatorId)
	})

	t.Run("two_deltas_accumulate", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementPayable, 10000, 0)

		require.NoError(t, CreateStatementAdjustmentTx(stmt.Id, 200, "漏记补正", 7))
		require.NoError(t, CreateStatementAdjustmentTx(stmt.Id, -50, "再冲正", 7))

		var got CommissionStatement
		require.NoError(t, DB.First(&got, stmt.Id).Error)
		require.EqualValues(t, 150, got.AdjustedCents, "200 + (-50) accumulated")
		require.EqualValues(t, 10150, got.SettleAmountCents)
		var cnt int64
		require.NoError(t, DB.Model(&StatementAdjustment{}).Where("statement_id = ?", stmt.Id).Count(&cnt).Error)
		require.EqualValues(t, 2, cnt)
	})

	t.Run("withdrawing_rejected", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementWithdrawing, 10000, 0)

		err := CreateStatementAdjustmentTx(stmt.Id, -50, "提现中不得调整", 7)
		require.ErrorContains(t, err, "提现中")
		assertNoAdjustment(t, stmt.Id)
	})

	t.Run("settled_rejected", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementSettled, 10000, 0)

		err := CreateStatementAdjustmentTx(stmt.Id, -50, "已结清不得调整", 7)
		require.ErrorContains(t, err, "已结清")
		assertNoAdjustment(t, stmt.Id)
	})

	t.Run("statement_not_found_rejected", func(t *testing.T) {
		setupCommissionTestDB(t)

		err := CreateStatementAdjustmentTx(99999999, -50, "不存在", 7)
		require.Error(t, err)
	})

	t.Run("delta_zero_rejected", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementPayable, 10000, 0)

		err := CreateStatementAdjustmentTx(stmt.Id, 0, "无意义调整", 7)
		require.ErrorContains(t, err, "差额")
		assertNoAdjustment(t, stmt.Id)
	})

	t.Run("concurrent_two_adjustments_serialized", func(t *testing.T) {
		setupCommissionTestDB(t)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementPayable, 10000, 0)

		// 2 goroutine 并发各 +100：statement 行 FOR UPDATE 串行化，先读后算累计，无丢失更新。
		// SQLite shared-cache 驱动在真并发写下偶发表锁竞态（SQLITE_LOCKED 262，不受
		// busy_timeout 管理，MySQL/PG 无此驱动层问题）：写失败即事务回滚零写入，重试安全自愈。
		callAdjust := func() error {
			var lastErr error
			for attempt := 0; attempt < 5; attempt++ {
				lastErr = CreateStatementAdjustmentTx(stmt.Id, 100, "并发调整", 7)
				if lastErr == nil || !strings.Contains(lastErr.Error(), "table is locked") {
					return lastErr
				}
				time.Sleep(20 * time.Millisecond)
			}
			return lastErr
		}
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- callAdjust()
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}

		var got CommissionStatement
		require.NoError(t, DB.First(&got, stmt.Id).Error)
		require.EqualValues(t, 200, got.AdjustedCents, "no lost update under row lock serialization")
		require.EqualValues(t, 10200, got.SettleAmountCents)
		var cnt int64
		require.NoError(t, DB.Model(&StatementAdjustment{}).Where("statement_id = ?", stmt.Id).Count(&cnt).Error)
		require.EqualValues(t, 2, cnt, "exactly two adjustment rows")
	})

	t.Run("gate_off_still_succeeds", func(t *testing.T) {
		setupCommissionTestDB(t)
		withCommissionEnabled(t, false)

		dist := seedStatementUser(t, common.RoleDistributorUser)
		stmt := seedAdjustStatement(t, dist.Id, "2026-08", StatementPayable, 10000, 0)

		require.NoError(t, CreateStatementAdjustmentTx(stmt.Id, -50, "总开关关闭时的调整", 7))
		var got CommissionStatement
		require.NoError(t, DB.First(&got, stmt.Id).Error)
		require.EqualValues(t, -50, got.AdjustedCents, "附录 D5.1：调整入口不受总开关门控")
	})
}

// assertNoAdjustment 断言账单未产生调整单且累计值未被改动。
func assertNoAdjustment(t *testing.T, statementId int64) {
	t.Helper()
	var adjCnt int64
	require.NoError(t, DB.Model(&StatementAdjustment{}).Where("statement_id = ?", statementId).Count(&adjCnt).Error)
	require.Zero(t, adjCnt, "rejected branch must not create adjustment row")
	var stmt CommissionStatement
	require.NoError(t, DB.First(&stmt, statementId).Error)
	require.Zero(t, stmt.AdjustedCents, "rejected branch must not touch adjusted_cents")
}

// TestNextPeriodSweep D1「下期账单纳入上期调整分录」全机制联测（附录 D1 + ROADMAP 验收 7，
// 消费 04-01 GenerateDueStatements 参数化接缝）：
//  1. settled 账单差错 → A7 补录（period=当前月）→ 下期出账 sweep 自动纳入下期账单
//     （items 恰含 manual 行、PaymentMethod="manual" D5.2、流水翻 available 回填）
//  2. payable 调整单不投影下期（settle 已含 delta，再投影即双重计算）
//  3. reversal 跨月隔离回归：settled 账单对应充值单作废拒绝（ErrAlreadyStatemented，
//     历史不涂改，03-03 语义），差错唯一出口 = A7 下期体现
func TestNextPeriodSweep(t *testing.T) {
	setupCommissionTestDB(t)

	dist1 := seedStatementUser(t, common.RoleDistributorUser) // settled 差错 → A7 路径
	dist2 := seedStatementUser(t, common.RoleDistributorUser) // payable 调整单不投影
	cust := seedStatementUser(t, common.RoleCommonUser)

	// 铺底：dist1 上期账单已结清；dist2 上期账单应付
	seedAdjustStatement(t, dist1.Id, "2026-08", StatementSettled, 1000, 0)
	s2 := seedAdjustStatement(t, dist2.Id, "2026-08", StatementPayable, 2000, 0)
	seedAdjustItem(t, s2.Id, "TSWEEP-2", 2000)

	// ① settled 差错路径：A7 补录 → period=当前月、status=pending
	require.NoError(t, CreateManualFlow(dist1.Id, CommissionFlowManualCredit, 500, "已结清账单差错补录", 7))
	var manualFlow CommissionFlow
	require.NoError(t, DB.Where("distributor_id = ? AND flow_type = ?", dist1.Id, CommissionFlowManualCredit).First(&manualFlow).Error)
	require.Equal(t, CommissionFlowPending, manualFlow.Status)
	require.Equal(t, time.Now().Format("2006-01"), manualFlow.Period, "补录账期 = 记录时刻当前月（D1）")

	// ② payable 账单调整单（同事务累计，按调整后金额结算）
	require.NoError(t, CreateStatementAdjustmentTx(s2.Id, -50, "差额补正", 7))

	// ③ 下期出账：参数化 now = 下月任意日 → 出账期 = 上自然月 ≥ 补录账期 → sweep 纳入
	next := time.Now().AddDate(0, 1, 0)
	n, err := GenerateDueStatements(next)
	require.NoError(t, err)
	require.Equal(t, 1, n, "仅 dist1（有新 pending 流水）出下期账单")

	// 下期账单恰含 manual 流水一行（验收 7 活体证明）
	// （dist1 名下共 2 张：铺底 settled + 新出账单；按期标签取新账单，First 主键序会取到铺底单）
	var dist1StmtCnt int64
	require.NoError(t, DB.Model(&CommissionStatement{}).Where("distributor_id = ?", dist1.Id).Count(&dist1StmtCnt).Error)
	require.EqualValues(t, 2, dist1StmtCnt, "settled 铺底 + 新出下期账单")
	var nextStmt CommissionStatement
	require.NoError(t, DB.Where("distributor_id = ? AND period = ?", dist1.Id, manualFlow.Period).First(&nextStmt).Error)
	require.Equal(t, manualFlow.Period, nextStmt.Period, "下期账单期标签 = 补录账期")
	items := statementItemsOf(t, nextStmt.Id)
	require.Len(t, items, 1, "下期账单仅由新 pending 流水构成")
	it := items[0]
	require.Equal(t, manualFlow.Id, it.FlowId)
	require.EqualValues(t, 500, it.CommissionCents)
	require.Equal(t, "manual", it.PaymentMethod, "manual 明细缺省（D5.2）")
	require.Equal(t, manualFlow.CreatedAt, it.CompleteTime, "manual 到账时间 = 记账时间（D5.2）")
	require.EqualValues(t, 500, nextStmt.TotalCommissionCents)
	require.EqualValues(t, 500, nextStmt.SettleAmountCents)

	// 流水终态：翻 available + statement_id 回填
	var gotFlow CommissionFlow
	require.NoError(t, DB.First(&gotFlow, manualFlow.Id).Error)
	require.Equal(t, CommissionFlowAvailable, gotFlow.Status)
	require.Equal(t, nextStmt.Id, gotFlow.StatementId)

	// payable 调整单不投影：dist2 无新账单、零流水（调整单不产生流水，无双重计算）
	var dist2StmtCnt, dist2FlowCnt int64
	require.NoError(t, DB.Model(&CommissionStatement{}).Where("distributor_id = ?", dist2.Id).Count(&dist2StmtCnt).Error)
	require.EqualValues(t, 1, dist2StmtCnt, "dist2 仍只有原 payable 账单")
	require.NoError(t, DB.Model(&CommissionFlow{}).Where("distributor_id = ?", dist2.Id).Count(&dist2FlowCnt).Error)
	require.Zero(t, dist2FlowCnt, "调整单零流水投影")

	// reversal 跨月隔离回归：settled 账单对应充值单作废拒绝（03-03 语义回归）
	fSettled := seedFlow(t, seedFlowOpts{
		distributorId: dist1.Id, customerId: cust.Id, tradeNo: "TSWEEP-SETTLED",
		topupMoneyCents: 1000, rateBp: 1000, amountCents: 100, period: "2026-08",
		status: CommissionFlowAvailable, // 已出账（settled 账单内的流水终态）
		withTopUp: &seedFlowTopUpOpts{paymentMethod: "epay", paymentProvider: "epay", completeTime: 1755000000},
	})
	err = VoidTopUp(fSettled.TradeNo, "已出账作废尝试", 7)
	require.ErrorIs(t, err, ErrAlreadyStatemented, "已出账作废必须拒绝（历史不涂改）")
	var tu TopUp
	require.NoError(t, DB.Where("trade_no = ?", fSettled.TradeNo).First(&tu).Error)
	require.Equal(t, common.TopUpStatusSuccess, tu.Status, "作废整体回滚：status 未变")
	var revCnt int64
	require.NoError(t, DB.Model(&CommissionFlow{}).Where("trade_no = ? AND flow_type = ?", fSettled.TradeNo, CommissionFlowReversal).Count(&revCnt).Error)
	require.Zero(t, revCnt, "作废回滚零冲销流水")
}

// TestVoidTopUpLockDiscipline D5.4 锁纪律统一验证：
//  1. 源码结构断言：作废事务闭包内含分销商 users 行锁、位于 topup 行锁之前（与出账
//     事务 generateStatementTx 同序，消除交叉死锁面），且锁目标取自原流水 DistributorId
//     （checker m-3：void-改绑并发下当前 inviter_id 不可信）
//  2. 无佣金链路充值单：跳过分销商锁，作废语义原样
//  3. 有佣金链路充值单：补锁后冲销语义原样（正负流水同账期）
func TestVoidTopUpLockDiscipline(t *testing.T) {
	t.Run("source_lock_order", func(t *testing.T) {
		src, err := os.ReadFile("commission_reversal.go")
		require.NoError(t, err)
		s := string(src)

		// 唯一锚定补锁代码处的原流水定位查询（ReverseCommissionTx 内有相似查询，勿混用）
		flowLookupIdx := strings.Index(s, `Order("id DESC").First(&original).Error`)
		require.Positive(t, flowLookupIdx, "原佣金流水定位查询缺失（m-3 锁目标依据）")
		distLockIdx := strings.Index(s, `Select("id").First(&lockUser, "id = ?", lockDistId)`)
		require.Positive(t, distLockIdx, "分销商 users 行锁缺失（D5.4）")
		topUpLockIdx := strings.Index(s, `Set("gorm:query_option", "FOR UPDATE").Where(refCol+" = ?", tradeNo).First(topUp)`)
		require.Positive(t, topUpLockIdx, "topup 行锁缺失（03-03 既有）")

		require.Less(t, flowLookupIdx, distLockIdx, "先无锁读原流水取 DistributorId，再锁该分销商行（m-3 顺序）")
		require.Less(t, distLockIdx, topUpLockIdx, "锁顺序：分销商 users 行（第一把）→ topup 行，与出账事务同序")
		// 业务写动作（refunded 置位）在两把锁之后
		writeIdx := strings.Index(s, "TopUpStatusRefunded")
		require.Positive(t, writeIdx)
		require.Less(t, topUpLockIdx, writeIdx, "锁在业务写动作之前（D5.4）")
		require.NotContains(t, s, "inviter_id", "锁目标禁止使用当前 inviter_id（void-改绑并发下会被改写，m-3）")
	})

	t.Run("no_commission_link_void_succeeds", func(t *testing.T) {
		setupCommissionTestDB(t)
		// 无原佣金流水的成功充值单（开关期充值）：跳过分销商锁直接走原流程
		topUp := &TopUp{
			UserId:          424242,
			Amount:          500,
			Money:           50.00,
			TradeNo:         "TLOCK-NOLINK",
			PaymentProvider: PaymentProviderEpay,
			PaymentMethod:   PaymentProviderEpay,
			CreateTime:      common.GetTimestamp(),
			Status:          common.TopUpStatusSuccess,
		}
		require.NoError(t, DB.Create(topUp).Error)

		require.NoError(t, VoidTopUp("TLOCK-NOLINK", "无佣金链路作废", 7))
		var tu TopUp
		require.NoError(t, DB.Where("trade_no = ?", "TLOCK-NOLINK").First(&tu).Error)
		require.Equal(t, common.TopUpStatusRefunded, tu.Status)
		var revCnt int64
		require.NoError(t, DB.Model(&CommissionFlow{}).Count(&revCnt).Error)
		require.Zero(t, revCnt, "无佣金链路零流水")
	})

	t.Run("commissioned_void_semantics_unchanged", func(t *testing.T) {
		setupCommissionTestDB(t)
		withCommissionEnabled(t, true)
		// 真实记账产生 pending 原流水 → 作废成功：补锁不改变冲销语义
		f := seedCommissionFixture(t, seedCommissionOpts{
			paymentProvider: PaymentProviderEpay,
			money:           100.00,
			amount:          1000,
			rateBp:          1000,
			topupStatus:     common.TopUpStatusSuccess, // 仅 success 可作废
		})
		require.NoError(t, RecordCommissionTx(DB, f.TopUp))

		require.NoError(t, VoidTopUp(f.TopUp.TradeNo, "补锁后作废", 7))

		var tu TopUp
		require.NoError(t, DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&tu).Error)
		require.Equal(t, common.TopUpStatusRefunded, tu.Status)
		var rev CommissionFlow
		require.NoError(t, DB.Where("trade_no = ? AND flow_type = ?", f.TopUp.TradeNo, CommissionFlowReversal).First(&rev).Error)
		require.EqualValues(t, -1000, rev.AmountCents, "负数冲销原样")
		require.Equal(t, rev.Period, mustOriginalPeriod(t, f.TopUp.TradeNo), "冲销与原流水同账期（净额抵减）")
	})
}

// mustOriginalPeriod 取同单原 commission 流水的账期（冲销同账期断言用）。
func mustOriginalPeriod(t *testing.T, tradeNo string) string {
	t.Helper()
	var f CommissionFlow
	require.NoError(t, DB.Where("trade_no = ? AND flow_type = ?", tradeNo, CommissionFlowCommission).First(&f).Error)
	return f.Period
}
