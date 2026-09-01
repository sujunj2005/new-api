package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupCommissionRateTestDB 初始化比例管理 controller 测试 DB
// （平移 setupAttributionTestDB 模式，02-03/02-04 死锁教训）：
// 内存 SQLite（shared cache + _busy_timeout=30000），禁止 SetMaxOpenConns(1)——
// A2 在 model.DB.Transaction 内嵌套查询，单连接必死锁。
// 相比先例补 Restore：t.Cleanup 先恢复全局 DB 再关闭本测试库
// （03-01 教训：后续测试不得拿到已关闭的连接）。
func setupCommissionRateTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	origDB, origLogDB := model.DB, model.LOG_DB
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.CommissionRate{}, &model.CommissionRateHistory{}))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = origDB, origLogDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// seedRateUser 建一个测试用户（直接 DB.Create，绕过 Insert 的随机 AffCode 覆盖；
// users.username/aff_code 均为 uniqueIndex，用前缀保证唯一）。
func seedRateUser(t *testing.T, username string, role int) *model.User {
	t.Helper()
	u := &model.User{
		Username: username,
		Password: "testpass1234",
		Role:     role,
		Status:   common.UserStatusEnabled,
		AffCode:  "RA" + username,
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// performSetRate 构造 gin test context 直调 SetCommissionRate（B 域测试模式）。
// operatorId 经 c.Set("id", ...) 模拟 session 注入；路径参数 :distributorId 直塞 ctx.Params。
func performSetRate(t *testing.T, operatorId int, distributorIdStr string, rateBp int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	body, err := json.Marshal(map[string]int{"rate_bp": rateBp})
	require.NoError(t, err)
	ctx.Request = httptest.NewRequest("PUT", "/api/commission/rates/"+distributorIdStr, bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", operatorId)
	ctx.Params = gin.Params{{Key: "distributorId", Value: distributorIdStr}}
	SetCommissionRate(ctx)
	return w
}

// performGetRates 直调 GetCommissionRates（A1）。
func performGetRates(t *testing.T) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/commission/rates?p=1&page_size=10", nil)
	GetCommissionRates(ctx)
	return w
}

// performGetRateHistory 直调 GetCommissionRateHistory（A3）。
func performGetRateHistory(t *testing.T, distributorId int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", fmt.Sprintf("/api/commission/rates/%d/history?p=1&page_size=10", distributorId), nil)
	ctx.Params = gin.Params{{Key: "distributorId", Value: strconv.Itoa(distributorId)}}
	GetCommissionRateHistory(ctx)
	return w

}

// parseRateEnvelope 解析 ApiSuccess/ApiError 信封。
func parseRateEnvelope(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// TestCommissionRateSetValidation A2 校验链全分支（RATE-01，契约 §4.3 A2）：
// rate_bp 越界（>10000 / <0）与目标非分销商（role=1/role=10）全部 success=false；
// 路径参数解析失败 → HTTP 400（distributor.go 先例）。
func TestCommissionRateSetValidation(t *testing.T) {
	setupCommissionRateTestDB(t)
	admin := seedRateUser(t, "rateadmin", common.RoleAdminUser)

	cases := []struct {
		name       string
		rateBp     int
		targetRole int
	}{
		{name: "rate_bp_over_upper_10001", rateBp: 10001, targetRole: common.RoleDistributorUser},
		{name: "rate_bp_negative", rateBp: -1, targetRole: common.RoleDistributorUser},
		{name: "target_role_1_not_distributor", rateBp: 500, targetRole: common.RoleCommonUser},
		{name: "target_role_10_admin_not_distributor", rateBp: 500, targetRole: common.RoleAdminUser},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			target := seedRateUser(t, "target_"+tc.name, tc.targetRole)
			w := performSetRate(t, admin.Id, strconv.Itoa(target.Id), tc.rateBp)
			require.Equal(t, 200, w.Code, "错误走信封 HTTP 200 + success=false")
			resp := parseRateEnvelope(t, w)
			require.False(t, resp["success"].(bool), "got: %v", resp)
		})
	}

	// 校验链第 1 分支：路径参数非数字 → HTTP 400（不走信封）
	w := performSetRate(t, admin.Id, "abc", 500)
	require.Equal(t, 400, w.Code, "无效分销商ID直接 400")
	// 校验链第 2 分支：请求体非法 JSON → HTTP 400
	w2 := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w2)
	ctx.Request = httptest.NewRequest("PUT", "/api/commission/rates/1", bytes.NewReader([]byte("{invalid")))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", admin.Id)
	ctx.Params = gin.Params{{Key: "distributorId", Value: "1"}}
	SetCommissionRate(ctx)
	require.Equal(t, 400, w2.Code, "非法 JSON 请求体直接 400")
}

// TestCommissionRateSetHappyPath A2 成功路径：rates 行 1 条 + histories 行 1 条
// （OperatorId/old/new 正确，RATE-03 审计留痕）。
func TestCommissionRateSetHappyPath(t *testing.T) {
	setupCommissionRateTestDB(t)
	admin := seedRateUser(t, "rateadmin", common.RoleAdminUser)
	target := seedRateUser(t, "disttarget", common.RoleDistributorUser)

	w := performSetRate(t, admin.Id, strconv.Itoa(target.Id), 500)
	resp := parseRateEnvelope(t, w)
	require.True(t, resp["success"].(bool), "got: %v", resp)

	var rates []model.CommissionRate
	require.NoError(t, model.DB.Find(&rates).Error)
	require.Len(t, rates, 1, "rates 恰好 1 行")
	require.Equal(t, target.Id, rates[0].DistributorId)
	require.Equal(t, 500, rates[0].RateBp)
	require.Equal(t, admin.Id, rates[0].UpdatedBy)

	var hists []model.CommissionRateHistory
	require.NoError(t, model.DB.Find(&hists).Error)
	require.Len(t, hists, 1, "histories 恰好 1 行")
	require.Equal(t, 0, hists[0].OldRateBp, "首次设置 old=0")
	require.Equal(t, 500, hists[0].NewRateBp)
	require.Equal(t, admin.Id, hists[0].OperatorId, "operator 取 session id")
}

// TestCommissionRateList A1：设置后列表 items 含 username 冗余且 rate_bp 正确（契约 §4.3 A1）。
func TestCommissionRateList(t *testing.T) {
	setupCommissionRateTestDB(t)
	admin := seedRateUser(t, "rateadmin", common.RoleAdminUser)
	dA := seedRateUser(t, "dist_list_a", common.RoleDistributorUser)
	dB := seedRateUser(t, "dist_list_b", common.RoleDistributorUser)
	seedRateUser(t, "plain_list_user", common.RoleCommonUser) // 无比例行 → 不入列表

	w1 := performSetRate(t, admin.Id, strconv.Itoa(dA.Id), 1000)
	require.True(t, parseRateEnvelope(t, w1)["success"].(bool))
	w2 := performSetRate(t, admin.Id, strconv.Itoa(dB.Id), 500)
	require.True(t, parseRateEnvelope(t, w2)["success"].(bool))

	w := performGetRates(t)
	require.Equal(t, 200, w.Code)
	resp := parseRateEnvelope(t, w)
	require.True(t, resp["success"].(bool), "got: %v", resp)
	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok, "data 为 PageInfo")
	require.Equal(t, float64(2), data["total"], "仅 2 行比例配置")
	items, ok := data["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 2)

	first := items[0].(map[string]interface{})
	require.Equal(t, "dist_list_a", first["username"], "A1 username 冗余（LEFT JOIN users）")
	require.Equal(t, float64(dA.Id), first["distributor_id"], "distributor_id ASC")
	require.Equal(t, float64(1000), first["rate_bp"])
	second := items[1].(map[string]interface{})
	require.Equal(t, "dist_list_b", second["username"])
	require.Equal(t, float64(500), second["rate_bp"])
}

// TestCommissionRateHistoryA3 A3：连续两次 A2（500→1000，不同 operator）后 history 2 行，
// old/new 链条 0→500→1000 正确（RATE-03，契约 §4.3 A3）。
func TestCommissionRateHistoryA3(t *testing.T) {
	setupCommissionRateTestDB(t)
	admin1 := seedRateUser(t, "rateadmin1", common.RoleAdminUser)
	admin2 := seedRateUser(t, "rateadmin2", common.RoleAdminUser)
	target := seedRateUser(t, "disttarget", common.RoleDistributorUser)

	w1 := performSetRate(t, admin1.Id, strconv.Itoa(target.Id), 500)
	require.True(t, parseRateEnvelope(t, w1)["success"].(bool))
	w2 := performSetRate(t, admin2.Id, strconv.Itoa(target.Id), 1000)
	require.True(t, parseRateEnvelope(t, w2)["success"].(bool))

	w := performGetRateHistory(t, target.Id)
	require.Equal(t, 200, w.Code)
	resp := parseRateEnvelope(t, w)
	require.True(t, resp["success"].(bool), "got: %v", resp)
	data, ok := resp["data"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(2), data["total"])
	items, ok := data["items"].([]interface{})
	require.True(t, ok)
	require.Len(t, items, 2)

	// id DESC：最新变更在前（500→1000，operator=admin2），最早在后（0→500，operator=admin1）
	first := items[0].(map[string]interface{})
	require.Equal(t, float64(500), first["old_rate_bp"])
	require.Equal(t, float64(1000), first["new_rate_bp"])
	require.Equal(t, float64(admin2.Id), first["operator_id"])
	second := items[1].(map[string]interface{})
	require.Equal(t, float64(0), second["old_rate_bp"])
	require.Equal(t, float64(500), second["new_rate_bp"])
	require.Equal(t, float64(admin1.Id), second["operator_id"])
	// 链条连续：前一条 new == 后一条 old
	require.Equal(t, second["new_rate_bp"], first["old_rate_bp"], "old/new 链条 0→500→1000")
}
