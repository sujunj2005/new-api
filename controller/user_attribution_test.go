package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupAttributionTestDB 初始化归属管理测试 DB（参照 token_test.go setupTokenControllerTestDB 模式）。
// SQLite 内存库（shared cache + _busy_timeout 防 "database is locked"，不限制连接数避免事务嵌套死锁）。
func setupAttributionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	// 不限制 MaxOpenConns：事务内嵌套查询需要多连接，SetMaxOpenConns(1) 会死锁
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.AttributionChange{}))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// TestRebindWindow 验证 CanRebind 纯函数（契约 §2.4 补绑窗口设置键）。
// 覆盖：days 模式窗口内/超窗口、unlimited 模式任意时间、未知 mode 防御性 false、days<=0 防御性 false。
func TestRebindWindow(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)

	// days 模式 + created_at 3 天前 + days=7 → true（窗口内）
	require.True(t, model.CanRebind(now-3*daySec, "days", 7))
	// days 模式 + 30 天前 + days=7 → false（超窗口）
	require.False(t, model.CanRebind(now-30*daySec, "days", 7))
	// unlimited 模式 + 任意时间 → true（仅受 inviter_id=0 约束，窗口恒通过）
	require.True(t, model.CanRebind(now-30*daySec, "unlimited", 7))
	require.True(t, model.CanRebind(0, "unlimited", 7))
	// 未知 mode → false（防御性，不应出现于正常配置）
	require.False(t, model.CanRebind(now, "invalid", 7))
	// days <= 0 → false（防御性）
	require.False(t, model.CanRebind(now, "days", 0))
}

// TestRegisterAff 验证注册带 aff_code 时 InviterId 正确写入（M3，现有路径零改动，Pitfall 9）。
// 复用现有 GetUserIdByAffCode → InviterId 路径（controller/user.go:217-224），仅补测试覆盖。
func TestRegisterAff(t *testing.T) {
	setupAttributionTestDB(t)

	// 临时启用注册（恢复原始值，避免污染其他测试）
	originalRegister := common.RegisterEnabled
	originalPasswordRegister := common.PasswordRegisterEnabled
	originalGenerateToken := constant.GenerateDefaultToken
	common.RegisterEnabled = true
	common.PasswordRegisterEnabled = true
	constant.GenerateDefaultToken = false // 避免 Register 尝试创建 Token（Token 表未迁移）
	t.Cleanup(func() {
		common.RegisterEnabled = originalRegister
		common.PasswordRegisterEnabled = originalPasswordRegister
		constant.GenerateDefaultToken = originalGenerateToken
	})

	// 构造一个分销商用户（有 aff_code）
	distributor := model.User{
		Username: "distributor1",
		Password: "password123",
		Role:     common.RoleDistributorUser,
	}
	require.NoError(t, distributor.Insert(0))
	// Insert 会用随机串覆盖 AffCode（model/user.go:398），测试场景需要一个已知码
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", distributor.Id).Update("aff_code", "DIST").Error)

	// 构造注册请求带 aff_code
	regBody, _ := json.Marshal(map[string]string{
		"username": "newcustomer",
		"password": "password123",
		"aff_code": "DIST",
	})
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("POST", "/api/user/register", bytes.NewReader(regBody))
	ctx.Request.Header.Set("Content-Type", "application/json")

	Register(ctx)

	require.Equal(t, 200, w.Code, "register should return 200")
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	require.True(t, resp["success"].(bool), "register should succeed, got: %v", resp)

	// 断言新用户 InviterId == 分销商 id（ATTR-01 归属写入验证）
	var newCustomer model.User
	require.NoError(t, model.DB.Where("username = ?", "newcustomer").First(&newCustomer).Error)
	require.Equal(t, distributor.Id, newCustomer.InviterId, "InviterId should equal distributor id after register with aff_code")
}

// TestAffRebind 六分支完整实现在 attribution_test.go（02-05 Task 2）:
//   - TestAffRebind_WindowPass / WindowExpired / AlreadyBound / UnlimitedMode / InvalidCode / NotDistributor
// 契约 §4.2 B1 + §5 幂等与并发约定（条件 UPDATE inviter_id=0 + 窗口校验先行）。
// 02-03 此处为 t.Skip 骨架，02-05 已迁移至 attribution_test.go 完整实现。
