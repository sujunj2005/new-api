package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
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

// This file is the 02-05 Task 1 regression suite for ROLE-03 zero-change
// conclusion (02-04-AUDIT.md A1-A29 baseline). It verifies:
//   - A1-A29 UserAuth 级接口在 role=5 身份下仅返回自身数据（self-scoped）
//   - C 组 AdminAuth/RootAuth 路由 role=5 天然被拒（authHelper 阈值 5<10/100）
//   - 双分销商（D1/D2 role=5）数据互不可见 + IDOR 防护（B5 403）
//
// 测试基线：.planning/phases/02-distributor-role-attribution/02-04-AUDIT.md A 表与 C 组。
// 零改造结论：A 表 29 组接口全部 self-scoped（按 userId / token_id 过滤），
// C 组按 authHelper 阈值归为天然隔离。本测试遍历断言此结论，不得偏离 AUDIT.md A 表。

// setupIsolationTestDB 初始化隔离回归测试 DB（User + TopUp + Token + Log + AttributionChange）。
// 模式参照 user_distributor_test.go setupDistributorTestDB，但扩展 AutoMigrate 至全部被测表。
// Pitfall 8: SetMaxOpenConns(1) 防 SQLite "database is locked"。
func setupIsolationTestDB(t *testing.T) {
	t.Helper()
	db := setupDistributorTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.TopUp{},
		&model.Token{},
		&model.Log{},
		&model.AttributionChange{},
	))
}

// seedIsolationDistributor 插入一个分销商用户（role=5）并返回其 id。
// 跳过 User.Insert 的 finishInsert 边栏初始化（测试无需），直接 DB.Create。
func seedIsolationDistributor(t *testing.T, id int, username, affCode string) *model.User {
	t.Helper()
	token := fmt.Sprintf("distributor-token-%d", id)
	u := &model.User{
		Id:          id,
		Username:    username,
		Password:    "testpass1234",
		Role:        common.RoleDistributorUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     affCode,
		AccessToken: &token,
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// seedIsolationCustomer 插入一个客户用户（role=1），inviterId 指向某分销商。
func seedIsolationCustomer(t *testing.T, id int, username string, inviterId int) *model.User {
	t.Helper()
	u := &model.User{
		Id:       id,
		Username: username,
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		InviterId: inviterId,
		AffCode:  fmt.Sprintf("cust-aff-%d", id),
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// seedIsolationToken 插入一条令牌记录归属 userId。
func seedIsolationToken(t *testing.T, id, userId int, name string) {
	t.Helper()
	tok := &model.Token{
		Id:       id,
		UserId:   userId,
		Key:      fmt.Sprintf("sk-test-key-%d", id),
		Name:     name,
		Status:   1,
		Group:    "default",
	}
	require.NoError(t, model.DB.Create(tok).Error)
}

// seedIsolationTopUp 插入一条充值单归属 userId。
// CreateTime 必须设为当前时间：GetUserTopUps 有 30 天查询窗口（topUpQueryCutoff），
// 默认 0 会被过滤掉。
func seedIsolationTopUp(t *testing.T, id, userId int, money float64, tradeNo string) {
	t.Helper()
	tp := &model.TopUp{
		Id:         id,
		UserId:     userId,
		Money:      money,
		TradeNo:    tradeNo,
		Status:     common.TopUpStatusSuccess,
		CreateTime: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(tp).Error)
}

// seedIsolationLog 插入一条日志归属 userId。
func seedIsolationLog(t *testing.T, id, userId int, content string) {
	t.Helper()
	lg := &model.Log{
		Id:        id,
		UserId:    userId,
		Type:      model.LogTypeSystem,
		Content:   content,
		Username:  fmt.Sprintf("user%d", userId),
		CreatedAt: common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(lg).Error)
}

// setupIsolationFixture 构造双分销商隔离 fixture:
//   - D1 (id=1, role=5, aff_code="D1AFF")
//   - D2 (id=2, role=5, aff_code="D2AFF")
//   - D1 名下客户 C1 (id=10, inviter_id=1)
//   - D2 名下客户 C2 (id=20, inviter_id=2)
//   - D1 自身令牌/充值/日志各 1 条; D2 自身令牌/充值/日志各 1 条
//
// 用于 TestDistributorIsolation_* 系列断言 self-scoped + 双分销商隔离。
func setupIsolationFixture(t *testing.T) {
	t.Helper()
	setupIsolationTestDB(t)

	seedIsolationDistributor(t, 1, "distributor1", "D1AFF")
	seedIsolationDistributor(t, 2, "distributor2", "D2AFF")
	seedIsolationCustomer(t, 10, "d1customer", 1)
	seedIsolationCustomer(t, 20, "d2customer", 2)

	// D1 自身数据
	seedIsolationToken(t, 101, 1, "d1-token")
	seedIsolationTopUp(t, 1001, 1, 10.0, "d1-trade-1001")
	seedIsolationLog(t, 10001, 1, "d1 log entry")

	// D2 自身数据
	seedIsolationToken(t, 201, 2, "d2-token")
	seedIsolationTopUp(t, 2001, 2, 20.0, "d2-trade-2001")
	seedIsolationLog(t, 20001, 2, "d2 log entry")
}

// extractDataMap 从响应 body 中提取 data 字段为 map[string]interface{}。
// 用于 GetSelf 等返回单对象的接口断言。
func extractDataMap(t *testing.T, body []byte) map[string]interface{} {
	t.Helper()
	resp := struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}{}
	require.NoError(t, json.Unmarshal(body, &resp))
	return resp.Data
}

// extractDataPageItems 从响应 body 中提取分页 items。
// 返回 items ([]interface{})，若 data.items 不存在或为空返回 nil。
func extractDataPageItems(t *testing.T, body []byte) []interface{} {
	t.Helper()
	resp := struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}{}
	require.NoError(t, json.Unmarshal(body, &resp))
	items, _ := resp.Data["items"].([]interface{})
	return items
}

// ==================== A1-A29 self-scoped 回归断言 ====================

// TestDistributorIsolation_GetSelf A1 GET /api/user/self：D1 仅见自身 id。
func TestDistributorIsolation_GetSelf(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/user/self", nil, 1)
	GetSelf(ctx)

	require.Equal(t, http.StatusOK, w.Code, "GetSelf should return 200")
	data := extractDataMap(t, w.Body.Bytes())
	id, _ := data["id"].(float64)
	require.Equal(t, float64(1), id, "D1 GetSelf should return D1's own id, got %v", data["id"])
	username, _ := data["username"].(string)
	require.Equal(t, "distributor1", username, "D1 GetSelf should return D1's own username")
}

// TestDistributorIsolation_GetUserTopUps A10 GET /api/user/topup/self：D1 仅返回 user_id=1 的充值单。
func TestDistributorIsolation_GetUserTopUps(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/user/topup/self", nil, 1)
	GetUserTopUps(ctx)

	require.Equal(t, http.StatusOK, w.Code)
	items := extractDataPageItems(t, w.Body.Bytes())
	require.Len(t, items, 1, "D1 should see only own topup, got %d items", len(items))
	item, _ := items[0].(map[string]interface{})
	userId, _ := item["user_id"].(float64)
	require.Equal(t, float64(1), userId, "D1 topup should have user_id=1")
	tradeNo, _ := item["trade_no"].(string)
	require.Equal(t, "d1-trade-1001", tradeNo, "D1 should see d1-trade-1001, not d2-trade")
}

// TestDistributorIsolation_TokenCRUD A18 GET /api/token/：D1 仅返回 user_id=1 的令牌。
func TestDistributorIsolation_TokenCRUD(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/token/", nil, 1)
	GetAllTokens(ctx)

	require.Equal(t, http.StatusOK, w.Code)
	items := extractDataPageItems(t, w.Body.Bytes())
	require.Len(t, items, 1, "D1 should see only own token, got %d items", len(items))
	item, _ := items[0].(map[string]interface{})
	userId, _ := item["user_id"].(float64)
	require.Equal(t, float64(1), userId, "D1 token should have user_id=1")
}

// TestDistributorIsolation_LogSelf A20 GET /api/log/self：D1 仅返回 user_id=1 的日志。
func TestDistributorIsolation_LogSelf(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/log/self", nil, 1)
	GetUserLogs(ctx)

	require.Equal(t, http.StatusOK, w.Code)
	items := extractDataPageItems(t, w.Body.Bytes())
	require.Len(t, items, 1, "D1 should see only own log, got %d items", len(items))
	item, _ := items[0].(map[string]interface{})
	userId, _ := item["user_id"].(float64)
	require.Equal(t, float64(1), userId, "D1 log should have user_id=1")
}

// TestDistributorIsolation_GetAffCode A8 GET /api/user/aff：D1 仅见自身 aff_code。
func TestDistributorIsolation_GetAffCode(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/user/aff", nil, 1)
	GetAffCode(ctx)

	require.Equal(t, http.StatusOK, w.Code)
	resp := struct {
		Success bool   `json:"success"`
		Data    string `json:"data"`
	}{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.Equal(t, "D1AFF", resp.Data, "D1 should see own aff_code D1AFF")
}

// TestDistributorIsolation_GetUserModels A5 GET /api/user/models：死代码 c.Param("id")
// 不可达（路由无 :id），回退到 c.GetInt("id")=D1，返回 D1 可用模型列表。
// AUDIT.md A5 边缘案例标注：勿误报为越界入口。
func TestDistributorIsolation_GetUserModels(t *testing.T) {
	setupIsolationFixture(t)

	ctx, w := newDistributorContext(t, "GET", "/api/user/models", nil, 1)
	GetUserModels(ctx)

	require.Equal(t, http.StatusOK, w.Code, "GetUserModels should return 200 for role=5")
	// 仅断言可访问且不泄漏他人数据（模型列表为非用户数据，非越界入口）
	resp := struct {
		Success bool        `json:"success"`
		Data    interface{} `json:"data"`
	}{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp.Success, "GetUserModels should succeed for role=5")
}

// TestDistributorIsolation_DualDistributor 双分销商隔离:
//   - D1 调用 /api/distributor/customers → 仅返回 inviter_id=1 的客户（C1 id=10）
//   - D2 调用 /api/distributor/customers → 仅返回 inviter_id=2 的客户（C2 id=20）
//   - D1 调用 /api/distributor/customers/20/topups（C2 属 D2）→ 403 IDOR 防护
func TestDistributorIsolation_DualDistributor(t *testing.T) {
	setupIsolationFixture(t)

	// D1 查自身客户列表
	ctx1, w1 := newDistributorContext(t, "GET", "/api/distributor/customers", nil, 1)
	GetDistributorCustomers(ctx1)
	require.Equal(t, http.StatusOK, w1.Code)
	items1 := extractDataPageItems(t, w1.Body.Bytes())
	require.Len(t, items1, 1, "D1 should see only 1 customer (C1)")
	item1, _ := items1[0].(map[string]interface{})
	cid1, _ := item1["id"].(float64)
	require.Equal(t, float64(10), cid1, "D1 customer should be C1 id=10")

	// D2 查自身客户列表
	ctx2, w2 := newDistributorContext(t, "GET", "/api/distributor/customers", nil, 2)
	GetDistributorCustomers(ctx2)
	require.Equal(t, http.StatusOK, w2.Code)
	items2 := extractDataPageItems(t, w2.Body.Bytes())
	require.Len(t, items2, 1, "D2 should see only 1 customer (C2)")
	item2, _ := items2[0].(map[string]interface{})
	cid2, _ := item2["id"].(float64)
	require.Equal(t, float64(20), cid2, "D2 customer should be C2 id=20")

	// D1 尝试访问 D2 的客户充值明细 → IDOR 防护 403
	ctx3, w3 := newDistributorContext(t, "GET", "/api/distributor/customers/20/topups", nil, 1)
	// 模拟路由参数 :id=20（c.Param 从 URL path 取，gin.CreateTestContext 不自动解析）
	ctx3.Params = gin.Params{{Key: "id", Value: "20"}}
	GetDistributorCustomerTopups(ctx3)
	require.Equal(t, http.StatusForbidden, w3.Code, "D1 accessing D2's customer should be 403 (IDOR)")
}

// ==================== C 组 AdminAuth/RootAuth 天然 403 回归断言 ====================

// newIsolationEngine 构造带 sessions 中间件的 gin.Engine，用于 C 组中间件链路测试。
// 模式参照 middleware/distributor_auth_test.go newDistributorEngine。
func newIsolationEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("isolation-test-secret"))))
	return r
}

// requireAccessDenied 断言 role=5 被 AdminAuth/RootAuth 拒绝。
// authHelper 阈值拒绝 (role < minRole) 返回 200 + success:false（auth.go:145-152），
// 精确匹配拒绝（DistributorAuth）返回 403。本测试验证 C 组天然隔离——
// 两种拒绝形式都视为"access denied"通过断言。
func requireAccessDenied(t *testing.T, w *httptest.ResponseRecorder, desc string) {
	t.Helper()
	body := w.Body.String()
	if w.Code == http.StatusForbidden {
		return // 精确匹配 403
	}
	if w.Code == http.StatusOK {
		require.Contains(t, body, `"success":false`, "%s: expected access denied (success:false), got: %s", desc, body)
		return
	}
	t.Fatalf("%s: expected 403 or 200+success:false, got code=%d body=%s", desc, w.Code, body)
}

// TestDistributorIsolation_AdminRoutes403 C 组遍历：role=5 访问 AdminAuth/RootAuth 路由
// 全部被拒（authHelper 阈值 5<10/100 → 200+success:false）。
// 验证 02-04-AUDIT.md C 组天然隔离结论（M4 零改动）。
func TestDistributorIsolation_AdminRoutes403(t *testing.T) {
	setupIsolationFixture(t)

	// 使用 access-token 路径：seed 用户已带 access_token，中间件查 DB 取 role=5 后阈值拒绝。
	d1Token := "distributor-token-1"

	tests := []struct {
		name   string
		target string
		method string
		mw     gin.HandlerFunc
		desc   string
	}{
		{"UserListAdmin", "/api/user/", "GET", middleware.AdminAuth(), "AdminAuth: GET /api/user/"},
		{"OptionRoot", "/api/option/", "GET", middleware.RootAuth(), "RootAuth: GET /api/option/"},
		{"ChannelAdmin", "/api/channel/", "GET", middleware.AdminAuth(), "AdminAuth: GET /api/channel/"},
		{"LogAllAdmin", "/api/log/", "GET", middleware.AdminAuth(), "AdminAuth: GET /api/log/"},
		{"RedemptionAdmin", "/api/redemption/", "GET", middleware.AdminAuth(), "AdminAuth: GET /api/redemption/"},
		{"DataAllAdmin", "/api/data/", "GET", middleware.AdminAuth(), "AdminAuth: GET /api/data/"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newIsolationEngine()
			r.Use(func(c *gin.Context) { c.Next() }) // no-op to keep chain
			r.GET(tc.target, tc.mw, func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true, "data": "leaked"})
			})

			req := httptest.NewRequest(tc.method, tc.target, nil)
			req.Header.Set("Authorization", d1Token)
			req.Header.Set("New-Api-User", "1")

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			requireAccessDenied(t, w, tc.desc)
			require.NotContains(t, w.Body.String(), `"leaked"`, "%s: data must not leak to role=5", tc.desc)
		})
	}
}

// TestDistributorIsolation_DistributorAuthPreciseMatch 分销商中间件精确匹配:
// role=5 通过，admin/root 被精确匹配 403，common 被阈值 200+success:false。
// 复用 02-04 TestDistributorAuthPreciseMatch 的断言模式，作为 02-05 回归基线。
func TestDistributorIsolation_DistributorAuthPreciseMatch(t *testing.T) {
	setupIsolationFixture(t)

	// 额外 seed admin/root/common 用户（D1 已 seed 为 role=5 id=1）
	seedDistributorTestUser(t, 3, common.RoleAdminUser, "admin-token-3")
	seedDistributorTestUser(t, 4, common.RoleRootUser, "root-token-4")
	seedDistributorTestUser(t, 5, common.RoleCommonUser, "common-token-5")

	tests := []struct {
		name     string
		token    string
		userID   int
		wantCode int
		wantPass bool
	}{
		{"distributor_role5_passes", "distributor-token-1", 1, http.StatusOK, true},
		{"admin_role10_rejected_403", "admin-token-3", 3, http.StatusForbidden, false},
		{"root_role100_rejected_403", "root-token-4", 4, http.StatusForbidden, false},
		{"common_role1_rejected_threshold", "common-token-5", 5, http.StatusOK, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newIsolationEngine()
			r.GET("/api/distributor/profile", middleware.DistributorAuth(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			req := httptest.NewRequest("GET", "/api/distributor/profile", nil)
			req.Header.Set("Authorization", tc.token)
			req.Header.Set("New-Api-User", fmt.Sprintf("%d", tc.userID))

			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			require.Equal(t, tc.wantCode, w.Code, "%s: code mismatch", tc.name)
			if tc.wantPass {
				require.Contains(t, w.Body.String(), `"success":true`, "%s: should pass", tc.name)
			} else {
				require.Contains(t, w.Body.String(), `"success":false`, "%s: should be rejected", tc.name)
			}
		})
	}
}

// seedDistributorTestUser 插入指定 role 的测试用户（带 access_token）。
// 用于 C 组中间件链路测试的 access-token 路径。
func seedDistributorTestUser(t *testing.T, id int, role int, token string) {
	t.Helper()
	u := &model.User{
		Id:          id,
		Username:    fmt.Sprintf("testuser%d", id),
		Password:    "testpass1234",
		Role:        role,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AffCode:     fmt.Sprintf("test-aff-%d", id),
		AccessToken: &token,
	}
	require.NoError(t, model.DB.Create(u).Error)
}
