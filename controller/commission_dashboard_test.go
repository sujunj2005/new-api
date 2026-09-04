package controller

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 佣金总览面集成测试（契约 01-CONTRACT.md §4.3 A9/A12 冻结形状 + v1.5 附录 F2 笔数扩展，
// CUST-03/STMT-04/CUST-05）。复用 statement query 测试基建（harness + seed 全套，同包禁复制）；
// gin test router 挂真实 DistributorAuth 中间件（与 api-router.go 同形态），双头请求
// （Authorization + New-Api-User），越权断言沿用三形态口径（OQ-4/威胁 T-06-01-01）。

// dashSeedFlow 直插一条佣金流水（A9/A12 种子变体：状态可选；seedStmtFlow 固定已入账状态不适用）。
func dashSeedFlow(t *testing.T, billingNo string, customerId, distributorId int, amountCents int64, status string) {
	t.Helper()
	f := &model.CommissionFlow{
		BillingNo:     billingNo,
		FlowType:      model.CommissionFlowCommission,
		CustomerId:    customerId,
		DistributorId: distributorId,
		AmountCents:   amountCents,
		Status:        status,
		Period:        "2026-09",
	}
	require.NoError(t, model.DB.Create(f).Error)
}

// newDashboardTestRouter 真实中间件路由（A9/A12 逐行挂 DistributorAuth，与 api-router.go 挂载形态一致）。
func newDashboardTestRouter() *gin.Engine {
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("dash-test-secret"))))
	r.GET("/api/commission/dashboard", middleware.DistributorAuth(), GetCommissionDashboard)
	r.GET("/api/commission/current", middleware.DistributorAuth(), GetCurrentCommission)
	return r
}

// dashFrozenKeys A9 响应字段全集（§4.3 四 cents 逐字 + 附录 F2 四 count，无多余/缺失）。
var dashFrozenKeys = []string{
	"current_pending_cents", "total_statement_cents", "total_withdrawn_cents", "pending_withdraw_cents",
	"current_pending_count", "total_statement_count", "total_withdrawn_count", "pending_withdraw_count",
}

// dashRequireOK 断言成功信封 + data 为对象（禁裸数组/裸值，信封形态纪律）。
func dashRequireOK(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
	resp := parseRateEnvelope(t, w)
	require.True(t, resp["success"].(bool), "got: %v", resp)
	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok, "data 必须为对象，got: %T", resp["data"])
	return data
}

// TestCommissionDashboardAPI A9/A12 真实中间件路由集成（字段冻结 + 会话派生隔离 + 越权三形态 + A12 一致性）。
func TestCommissionDashboardAPI(t *testing.T) {
	setupStatementQueryTestDB(t)
	r := newDashboardTestRouter()

	distA := seedStmtUserWithToken(t, "dash_dist_a", common.RoleDistributorUser, "dash-dist-a-token")
	distB := seedStmtUserWithToken(t, "dash_dist_b", common.RoleDistributorUser, "dash-dist-b-token")
	commonUser := seedStmtUserWithToken(t, "dash_common", common.RoleCommonUser, "dash-common-token")
	admin := seedStmtUserWithToken(t, "dash_admin", common.RoleAdminUser, "dash-admin-token")
	cust := seedStmtUser(t, "dash_cust", common.RoleCommonUser)

	// distA 受控种子：未出账 1000（另铺 500 已入账防混入）；应付 50000
	dashSeedFlow(t, "DASH-A-001", cust.Id, distA.Id, 1000, model.CommissionFlowPending)
	dashSeedFlow(t, "DASH-A-002", cust.Id, distA.Id, 500, model.CommissionFlowAvailable)
	seedStmtRow(t, distA.Id, "2026-07", model.StatementPayable, 50000, 0)

	// distB 受控种子：未出账 700；应付 9000（与 distA 全部互异，隔离断言对象）
	dashSeedFlow(t, "DASH-B-001", cust.Id, distB.Id, 700, model.CommissionFlowPending)
	seedStmtRow(t, distB.Id, "2026-07", model.StatementPayable, 9000, 0)

	t.Run("field_freeze_exact_keys", func(t *testing.T) {
		w := wdDo(r, "GET", "/api/commission/dashboard", "dash-dist-a-token", distA.Id, "")
		data := dashRequireOK(t, w)
		require.Len(t, data, len(dashFrozenKeys), "字段全集精确（无多余/缺失），got keys: %v", data)
		for _, k := range dashFrozenKeys {
			require.Contains(t, data, k)
		}
		// distA 种子值：未出账 1000（已入账 500 不混入）+ 应付 50000
		require.Equal(t, float64(1000), data["current_pending_cents"])
		require.Equal(t, float64(1), data["current_pending_count"])
		require.Equal(t, float64(50000), data["pending_withdraw_cents"])
		require.Equal(t, float64(1), data["pending_withdraw_count"])
	})

	t.Run("session_derived_isolation", func(t *testing.T) {
		// A9 零请求参数，数据归属强制会话派生：改不了参数也看不到他人聚合
		wA := wdDo(r, "GET", "/api/commission/dashboard", "dash-dist-a-token", distA.Id, "")
		wB := wdDo(r, "GET", "/api/commission/dashboard", "dash-dist-b-token", distB.Id, "")
		dataA := dashRequireOK(t, wA)
		dataB := dashRequireOK(t, wB)
		require.Equal(t, float64(1000), dataA["current_pending_cents"], "distA 自查见自己的聚合")
		require.Equal(t, float64(700), dataB["current_pending_cents"], "distB 自查见自己的聚合")
		require.Equal(t, float64(50000), dataA["pending_withdraw_cents"])
		require.Equal(t, float64(9000), dataB["pending_withdraw_cents"])
		// 两响应互不相等（值域互斥目击）
		require.NotEqual(t, dataA["current_pending_cents"], dataB["current_pending_cents"])
		require.NotEqual(t, dataA["pending_withdraw_cents"], dataB["pending_withdraw_cents"])
	})

	t.Run("auth_rejection_three_forms_dashboard", func(t *testing.T) {
		// role=1（普通用户）/ role=10（管理员）/ 未登录 三形态一律拒（OQ-4 口径）
		w := wdDo(r, "GET", "/api/commission/dashboard", "dash-common-token", commonUser.Id, "")
		wdRequireAuthRejection(t, w)
		w2 := wdDo(r, "GET", "/api/commission/dashboard", "dash-admin-token", admin.Id, "")
		wdRequireAuthRejection(t, w2)
		w3 := wdDo(r, "GET", "/api/commission/dashboard", "", 0, "")
		wdRequireAuthRejection(t, w3)
	})

	t.Run("auth_rejection_three_forms_current", func(t *testing.T) {
		w := wdDo(r, "GET", "/api/commission/current", "dash-common-token", commonUser.Id, "")
		wdRequireAuthRejection(t, w)
		w2 := wdDo(r, "GET", "/api/commission/current", "dash-admin-token", admin.Id, "")
		wdRequireAuthRejection(t, w2)
		w3 := wdDo(r, "GET", "/api/commission/current", "", 0, "")
		wdRequireAuthRejection(t, w3)
	})

	t.Run("a12_frozen_shape_and_consistency", func(t *testing.T) {
		wA9 := wdDo(r, "GET", "/api/commission/dashboard", "dash-dist-a-token", distA.Id, "")
		dataA9 := dashRequireOK(t, wA9)
		w := wdDo(r, "GET", "/api/commission/current", "dash-dist-a-token", distA.Id, "")
		data := dashRequireOK(t, w)
		require.Len(t, data, 3, "A12 三字段冻结（附录 F3 零扩展）")
		require.Contains(t, data, "period")
		require.Contains(t, data, "pending_flows")
		require.Contains(t, data, "pending_cents")
		require.Regexp(t, `^\d{4}-\d{2}$`, data["period"], "period 为当前自然月标签")
		// 同谓词绑死：pending_cents/pending_flows == 同种子 A9.current_pending_cents/count
		require.Equal(t, dataA9["current_pending_cents"], data["pending_cents"])
		require.Equal(t, dataA9["current_pending_count"], data["pending_flows"])
	})
}
