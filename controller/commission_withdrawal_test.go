package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 提现面集成测试（契约 v1.4 附录 E2 A15-A21 + A10/A11 self 接线）。
// 平移 setupStatementQueryTestDB 模式（AutoMigrate 追加提现单表 + logs 供审计链路），
// gin test router 挂真实中间件链（DistributorAuth/AdminAuth/RootAuth），seedStmtUserWithToken
// 注入 AccessToken 会话。覆盖：权限双向回归（T-05-01-06）、跨分销商 403（Pitfall 3）、
// A15 全分支、A16 服务端收窄、A17 三过滤、A18-A21 全链路与审计分层断言（Pitfall 4）、
// A8 对 withdrawing 拒绝的回归锚（WDRAW-04 锁定语义后半环）。

// wdSeedSeq 提现单种子单号序列（uk_wd_no 防撞）。
var wdSeedSeq atomic.Int64

// setupWithdrawalTestDB 初始化提现面 controller 测试 DB（复用母本 setup + 追加提现单表；
// 母本已含 gin.TestMode / 全局 DB 覆盖 / 清理恢复纪律）。
// 末尾追加的短等待 cleanup 先于母本 DB 恢复执行（LIFO）：给 AdminAuth 兜底审计的
// 异步 gopool 写入留出窗口，避免测试结束后 goroutine 摸到已恢复为 nil 的全局 DB
// 触发 gopool recover 噪音（不影响测试结论，仅日志卫生）。
func setupWithdrawalTestDB(t *testing.T) {
	t.Helper()
	db := setupStatementQueryTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.CommissionWithdrawal{}))
	t.Cleanup(func() { time.Sleep(300 * time.Millisecond) })
}

// seedWithdrawalRow 直插一张提现单（铺五态任意前置态；model.seedWithdrawal 未导出，
// controller 包自建平移版）。
func seedWithdrawalRow(t *testing.T, statementId int64, distributorId int, status string) *model.CommissionWithdrawal {
	t.Helper()
	wd := &model.CommissionWithdrawal{
		WithdrawalNo:  fmt.Sprintf("WD-SEED-%d", wdSeedSeq.Add(1)),
		StatementId:   statementId,
		DistributorId: distributorId,
		Status:        status,
	}
	require.NoError(t, model.DB.Create(wd).Error)
	return wd
}

// newWithdrawalTestRouter gin test router：与 api-router.go 相同的中间件/handler 挂载形态
//（A15/A16/A10/A11 DistributorAuth 精确匹配；A17-A21 AdminAuth；A8 回归锚 RootAuth）。
func newWithdrawalTestRouter() *gin.Engine {
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("wd-test-secret"))))
	// 分销商侧（A10/A11/A15/A16）
	r.GET("/api/commission/statements/self", middleware.DistributorAuth(), GetSelfCommissionStatements)
	r.GET("/api/commission/statements/self/:id/items", middleware.DistributorAuth(), GetSelfCommissionStatementItems)
	r.POST("/api/commission/withdrawals", middleware.DistributorAuth(), CreateWithdrawal)
	r.GET("/api/commission/withdrawals/self", middleware.DistributorAuth(), GetSelfWithdrawals)
	// 管理侧（A17-A21）
	r.GET("/api/commission/withdrawals", middleware.AdminAuth(), GetWithdrawals)
	r.POST("/api/commission/withdrawals/:id/accept", middleware.AdminAuth(), AcceptWithdrawal)
	r.POST("/api/commission/withdrawals/:id/reject", middleware.AdminAuth(), RejectWithdrawal)
	r.POST("/api/commission/withdrawals/:id/approve", middleware.AdminAuth(), ApproveWithdrawal)
	r.POST("/api/commission/withdrawals/:id/paid", middleware.AdminAuth(), MarkWithdrawalPaid)
	// 超管侧（A8 回归锚）
	r.POST("/api/commission/statements/:id/adjustments", middleware.RootAuth(), CreateStatementAdjustment)
	return r
}

// wdDo 发起带 AccessToken 会话的请求（Authorization + New-Api-User 双头，04-02 先例）。
func wdDo(r *gin.Engine, method, path, token string, userId int, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", token)
		req.Header.Set("New-Api-User", strconv.Itoa(userId))
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// wdOK 解析信封 success 字段。
func wdOK(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	resp := parseRateEnvelope(t, w)
	ok, _ := resp["success"].(bool)
	return ok
}

// wdMsg 解析信封 message 字段。
func wdMsg(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	resp := parseRateEnvelope(t, w)
	msg, _ := resp["message"].(string)
	return msg
}

// wdStatementStatus 重读账单状态（断言库内联动终态）。
func wdStatementStatus(t *testing.T, id int64) string {
	t.Helper()
	var s model.CommissionStatement
	require.NoError(t, model.DB.First(&s, "id = ?", id).Error)
	return s.Status
}

// wdWithdrawalStatus 重读提现单状态。
func wdWithdrawalStatus(t *testing.T, id int64) string {
	t.Helper()
	var w model.CommissionWithdrawal
	require.NoError(t, model.DB.First(&w, "id = ?", id).Error)
	return w.Status
}

// wdRequireAuthRejection 权限拒绝多形态断言（阈值拒绝 200+success:false / 精确匹配
// 拒绝 403 / 身份不匹配 401，三者都算拒——禁止数据泄漏才是断言本体，04-02 先例）。
func wdRequireAuthRejection(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code == http.StatusForbidden || w.Code == http.StatusUnauthorized {
		return
	}
	require.Equal(t, http.StatusOK, w.Code, "unexpected code, body: %s", w.Body.String())
	require.False(t, wdOK(t, w), "must be rejected, body: %s", w.Body.String())
}

// TestSelfStatement A10/A11 self 接线全分支（STMT-04/CUST-05 + gin 静态段优先回归）。
func TestSelfStatement(t *testing.T) {
	setupWithdrawalTestDB(t)
	distA := seedStmtUserWithToken(t, "self_dist_a", common.RoleDistributorUser, "self-dist-a-token")
	distB := seedStmtUserWithToken(t, "self_dist_b", common.RoleDistributorUser, "self-dist-b-token")
	admin := seedStmtUserWithToken(t, "self_admin", common.RoleAdminUser, "self-admin-token")
	commonUser := seedStmtUserWithToken(t, "self_common", common.RoleCommonUser, "self-common-token")
	cust := seedStmtUser(t, "self_cust", common.RoleCommonUser)

	sA1 := seedStmtRow(t, distA.Id, "2026-07", model.StatementPayable, 1000, 0)
	seedStmtRow(t, distA.Id, "2026-08", model.StatementPayable, 500, 0)
	sB1 := seedStmtRow(t, distB.Id, "2026-07", model.StatementPayable, 300, 0)
	seedStmtItem(t, sA1.Id, "SELF-PAY-001", "wxpay", 7000, 700, cust.Id)

	r := newWithdrawalTestRouter()

	t.Run("a10_self_only", func(t *testing.T) {
		w := wdDo(r, "GET", "/api/commission/statements/self?p=1&page_size=10", "self-dist-a-token", distA.Id, "")
		require.Equal(t, 200, w.Code)
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		data := parseRateEnvelope(t, w)["data"].(map[string]interface{})
		require.Equal(t, float64(2), data["total"], "distA 仅本人 2 张账单")
		for _, it := range data["items"].([]interface{}) {
			require.Equal(t, float64(distA.Id), it.(map[string]interface{})["distributor_id"], "A10 服务端收窄（ROLE-02）")
		}
	})

	t.Run("a11_own_items_with_username", func(t *testing.T) {
		w := wdDo(r, "GET", fmt.Sprintf("/api/commission/statements/self/%d/items?p=1&page_size=10", sA1.Id), "self-dist-a-token", distA.Id, "")
		require.Equal(t, 200, w.Code)
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		items := parseRateEnvelope(t, w)["data"].(map[string]interface{})["items"].([]interface{})
		require.Len(t, items, 1)
		require.Equal(t, "self_cust", items[0].(map[string]interface{})["username"], "A11 明细含 username 冗余（A6 同构）")
	})

	t.Run("a11_cross_distributor_403", func(t *testing.T) {
		w := wdDo(r, "GET", fmt.Sprintf("/api/commission/statements/self/%d/items", sB1.Id), "self-dist-a-token", distA.Id, "")
		require.Equal(t, http.StatusForbidden, w.Code, "归属越权必须 403, got: %s", w.Body.String())
		require.False(t, wdOK(t, w))
		require.Equal(t, "无权访问该账单", wdMsg(t, w))
	})

	t.Run("a11_invalid_id_rejected", func(t *testing.T) {
		w := wdDo(r, "GET", "/api/commission/statements/self/abc/items", "self-dist-a-token", distA.Id, "")
		require.False(t, wdOK(t, w), "strconv 失败 → 400 语义信封")
	})

	t.Run("a10_not_swallowed_by_id_param", func(t *testing.T) {
		// gin 静态段优先（A14 先例同款）：/statements/self 不得被 /statements/:id 吞噬
		w := wdDo(r, "GET", "/api/commission/statements/self", "self-dist-a-token", distA.Id, "")
		require.Equal(t, 200, w.Code)
		require.True(t, wdOK(t, w), "静态段必须命中 A10（若被 :id 吞噬则走 AdminAuth 阈值拒绝）, body: %s", w.Body.String())
	})

	t.Run("permission_common_and_admin_on_distributor_endpoints", func(t *testing.T) {
		// role=1：authHelper 阈值（1<5）拒绝——双形态断言，禁数据泄漏
		w1 := wdDo(r, "GET", "/api/commission/statements/self", "self-common-token", commonUser.Id, "")
		wdRequireAuthRejection(t, w1)
		require.NotContains(t, w1.Body.String(), `"items"`, "role=1 不得泄漏账单数据")
		// role=1 → A15 同拒
		w2 := wdDo(r, "POST", "/api/commission/withdrawals", "self-common-token", commonUser.Id, `{"statement_id":1}`)
		wdRequireAuthRejection(t, w2)
		// admin(10) 穿透分销商端点 → DistributorAuth 精确匹配 403（T-05-01-06）
		w3 := wdDo(r, "GET", "/api/commission/statements/self", "self-admin-token", admin.Id, "")
		require.Equal(t, http.StatusForbidden, w3.Code, "admin 穿透必须 403, got: %s", w3.Body.String())
		// admin(10) → A15 同拒
		w4 := wdDo(r, "POST", "/api/commission/withdrawals", "self-admin-token", admin.Id, `{"statement_id":1}`)
		require.Equal(t, http.StatusForbidden, w4.Code, "admin 穿透必须 403")
	})
}

// TestWithdrawalAPI A15-A21 中间件链全分支 + A8 回归锚 + 审计分层断言。
func TestWithdrawalAPI(t *testing.T) {
	setupWithdrawalTestDB(t)
	admin := seedStmtUserWithToken(t, "wd_admin", common.RoleAdminUser, "wd-admin-token")
	root := seedStmtUserWithToken(t, "wd_root", common.RoleRootUser, "wd-root-token")
	distA := seedStmtUserWithToken(t, "wd_dist_a", common.RoleDistributorUser, "wd-dist-a-token")
	distB := seedStmtUserWithToken(t, "wd_dist_b", common.RoleDistributorUser, "wd-dist-b-token")
	commonUser := seedStmtUserWithToken(t, "wd_common", common.RoleCommonUser, "wd-common-token")

	// 账单铺底（uk_stmt_period 语义：同分销商不同期）
	sApply := seedStmtRow(t, distA.Id, "2026-07", model.StatementPayable, 1000, 0)    // A15 happy（settle=1000）
	sCross := seedStmtRow(t, distA.Id, "2026-08", model.StatementPayable, 500, 0)     // 跨分销商 403 对照
	sChain := seedStmtRow(t, distA.Id, "2026-09", model.StatementPayable, 2000, 0)    // A18-A21 全链路
	sWithB := seedStmtRow(t, distB.Id, "2026-08", model.StatementWithdrawing, 800, 0) // 预置 withdrawing
	sSettled := seedStmtRow(t, distB.Id, "2026-07", model.StatementSettled, 300, 0)   // settled 拒绝
	wdB := seedWithdrawalRow(t, sWithB.Id, distB.Id, model.WithdrawalPending)         // A17 过滤/A18 受理对象

	r := newWithdrawalTestRouter()

	t.Run("permission_distributor_role_on_admin_endpoints", func(t *testing.T) {
		// role=5 → A17（AdminAuth 阈值 5<10 拒绝，双形态）；数据面零泄漏
		w1 := wdDo(r, "GET", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, "")
		wdRequireAuthRejection(t, w1)
		require.NotContains(t, w1.Body.String(), `"items"`, "role=5 不得泄漏提现单数据")
		// role=5 → A18/A19/A20/A21 全拒
		for _, path := range []string{
			fmt.Sprintf("/api/commission/withdrawals/%d/accept", wdB.Id),
			fmt.Sprintf("/api/commission/withdrawals/%d/reject", wdB.Id),
			fmt.Sprintf("/api/commission/withdrawals/%d/approve", wdB.Id),
			fmt.Sprintf("/api/commission/withdrawals/%d/paid", wdB.Id),
		} {
			w := wdDo(r, "POST", path, "wd-dist-a-token", distA.Id, `{}`)
			wdRequireAuthRejection(t, w)
		}
		// role=1 → A17 同拒
		w2 := wdDo(r, "GET", "/api/commission/withdrawals", "wd-common-token", commonUser.Id, "")
		wdRequireAuthRejection(t, w2)
	})

	t.Run("a15_full_branches", func(t *testing.T) {
		// payable 成功：账单翻 withdrawing + 提现单落库
		w := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, fmt.Sprintf(`{"statement_id":%d}`, sApply.Id))
		require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		require.Equal(t, model.StatementWithdrawing, wdStatementStatus(t, sApply.Id), "payable→withdrawing（D-05）")
		var wd model.CommissionWithdrawal
		require.NoError(t, model.DB.Where("statement_id = ?", sApply.Id).First(&wd).Error)
		require.Equal(t, model.WithdrawalPending, wd.Status)
		require.Equal(t, distA.Id, wd.DistributorId)

		// 跨分销商 403（T-05-01-01）：distB 对 distA 的 payable 账单申请
		w2 := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-b-token", distB.Id, fmt.Sprintf(`{"statement_id":%d}`, sCross.Id))
		require.Equal(t, http.StatusForbidden, w2.Code, "got: %s", w2.Body.String())
		require.False(t, wdOK(t, w2))
		require.Contains(t, wdMsg(t, w2), "无权操作该账单")
		require.Equal(t, model.StatementPayable, wdStatementStatus(t, sCross.Id), "越权申请零翻转")

		// withdrawing 拒绝（文案区分，Pitfall 6）
		w3 := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, fmt.Sprintf(`{"statement_id":%d}`, sApply.Id))
		require.False(t, wdOK(t, w3))
		require.Contains(t, wdMsg(t, w3), "提现中")

		// settled 拒绝
		w4 := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-b-token", distB.Id, fmt.Sprintf(`{"statement_id":%d}`, sSettled.Id))
		require.False(t, wdOK(t, w4))
		require.Contains(t, wdMsg(t, w4), "已结清")

		// statement_id 缺失（绑定为 0 → 账单不存在）
		w5 := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, `{}`)
		require.False(t, wdOK(t, w5))

		// 非法 JSON 类型 → 绑定失败
		w6 := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, `{"statement_id":"abc"}`)
		require.False(t, wdOK(t, w6))
	})

	t.Run("a16_self_narrowing", func(t *testing.T) {
		// distB 视角：仅自己的 wdB（服务端 WHERE 收窄，非前端过滤）
		w := wdDo(r, "GET", "/api/commission/withdrawals/self?p=1&page_size=10", "wd-dist-b-token", distB.Id, "")
		require.Equal(t, 200, w.Code)
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		data := parseRateEnvelope(t, w)["data"].(map[string]interface{})
		require.Equal(t, float64(1), data["total"], "服务端收窄：distB 仅自己的提现单")
		items := data["items"].([]interface{})
		require.Len(t, items, 1)
		row := items[0].(map[string]interface{})
		require.Equal(t, float64(distB.Id), row["distributor_id"])
		require.Equal(t, sWithB.Period, row["period"], "JOIN statement 冗余 period")
		require.Equal(t, float64(sWithB.SettleAmountCents), row["settle_amount_cents"], "D-08 金额实时取 statement")

		// distA 视角：看不到 distB 的行
		w2 := wdDo(r, "GET", "/api/commission/withdrawals/self", "wd-dist-a-token", distA.Id, "")
		data2 := parseRateEnvelope(t, w2)["data"].(map[string]interface{})
		for _, it := range data2["items"].([]interface{}) {
			require.NotEqual(t, float64(distB.Id), it.(map[string]interface{})["distributor_id"])
		}
	})

	t.Run("a17_admin_list_filters", func(t *testing.T) {
		// 无过滤全量（distA 1 条 + distB 1 条）+ username 冗余 + id DESC
		w := wdDo(r, "GET", "/api/commission/withdrawals?p=1&page_size=10", "wd-admin-token", admin.Id, "")
		require.Equal(t, 200, w.Code)
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		data := parseRateEnvelope(t, w)["data"].(map[string]interface{})
		require.Equal(t, float64(2), data["total"])
		items := data["items"].([]interface{})
		require.Len(t, items, 2)
		require.Equal(t, "wd_dist_a", items[0].(map[string]interface{})["username"], "id DESC：a15 新申请在前 + username JOIN")
		require.Equal(t, "wd_dist_b", items[1].(map[string]interface{})["username"])

		// distributor_id 过滤
		w2 := wdDo(r, "GET", fmt.Sprintf("/api/commission/withdrawals?distributor_id=%d", distB.Id), "wd-admin-token", admin.Id, "")
		data2 := parseRateEnvelope(t, w2)["data"].(map[string]interface{})
		require.Equal(t, float64(1), data2["total"])
		require.Equal(t, float64(distB.Id), data2["items"].([]interface{})[0].(map[string]interface{})["distributor_id"])

		// status 过滤
		w3 := wdDo(r, "GET", "/api/commission/withdrawals?status=pending", "wd-admin-token", admin.Id, "")
		require.Equal(t, float64(2), parseRateEnvelope(t, w3)["data"].(map[string]interface{})["total"])
		w4 := wdDo(r, "GET", "/api/commission/withdrawals?status=approved", "wd-admin-token", admin.Id, "")
		require.Equal(t, float64(0), parseRateEnvelope(t, w4)["data"].(map[string]interface{})["total"])

		// 非法 distributor_id → 400 语义信封
		w5 := wdDo(r, "GET", "/api/commission/withdrawals?distributor_id=abc", "wd-admin-token", admin.Id, "")
		require.False(t, wdOK(t, w5))
		require.Contains(t, wdMsg(t, w5), "无效的分销商ID")
	})

	t.Run("a18_accept_then_double_reject", func(t *testing.T) {
		w := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/accept", wdB.Id), "wd-admin-token", admin.Id, "")
		require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
		require.True(t, wdOK(t, w), "body: %s", w.Body.String())
		var wd model.CommissionWithdrawal
		require.NoError(t, model.DB.First(&wd, "id = ?", wdB.Id).Error)
		require.Equal(t, model.WithdrawalReviewing, wd.Status)
		require.Greater(t, wd.ReviewedAt, int64(0), "reviewed_at 落值")
		require.Equal(t, admin.Id, wd.OperatorId)
		require.Equal(t, model.StatementWithdrawing, wdStatementStatus(t, sWithB.Id), "受理账单零操作（D-05）")

		// 重复受理 → 拒绝（状态已变化）
		w2 := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/accept", wdB.Id), "wd-admin-token", admin.Id, "")
		require.False(t, wdOK(t, w2), "非申请中状态拒绝")
	})

	t.Run("a19_reject_branches", func(t *testing.T) {
		// 审计失败路径前置断言：此时尚无任何提现驳回留痕
		var rejected int64
		require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现驳回%").Count(&rejected).Error)
		require.Zero(t, rejected)

		// pending 直驳回（Q2=B：仅 reviewing 可驳回，必须先受理）
		// 独立 pending 单做负测试（wdB 已被 a18 子测推进到 reviewing，不可复用）
		sPend := seedStmtRow(t, distB.Id, "2026-11", model.StatementWithdrawing, 100, 0)
		wdPend := seedWithdrawalRow(t, sPend.Id, distB.Id, model.WithdrawalPending)
		w0 := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/reject", wdPend.Id), "wd-admin-token", admin.Id, `{"reason":"跳步驳回"}`)
		require.False(t, wdOK(t, w0), "pending 直驳回必须拒绝")
		require.Contains(t, wdMsg(t, w0), "仅审核中可驳回")
		require.Equal(t, model.WithdrawalPending, wdWithdrawalStatus(t, wdPend.Id), "负测试零迁移")
		require.Equal(t, model.StatementWithdrawing, wdStatementStatus(t, sPend.Id))

		// 铺 reviewing 态提现单（账单 withdrawing）供驳回分支
		sRej := seedStmtRow(t, distA.Id, "2026-10", model.StatementWithdrawing, 600, 0)
		wdRej := seedWithdrawalRow(t, sRej.Id, distA.Id, model.WithdrawalReviewing)

		// reason 缺失 → 400 语义 + 零迁移 + 零驳回留痕
		for _, body := range []string{`{}`, `{"reason":""}`, `{"reason":"   "}`} {
			w := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/reject", wdRej.Id), "wd-admin-token", admin.Id, body)
			require.False(t, wdOK(t, w), "body=%s got: %s", body, w.Body.String())
			require.Contains(t, wdMsg(t, w), "驳回原因不能为空")
		}
		require.Equal(t, model.WithdrawalReviewing, wdWithdrawalStatus(t, wdRej.Id), "校验失败零迁移")
		require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现驳回%").Count(&rejected).Error)
		require.Zero(t, rejected, "失败路径零成功留痕（T-05-01-10）")

		// 成功驳回：reviewing→rejected + reason + 账单回退 payable + 审计留痕
		w := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/reject", wdRej.Id), "wd-admin-token", admin.Id, `{"reason":"凭证信息不符"}`)
		require.Equal(t, 200, w.Code, "body: %s", w.Body.String())
		require.True(t, wdOK(t, w))
		require.NoError(t, model.DB.First(&wdRej, "id = ?", wdRej.Id).Error)
		require.Equal(t, model.WithdrawalRejected, wdRej.Status)
		require.Equal(t, "凭证信息不符", wdRej.Reason)
		require.Greater(t, wdRej.RejectedAt, int64(0))
		require.Equal(t, model.StatementPayable, wdStatementStatus(t, sRej.Id), "驳回回退 payable（D-05）")

		// 审计分层（Pitfall 4）：RecordLog 断业务详情（withdrawal_no/statement_id/amount 快照/reason）
		var logs []model.Log
		require.NoError(t, model.DB.Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现驳回%").Find(&logs).Error)
		require.Len(t, logs, 1)
		require.Contains(t, logs[0].Content, fmt.Sprintf("withdrawal_no=%s", wdRej.WithdrawalNo))
		require.Contains(t, logs[0].Content, fmt.Sprintf("statement_id=%d", sRej.Id))
		require.Contains(t, logs[0].Content, fmt.Sprintf("amount_cents=%d", sRej.SettleAmountCents), "D-08 实时值审计兜底")
		require.Contains(t, logs[0].Content, "reason=凭证信息不符")
	})

	t.Run("a20_a21_full_chain_and_audits", func(t *testing.T) {
		// A15 → [A21 跳步拒绝] → A18 → A20 → A21 全链路
		wApply := wdDo(r, "POST", "/api/commission/withdrawals", "wd-dist-a-token", distA.Id, fmt.Sprintf(`{"statement_id":%d}`, sChain.Id))
		require.True(t, wdOK(t, wApply), "body: %s", wApply.Body.String())
		var wd model.CommissionWithdrawal
		require.NoError(t, model.DB.Where("statement_id = ?", sChain.Id).First(&wd).Error)

		// reviewing 直打款（跳过 approved）→ 拒绝（Q1=B 五态）
		wSkip := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/paid", wd.Id), "wd-admin-token", admin.Id, `{"voucher_no":"V-EARLY"}`)
		require.False(t, wdOK(t, wSkip), "reviewing 直打款必须拒绝")
		require.Contains(t, wdMsg(t, wSkip), "仅已批准可打款")

		// A18 受理
		wAccept := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/accept", wd.Id), "wd-admin-token", admin.Id, "")
		require.True(t, wdOK(t, wAccept), "body: %s", wAccept.Body.String())

		// A20 批准：approved + 账单保持 withdrawing（D-12）
		wApprove := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/approve", wd.Id), "wd-admin-token", admin.Id, "")
		require.Equal(t, 200, wApprove.Code, "body: %s", wApprove.Body.String())
		require.True(t, wdOK(t, wApprove))
		require.NoError(t, model.DB.First(&wd, "id = ?", wd.Id).Error)
		require.Equal(t, model.WithdrawalApproved, wd.Status)
		require.Greater(t, wd.ApprovedAt, int64(0), "approved_at 落值（Q1=B）")
		require.Equal(t, model.StatementWithdrawing, wdStatementStatus(t, sChain.Id), "批准期间账单保持 withdrawing（D-12）")

		// A21 无凭证 → 拒绝
		wNoVoucher := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/paid", wd.Id), "wd-admin-token", admin.Id, `{"voucher_no":"  "}`)
		require.False(t, wdOK(t, wNoVoucher))
		require.Contains(t, wdMsg(t, wNoVoucher), "打款凭证号不能为空")

		// A21 打款登记：paid + voucher + 账单 settled
		wPaid := wdDo(r, "POST", fmt.Sprintf("/api/commission/withdrawals/%d/paid", wd.Id), "wd-admin-token", admin.Id, `{"voucher_no":"VOUCHER-2026-001"}`)
		require.True(t, wdOK(t, wPaid), "body: %s", wPaid.Body.String())
		require.NoError(t, model.DB.First(&wd, "id = ?", wd.Id).Error)
		require.Equal(t, model.WithdrawalPaid, wd.Status)
		require.Equal(t, "VOUCHER-2026-001", wd.VoucherNo)
		require.Greater(t, wd.PaidAt, int64(0))
		require.Equal(t, model.StatementSettled, wdStatementStatus(t, sChain.Id), "打款结清（D-05）")

		// 审计分层：手动 RecordLog 详情（受理/批准/打款）+ 兜底存在性（异步 gopool，Eventually 轮询）
		var acceptLogs, approveLogs, paidLogs int64
		require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现受理 withdrawal_no=%").Count(&acceptLogs).Error)
		require.Greater(t, acceptLogs, int64(1), "A18 审计（a18 子测 1 次 + 本链路 1 次）")
		require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现批准 withdrawal_no=%").Count(&approveLogs).Error)
		require.Equal(t, int64(1), approveLogs)
		require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现打款 withdrawal_no=%").Count(&paidLogs).Error)
		require.Equal(t, int64(1), paidLogs)
		var paidLog model.Log
		require.NoError(t, model.DB.Where("type = ? AND content LIKE ?", model.LogTypeManage, "%提现打款 withdrawal_no=%").First(&paidLog).Error)
		require.Contains(t, paidLog.Content, "voucher_no=VOUCHER-2026-001")
		require.Contains(t, paidLog.Content, fmt.Sprintf("amount_cents=%d", sChain.SettleAmountCents))

		// 兜底审计只断存在性（禁 COUNT==1 硬编码，Pitfall 4）
		require.Eventually(t, func() bool {
			var n int64
			err := model.DB.Model(&model.Log{}).
				Where("type = ? AND content LIKE ?", model.LogTypeManage, "POST /api/commission/withdrawals/%").
				Count(&n).Error
			return err == nil && n >= 1
		}, 5*time.Second, 100*time.Millisecond, "AdminAuth 写操作兜底审计必须存在（T-05-01-07 第三层）")
	})

	t.Run("regression_a8_rejects_withdrawing_statement", func(t *testing.T) {
		// WDRAW-04 锁定语义后半环（既有 A8 分支，04-03 已交付零改动）：withdrawing 账单调账拒绝
		w := wdDo(r, "POST", fmt.Sprintf("/api/commission/statements/%d/adjustments", sWithB.Id), "wd-root-token", root.Id, `{"delta_cents":-50,"reason":"测试调账"}`)
		require.False(t, wdOK(t, w), "提现中账单禁止开调整单, body: %s", w.Body.String())
		require.Contains(t, wdMsg(t, w), "提现中")
		// settled 同拒（COMM-07）
		w2 := wdDo(r, "POST", fmt.Sprintf("/api/commission/statements/%d/adjustments", sSettled.Id), "wd-root-token", root.Id, `{"delta_cents":-50,"reason":"测试调账"}`)
		require.False(t, wdOK(t, w2))
	})
}
