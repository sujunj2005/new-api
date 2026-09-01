package controller

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupDistributorTestDB initializes an in-memory SQLite DB for distributor-related
// controller tests. Local copy of middleware.setupDistributorTestDB (controller is
// a different package, cannot import test helpers across packages).
// 模式参照 controller/token_test.go setupTokenControllerTestDB + 02-RESEARCH.md Code Examples.
// SQLite shared cache + _busy_timeout 防 "database is locked"，不限制连接数避免事务嵌套死锁。
func setupDistributorTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	model.DB = db
	model.LOG_DB = db
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	// AutoMigrate User 表：UpdateUser 调 GetUserById/EditWithTx/InvalidateUserCache
	require.NoError(t, db.AutoMigrate(&model.User{}))
	return db
}

// newDistributorContext builds a gin.Context with id + role=5 preset directly
// (bypasses authHelper). Used by controller tests that invoke handler functions
// directly without the middleware chain. Per 02-RESEARCH.md Code Examples.
func newDistributorContext(t *testing.T, method, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	ctx.Set("id", userID)
	ctx.Set("role", common.RoleDistributorUser) // DistributorAuth 从这里读
	return ctx, recorder
}

// newAdminContext builds a gin.Context with id + role=10 (admin) preset.
// For 02-05 admin-side tests (UpdateUser role 变更, AdminBindAttribution, etc.).
func newAdminContext(t *testing.T, method, target string, body any, userID int) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(method, target, nil)
	ctx.Set("id", userID)
	ctx.Set("role", common.RoleAdminUser)
	return ctx, recorder
}

// TestUpdateUserRoleToDistributor is a skeleton test for the M1 role变更 路径
// (契约 §4.1 M1 + F3). Full implementation deferred to 02-05.
//
// 02-05 将填充:
//   - 管理员 context + PUT /api/user/ role=5 → 断言 DB 行 role==5
//   - 断言 InvalidateUserCache / InvalidateUserTokensCache 被调用
//   - 断言非法值 {2,7,...} 被拒绝 (isDistributorRoleValid, user.go:326-329)
//   - 断言 canManageTargetRole 双向校验 (user.go:681-689)
func TestUpdateUserRoleToDistributor(t *testing.T) {
	t.Skip("skeleton - full implementation in 02-05 TestDistributorIsolation suite")
}
