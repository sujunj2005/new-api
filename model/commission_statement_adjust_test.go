package model

import (
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

		// 2 goroutine 并发各 +100：statement 行 FOR UPDATE 串行化，先读后算累计，无丢失更新
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for i := 0; i < 2; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- CreateStatementAdjustmentTx(stmt.Id, 100, "并发调整", 7)
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
