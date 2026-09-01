package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupDistributorTestDB initializes an in-memory SQLite database for DistributorAuth tests.
// 模式参照 controller/token_test.go setupTokenControllerTestDB + 02-RESEARCH.md Code Examples.
// Pitfall 8: SQLite 无 FOR UPDATE，SetMaxOpenConns(1) 防 "database is locked"。
func setupDistributorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	// AutoMigrate User 表：ValidateAccessToken 查 users.access_token
	require.NoError(t, db.AutoMigrate(&model.User{}))
	return db
}

// seedDistributorUser inserts a user with given role + access_token into DB.
// 用于 TestDistributorAuthPreciseMatch 的 access-token 路径：authHelper 调
// model.ValidateAccessToken(token) 查 DB 取 user，故须先 seed。
func seedDistributorUser(t *testing.T, db *gorm.DB, id int, role int, accessToken string) {
	t.Helper()
	token := accessToken
	user := &model.User{
		Id:          id,
		Username:    fmt.Sprintf("user%d", id),
		Password:    "testpass1234",
		Role:        role,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AccessToken: &token,
		AffCode:     fmt.Sprintf("aff-%d", id), // uniqueIndex 约束（user.go:44）
	}
	require.NoError(t, db.Create(user).Error)
}

// newDistributorEngine builds a gin.Engine with sessions middleware registered.
// authHelper (auth.go:38) calls sessions.Default(c) which requires the sessions
// middleware to have run; without it the context panics with "Key ... does not
// exist". Pattern copied from middleware/header_nav_test.go:40-42.
func newDistributorEngine() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("distributor-auth-test"))))
	return r
}

// newDistributorContext builds a gin.Context with id + role=5 preset directly
// (bypasses authHelper). Used by 02-05 controller tests that invoke handler
// functions directly without the middleware chain. Kept here as the canonical
// helper per 02-RESEARCH.md Code Examples; controller package re-declares its
// own local copy (different package, cannot import test helpers).
func newDistributorContext(t *testing.T, method, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	ctx.Set("id", userID)
	ctx.Set("role", common.RoleDistributorUser) // DistributorAuth 从这里读
	return ctx, recorder
}

// TestIsValidateRoleAcceptsDistributor verifies IsValidateRole's role=5 whitelist
// inclusion (Pitfall 1: 漏改 IsValidateRole → role=5 用户 session 登录被拒).
// 02-01 Task 1 已扩展 IsValidateRole 含 RoleDistributorUser（constants.go:208-211）.
func TestIsValidateRoleAcceptsDistributor(t *testing.T) {
	require.True(t, common.IsValidateRole(common.RoleDistributorUser), "role=5 must be in whitelist (login gate)")
	require.True(t, common.IsValidateRole(common.RoleCommonUser), "role=1 must still pass")
	require.True(t, common.IsValidateRole(common.RoleAdminUser), "role=10 must pass")
	require.True(t, common.IsValidateRole(common.RoleRootUser), "role=100 must pass")
	require.True(t, common.IsValidateRole(common.RoleGuestUser), "role=0 must pass")
	require.False(t, common.IsValidateRole(2), "role=2 must be rejected (invalid value)")
	require.False(t, common.IsValidateRole(7), "role=7 must be rejected (whitelist bypass defense)")
	require.False(t, common.IsValidateRole(-1), "negative role must be rejected")
}

// TestDistributorAuthPreciseMatch verifies DistributorAuth's precise match semantics
// (契约 §2.2):
//   - role=5 通过（authHelper 阈值 5>=5 + 精确匹配 5==5 都通过）
//   - role=10/admin 被精确匹配拦截 403（authHelper 阈值通过，但 10!=5）
//   - role=100/root 被精确匹配拦截 403
//   - role=1/common 被阈值拦截（authHelper: 1 < minRole=5 → 200 + Abort）
//
// authHelper 阈值拒绝点: auth.go:132 `if role.(int) < minRole`
// DistributorAuth 精确匹配拒绝点: auth.go:216 `if c.GetInt("role") != common.RoleDistributorUser`
//
// 测试走 access-token 路径（authHelper 在 session 为空时走 Authorization header →
// model.ValidateAccessToken 查 DB）。session middleware 必须注册（authHelper:38
// sessions.Default(c) 需要 session manager 存在，否则 panic）。
func TestDistributorAuthPreciseMatch(t *testing.T) {
	setupDistributorTestDB(t)

	tests := []struct {
		name        string
		userID      int
		role        int
		wantPass    bool
		wantCode    int
		threshold   bool // true=被 authHelper 阈值拒绝(200), false=被精确匹配拒绝(403) 或通过
		description string
	}{
		{
			name:        "distributor_role5_passes",
			userID:      1,
			role:        common.RoleDistributorUser,
			wantPass:    true,
			wantCode:    http.StatusOK,
			threshold:   false,
			description: "role=5 should pass DistributorAuth (threshold + precise match)",
		},
		{
			name:        "admin_role10_rejected_by_precise_match",
			userID:      2,
			role:        common.RoleAdminUser,
			wantPass:    false,
			wantCode:    http.StatusForbidden,
			threshold:   false,
			description: "admin(10) passes threshold but rejected by precise match (10!=5) → 403",
		},
		{
			name:        "root_role100_rejected_by_precise_match",
			userID:      3,
			role:        common.RoleRootUser,
			wantPass:    false,
			wantCode:    http.StatusForbidden,
			threshold:   false,
			description: "root(100) passes threshold but rejected by precise match (100!=5) → 403",
		},
		{
			name:        "common_role1_rejected_by_threshold",
			userID:      4,
			role:        common.RoleCommonUser,
			wantPass:    false,
			wantCode:    http.StatusOK,
			threshold:   true,
			description: "common(1) rejected by authHelper threshold (1<5) → 200 + error body",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			token := fmt.Sprintf("tok-%s", tc.name)
			seedDistributorUser(t, model.DB, tc.userID, tc.role, token)

			r := newDistributorEngine()
			r.GET("/api/distributor/profile", DistributorAuth(), func(c *gin.Context) {
				c.JSON(http.StatusOK, gin.H{"success": true})
			})

			req := httptest.NewRequest("GET", "/api/distributor/profile", nil)
			req.Header.Set("Authorization", token)
			req.Header.Set("New-Api-User", fmt.Sprintf("%d", tc.userID))

			recorder := httptest.NewRecorder()
			r.ServeHTTP(recorder, req)

			if tc.wantPass {
				require.Equal(t, http.StatusOK, recorder.Code, tc.description)
				require.Contains(t, recorder.Body.String(), `"success":true`, tc.description)
			} else {
				require.Equal(t, tc.wantCode, recorder.Code, tc.description)
				if tc.threshold {
					// authHelper 阈值拒绝：StatusOK(200) + success:false body
					require.Equal(t, http.StatusOK, recorder.Code, "threshold rejection should be 200")
					require.Contains(t, recorder.Body.String(), `"success":false`, tc.description)
				} else {
					// DistributorAuth 精确匹配拒绝：StatusForbidden(403)
					require.Equal(t, http.StatusForbidden, recorder.Code, "precise match rejection should be 403")
				}
			}
		})
	}
}
