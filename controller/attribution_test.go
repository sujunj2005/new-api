package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本文件为 02-05 Task 2 补绑场景自证测试。
// 契约依据：01-CONTRACT.md §4.2 B1/B2 + §5 幂等与并发约定。
//
// 六分支 TestAffRebind（B1 自助补绑）:
//   1. WindowPass      — days 模式窗口内 → 200 成功
//   2. WindowExpired   — days 模式超期 → 403 拒绝
//   3. AlreadyBound    — 已有归属 → 400 拒绝（条件 UPDATE affected=0）
//   4. UnlimitedMode   — unlimited 模式任意时间 → 200 成功
//   5. InvalidCode     — aff_code 不存在 → 400 拒绝
//   6. NotDistributor  — aff_code 属于普通用户 → 400 拒绝
//
// 两场景 TestAdminBind（B2 管理员补绑，F5 无条件覆盖）:
//   1. OverwriteExisting      — 无条件覆盖已有归属 + 审计 source=admin_bind
//   2. NotDistributorTarget   — 目标非分销商 → 400 拒绝
//
// TestRegisterAff 完整实现在 user_attribution_test.go（02-03 已实现，02-05 确认绿）。
// 本文件与 user_attribution_test.go 同属 package controller，共享 setupAttributionTestDB helper。

// rebindFixture 封装补绑测试 fixture 的常用实体。
type rebindFixture struct {
	distributor *model.User // role=5 分销商
	customer    *model.User // role=1 客户（inviter_id=0）
}

// setupRebindFixture 初始化补绑测试 DB + 一个分销商（affCode 可配）+ 一个未绑定客户。
// 调用者通过参数控制 window mode/days 与客户注册时间，覆盖六分支。
// 模式参照 user_attribution_test.go setupAttributionTestDB + 02-RESEARCH.md Code Examples。
func setupRebindFixture(t *testing.T, mode string, days int, customerCreatedAt int64) rebindFixture {
	t.Helper()
	setupAttributionTestDB(t)

	// 设置补绑窗口（CanRebind 从 common.AffRebindWindowMode/Days 读取）
	origMode := common.AffRebindWindowMode
	origDays := common.AffRebindWindowDays
	common.AffRebindWindowMode = mode
	common.AffRebindWindowDays = days
	t.Cleanup(func() {
		common.AffRebindWindowMode = origMode
		common.AffRebindWindowDays = origDays
	})

	// 创建分销商（role=5, aff_code="D1AFF"）
	d1 := &model.User{
		Username: "d1rebind",
		Password: "testpass1234",
		Role:     common.RoleDistributorUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, d1.Insert(0))
	// Insert 会用随机串覆盖 AffCode，测试场景需已知码，直接 DB.Update
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", d1.Id).Update("aff_code", "D1AFF").Error)

	// 创建未绑定客户（role=1, inviter_id=0, created_at=customerCreatedAt）
	c1 := &model.User{
		Username: "c1rebind",
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, c1.Insert(0))
	// 设置 CreatedAt（Insert 用 autoCreateTime 覆盖，须事后 Update）
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", c1.Id).Update("created_at", customerCreatedAt).Error)

	return rebindFixture{distributor: d1, customer: c1}
}

// callAffRebind 构造 POST /api/user/aff_rebind 请求并调用 AffRebind handler。
// 返回响应 code + body + 解析后的 success/message。
func callAffRebind(t *testing.T, userID int, affCode string) (int, []byte, bool, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"aff_code": affCode})
	w := setupAffRebindRecorder(t, userID, body)
	resp := struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, w.Body.Bytes(), resp.Success, resp.Message
}

// setupAffRebindRecorder 构造 AffRebind 调用的 recorder + context。
func setupAffRebindRecorder(t *testing.T, userID int, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/api/user/aff_rebind", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", userID)
	ctx.Set("role", common.RoleCommonUser)
	AffRebind(ctx)
	return recorder
}

// callAdminBind 构造 POST /api/attribution/bind 请求并调用 AdminBindAttribution handler。
// 返回响应 code + body + 解析后的 success/message。
func callAdminBind(t *testing.T, operatorID, targetUserID, distributorID int, reason string) (int, []byte, bool, string) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{
		"user_id":        targetUserID,
		"distributor_id": distributorID,
		"reason":         reason,
	})
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest("POST", "/api/attribution/bind", bytes.NewReader(body))
	ctx.Request.Header.Set("Content-Type", "application/json")
	ctx.Set("id", operatorID)
	ctx.Set("role", common.RoleAdminUser)
	AdminBindAttribution(ctx)
	resp := struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}{}
	_ = json.Unmarshal(recorder.Body.Bytes(), &resp)
	return recorder.Code, recorder.Body.Bytes(), resp.Success, resp.Message
}

// requireAttributionChangeExists 断言 AttributionChange 表中存在匹配条件的记录。
func requireAttributionChangeExists(t *testing.T, userID int, source string, oldInviter, newInviter int) {
	t.Helper()
	var changes []model.AttributionChange
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", userID, source).Find(&changes).Error)
	require.NotEmpty(t, changes, "expected AttributionChange{user_id=%d, source=%s} to exist", userID, source)
	var matched bool
	for _, ch := range changes {
		if ch.OldInviterId == oldInviter && ch.NewInviterId == newInviter {
			matched = true
			break
		}
	}
	require.True(t, matched, "expected AttributionChange{old=%d, new=%d, source=%s} in %v", oldInviter, newInviter, source, changes)
}

// ==================== TestAffRebind 六分支 ====================

// TestAffRebind_WindowPass (a) days 模式窗口内补绑成功。
func TestAffRebind_WindowPass(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	f := setupRebindFixture(t, "days", 7, now-3*daySec) // 3 天前，窗口内

	code, _, success, _ := callAffRebind(t, f.customer.Id, "D1AFF")
	require.Equal(t, http.StatusOK, code, "window-pass rebind should return 200")
	require.True(t, success, "window-pass rebind should succeed")

	// 断言客户已绑定到分销商
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Equal(t, f.distributor.Id, reloaded.InviterId, "customer inviter_id should be distributor id")

	// 断言审计行写入（source=self_bind, operator=customer）
	requireAttributionChangeExists(t, f.customer.Id, "self_bind", 0, f.distributor.Id)
}

// TestAffRebind_WindowExpired (b) days 模式超期拒绝。
func TestAffRebind_WindowExpired(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	f := setupRebindFixture(t, "days", 7, now-30*daySec) // 30 天前，超 7 天窗口

	code, _, success, _ := callAffRebind(t, f.customer.Id, "D1AFF")
	require.Equal(t, http.StatusForbidden, code, "expired rebind should return 403")
	require.False(t, success, "expired rebind should fail")

	// 断言客户仍未绑定
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Zero(t, reloaded.InviterId, "customer inviter_id should remain 0 after rejection")
}

// TestAffRebind_AlreadyBound (d) 已有归属拒绝（条件 UPDATE affected=0）。
// 契约 §5：条件 UPDATE inviter_id=0，affected=0 → ErrAlreadyBound 400。
func TestAffRebind_AlreadyBound(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	f := setupRebindFixture(t, "days", 7, now-3*daySec)

	// 预置客户已有归属（inviter_id = distributor.Id）
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", f.customer.Id).Update("inviter_id", f.distributor.Id).Error)

	code, _, success, _ := callAffRebind(t, f.customer.Id, "D1AFF")
	require.Equal(t, http.StatusBadRequest, code, "already-bound rebind should return 400")
	require.False(t, success, "already-bound rebind should fail")

	// 断言 inviter_id 仍为原分销商（未被覆盖）
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Equal(t, f.distributor.Id, reloaded.InviterId, "customer inviter_id should remain distributor id")
}

// TestAffRebind_UnlimitedMode (c) unlimited 模式不限时可绑。
func TestAffRebind_UnlimitedMode(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	// 365 天前，超 days 窗口但 unlimited 模式允许
	f := setupRebindFixture(t, "unlimited", 7, now-365*daySec)

	code, _, success, _ := callAffRebind(t, f.customer.Id, "D1AFF")
	require.Equal(t, http.StatusOK, code, "unlimited-mode rebind should return 200")
	require.True(t, success, "unlimited-mode rebind should succeed")

	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Equal(t, f.distributor.Id, reloaded.InviterId, "customer inviter_id should be distributor id")
}

// TestAffRebind_InvalidCode (e) aff_code 不存在 → 400。
func TestAffRebind_InvalidCode(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	f := setupRebindFixture(t, "days", 7, now-3*daySec)

	code, _, success, _ := callAffRebind(t, f.customer.Id, "NOTEXIST")
	require.Equal(t, http.StatusBadRequest, code, "invalid aff_code should return 400")
	require.False(t, success, "invalid aff_code should fail")

	// 断言客户未绑定
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Zero(t, reloaded.InviterId, "customer inviter_id should remain 0")
}

// TestAffRebind_NotDistributor (f) aff_code 属于普通用户（role=1）→ 400。
// 防止绑定到普通邀请人冒充分销商。
func TestAffRebind_NotDistributor(t *testing.T) {
	now := common.GetTimestamp()
	daySec := int64(86400)
	f := setupRebindFixture(t, "days", 7, now-3*daySec)

	// 创建普通用户 U2（role=1, aff_code="U2AFF"）
	u2 := &model.User{
		Username: "u2notdistributor",
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, u2.Insert(0))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", u2.Id).Update("aff_code", "U2AFF").Error)

	code, _, success, _ := callAffRebind(t, f.customer.Id, "U2AFF")
	require.Equal(t, http.StatusBadRequest, code, "non-distributor aff_code should return 400")
	require.False(t, success, "non-distributor aff_code should fail")

	// 断言客户未绑定
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, f.customer.Id).Error)
	require.Zero(t, reloaded.InviterId, "customer inviter_id should remain 0")
}

// ==================== TestAdminBind 两场景 ====================

// setupAdminBindFixture 初始化管理员补绑 fixture:
//   - 分销商 D1 (role=5, aff_code="D1AFF")
//   - 分销商 D2 (role=5, aff_code="D2AFF")
//   - 客户 C1 (role=1, inviter_id 预置为 d1Id)
//   - 操作者 admin (role=10)
func setupAdminBindFixture(t *testing.T) (d1, d2, c1, admin *model.User) {
	t.Helper()
	setupAttributionTestDB(t)

	d1 = &model.User{
		Username: "d1admin",
		Password: "testpass1234",
		Role:     common.RoleDistributorUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, d1.Insert(0))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", d1.Id).Update("aff_code", "D1AFF").Error)

	d2 = &model.User{
		Username: "d2admin",
		Password: "testpass1234",
		Role:     common.RoleDistributorUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, d2.Insert(0))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", d2.Id).Update("aff_code", "D2AFF").Error)

	c1 = &model.User{
		Username: "c1admin",
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		InviterId: d1.Id, // 预置已有归属
	}
	require.NoError(t, c1.Insert(0))
	// Insert 不写 InviterId（finishInsert 不动 inviter_id 字段），手动设置
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", c1.Id).Update("inviter_id", d1.Id).Error)

	admin = &model.User{
		Username: "adminop",
		Password: "testpass1234",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, admin.Insert(0))

	return d1, d2, c1, admin
}

// TestAdminBind_OverwriteExisting F5 无条件覆盖已有归属 + 审计 source=admin_bind。
// 契约 §5 F5：管理员改绑不受 inviter_id=0 约束（与自助补绑物理隔离）。
func TestAdminBind_OverwriteExisting(t *testing.T) {
	d1, d2, c1, admin := setupAdminBindFixture(t)

	code, _, success, _ := callAdminBind(t, admin.Id, c1.Id, d2.Id, "correction")
	require.Equal(t, http.StatusOK, code, "admin overwrite should return 200")
	require.True(t, success, "admin overwrite should succeed")

	// 断言客户归属已从 D1 覆盖到 D2（无条件，F5）
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, c1.Id).Error)
	require.Equal(t, d2.Id, reloaded.InviterId, "customer inviter_id should be overwritten to D2")

	// 断言审计行写入（source=admin_bind, old=d1.id, new=d2.id, reason=correction）
	requireAttributionChangeExists(t, c1.Id, "admin_bind", d1.Id, d2.Id)
	var changes []model.AttributionChange
	require.NoError(t, model.DB.Where("user_id = ? AND source = ?", c1.Id, "admin_bind").Find(&changes).Error)
	require.NotEmpty(t, changes)
	require.Equal(t, "correction", changes[0].Reason, "audit reason should be 'correction'")
	require.Equal(t, admin.Id, changes[0].OperatorId, "audit operator_id should be admin id")
}

// TestAdminBind_NotDistributorTarget 目标非分销商 → 400 拒绝。
func TestAdminBind_NotDistributorTarget(t *testing.T) {
	_, _, c1, admin := setupAdminBindFixture(t)

	// 创建普通用户 U2（role=1）作为目标 distributor_id
	u2 := &model.User{
		Username: "u2nondisttarget",
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, u2.Insert(0))

	code, _, success, _ := callAdminBind(t, admin.Id, c1.Id, u2.Id, "try non-distributor target")
	require.Equal(t, http.StatusBadRequest, code, "non-distributor target should return 400")
	require.False(t, success, "non-distributor target should fail")

	// 断言客户归属未变（c1.InviterId 仍为预置的 d1.Id）
	var reloaded model.User
	require.NoError(t, model.DB.First(&reloaded, c1.Id).Error)
	// setupAdminBindFixture 预置 c1.InviterId=d1.Id；失败调用不应改动
	// （注：c1.Id 是 Insert 后的真实 id，InviterId 在 setupAdminBindFixture 末尾被 Update 为 d1.Id）
	require.Equal(t, c1.InviterId, reloaded.InviterId, "customer inviter_id should be unchanged after rejection")
}

// ==================== TestAdminDistributorCustomers（B7，plan 6.1 G-1） ====================

// b7RowShape B7 行形状 = B4 distributorCustomerItem 冻结 json tag（契约附录 G：逐字一致）。
var b7RowShape = []string{"id", "username", "display_name", "created_at", "total_topup_cents", "total_commission_cents"}

// b7Page / b7Envelope B7 PageInfo 信封解析（items 保留原始 map 以断言行形状键集）。
type b7Page struct {
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
	Total    int              `json:"total"`
	Items    []map[string]any `json:"items"`
}

type b7Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    b7Page `json:"data"`
}

// seedB7Customer 直插一名归属客户（Create 不写 InviterId，事后 Update——setupAdminBindFixture 先例）。
func seedB7Customer(t *testing.T, username string, inviterId int) *model.User {
	t.Helper()
	u := &model.User{
		Username: username,
		Password: "testpass1234",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		AffCode:  "B7" + username,
	}
	require.NoError(t, model.DB.Create(u).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", u.Id).Update("inviter_id", inviterId).Error)
	return u
}

// seedB7Topup 直插一张成功充值单（money 为 float 元，B4 聚合口径 SUM(money)*100）。
func seedB7Topup(t *testing.T, userId int, tradeNo string, money float64) {
	t.Helper()
	require.NoError(t, model.DB.Create(&model.TopUp{
		UserId:  userId,
		Money:   money,
		TradeNo: tradeNo,
		Status:  common.TopUpStatusSuccess,
	}).Error)
}

// newB7TestRouter gin test router：与 api-router.go 相同的 AdminAuth 挂载形态（B7）。
func newB7TestRouter() *gin.Engine {
	r := gin.New()
	r.Use(sessions.Sessions("session", cookie.NewStore([]byte("b7-test-secret"))))
	r.GET("/api/attribution/distributor/:id/customers", middleware.AdminAuth(), GetDistributorCustomersForAdmin)
	return r
}

// b7RowByUsername 从 items 中取指定 username 的行。
func b7RowByUsername(t *testing.T, items []map[string]any, username string) map[string]any {
	t.Helper()
	for _, it := range items {
		if it["username"] == username {
			return it
		}
	}
	t.Fatalf("row %q not found in items: %v", username, items)
	return nil
}

// b7Num 取行内数值字段（JSON 反序列化为 float64）。
func b7Num(t *testing.T, row map[string]any, key string) float64 {
	t.Helper()
	v, ok := row[key].(float64)
	require.True(t, ok, "row key %q must be numeric, got %v", key, row[key])
	return v
}

// TestAdminDistributorCustomers B7 管理员查看分销商客户列表（plan 6.1 G-1，契约附录 G）。
// admin 会话 → 返回指定分销商客户行（total_topup_cents/total_commission_cents 聚合值
// 与 B4 同口径：SUM(money)*100 / SUM(amount_cents) 双谓词）；
// role=5 → 拒绝（多形态断言，OQ-4 口径）；分页参数生效；:id 非数字 400。
func TestAdminDistributorCustomers(t *testing.T) {
	setupStatementQueryTestDB(t)

	admin := seedStmtUserWithToken(t, "b7admin", common.RoleAdminUser, "b7-admin-token")
	dist := seedStmtUserWithToken(t, "b7dist", common.RoleDistributorUser, "b7-dist-token")
	other := seedStmtUserWithToken(t, "b7other", common.RoleDistributorUser, "b7-other-token")

	c1 := seedB7Customer(t, "b7cust1", dist.Id)
	c2 := seedB7Customer(t, "b7cust2", dist.Id)
	c3 := seedB7Customer(t, "b7cust3", dist.Id)
	unbound := seedB7Customer(t, "b7unbound", 0)

	seedB7Topup(t, c1.Id, "B7-TP-1", 100)     // → 10000 cents
	seedB7Topup(t, c3.Id, "B7-TP-3", 50.5)    // → 5050 cents
	seedB7Topup(t, unbound.Id, "B7-TP-U", 777) // 非本分销商客户，任何视图都不得出现
	seedStmtFlow(t, "B7-FL-1", model.CommissionFlowCommission, c1.Id, dist.Id, 500)
	// 跨分销商谓词隔离：other 名下指向同客户的流水不得计入 dist 视图
	seedStmtFlow(t, "B7-FL-DECOY", model.CommissionFlowCommission, c1.Id, other.Id, 999)

	r := newB7TestRouter()
	path := fmt.Sprintf("/api/attribution/distributor/%d/customers", dist.Id)

	t.Run("AdminViewsAggregatedRows", func(t *testing.T) {
		w := wdDo(r, "GET", path, "b7-admin-token", admin.Id, "")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp b7Envelope
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.True(t, resp.Success, "admin view must succeed, body: %s", w.Body.String())
		require.Equal(t, 3, resp.Data.Total, "only distributor-owned customers counted")
		require.Len(t, resp.Data.Items, 3)

		row := b7RowByUsername(t, resp.Data.Items, c1.Username)
		require.Equal(t, float64(c1.Id), b7Num(t, row, "id"))
		require.InDelta(t, 10000, b7Num(t, row, "total_topup_cents"), 0.0001)
		require.InDelta(t, 500, b7Num(t, row, "total_commission_cents"), 0.0001,
			"decoy flow of other distributor must not leak into aggregation")

		row2 := b7RowByUsername(t, resp.Data.Items, c2.Username)
		require.InDelta(t, 0, b7Num(t, row2, "total_topup_cents"), 0.0001)
		require.InDelta(t, 0, b7Num(t, row2, "total_commission_cents"), 0.0001)

		// 行形状与 B4 distributorCustomerItem 冻结 json tag 逐字一致（附录 G）
		keys := make([]string, 0, len(row))
		for k := range row {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		require.Equal(t, b7RowShape, keys)
	})

	t.Run("Pagination", func(t *testing.T) {
		w := wdDo(r, "GET", path+"?p=1&page_size=2", "b7-admin-token", admin.Id, "")
		require.Equal(t, http.StatusOK, w.Code, "body: %s", w.Body.String())
		var resp b7Envelope
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
		require.True(t, resp.Success)
		require.Equal(t, 3, resp.Data.Total)
		require.Len(t, resp.Data.Items, 2)
		// order id desc（B4 同口径）：第一页应为 c3/c2
		require.Equal(t, c3.Username, resp.Data.Items[0]["username"])
		require.Equal(t, c2.Username, resp.Data.Items[1]["username"])

		w2 := wdDo(r, "GET", path+"?p=2&page_size=2", "b7-admin-token", admin.Id, "")
		require.Equal(t, http.StatusOK, w2.Code)
		var resp2 b7Envelope
		require.NoError(t, json.Unmarshal(w2.Body.Bytes(), &resp2))
		require.True(t, resp2.Success)
		require.Len(t, resp2.Data.Items, 1)
		require.Equal(t, c1.Username, resp2.Data.Items[0]["username"])
	})

	t.Run("DistributorRejected", func(t *testing.T) {
		// role=5 无权访问 admin 端点（多形态拒绝断言，OQ-4 口径：
		// 阈值拒绝 200+success:false / 403 / 401 皆算拒——禁止数据泄漏是断言本体）
		w := wdDo(r, "GET", path, "b7-dist-token", dist.Id, "")
		wdRequireAuthRejection(t, w)
	})

	t.Run("InvalidDistributorId", func(t *testing.T) {
		w := wdDo(r, "GET", "/api/attribution/distributor/abc/customers", "b7-admin-token", admin.Id, "")
		require.Equal(t, http.StatusBadRequest, w.Code, "non-numeric :id must 400, body: %s", w.Body.String())
	})
}
