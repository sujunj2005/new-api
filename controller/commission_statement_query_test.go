package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/middleware"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// setupStatementQueryTestDB 初始化账单查询 controller 测试 DB
// （平移 setupCommissionRateTestDB 模式，02-03/02-04 死锁教训）：
// 内存 SQLite（shared cache + _busy_timeout=30000），禁止 SetMaxOpenConns(1)。
// AutoMigrate 覆盖 statement 三表 + users + commission_flows（B4 聚合）
// + top_ups（B4 total_topup_cents 聚合对象）+ logs（AdminAuth 审计链路兜底）。
// t.Cleanup 先恢复全局 DB 再关闭本测试库（03-01 教训）。
func setupStatementQueryTestDB(t *testing.T) *gorm.DB {
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
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.CommissionStatement{},
		&model.CommissionStatementItem{},
		&model.StatementAdjustment{},
		&model.CommissionFlow{},
		&model.TopUp{},
		&model.Log{},
	))
	t.Cleanup(func() {
		model.DB, model.LOG_DB = origDB, origLogDB
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
	return db
}

// seedStmtUser 建一个测试用户（直接 DB.Create 绕过 Insert 的随机 AffCode 覆盖；
// users.username/aff_code 均为 uniqueIndex，用前缀保证唯一）。
func seedStmtUser(t *testing.T, username string, role int) *model.User {
	t.Helper()
	u := &model.User{
		Username: username,
		Password: "testpass1234",
		Role:     role,
		Status:   common.UserStatusEnabled,
		AffCode:  "SA" + username,
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// seedStmtUserWithToken 建带 access_token 的测试用户（AdminAuth 中间件链路测试用：
// authenticateUser 经 ValidateAccessToken 查库取 role，请求头需 Authorization + New-Api-User）。
func seedStmtUserWithToken(t *testing.T, username string, role int, token string) *model.User {
	t.Helper()
	u := &model.User{
		Username:    username,
		Password:    "testpass1234",
		Role:        role,
		Status:      common.UserStatusEnabled,
		AffCode:     "SA" + username,
		AccessToken: &token,
	}
	require.NoError(t, model.DB.Create(u).Error)
	return u
}

// seedStmtRow 直插一张对账单（按契约 §3.3 形状；LockedAt 生成即锁定语义）。
func seedStmtRow(t *testing.T, distributorId int, period, status string, totalCommission, adjusted int64) *model.CommissionStatement {
	t.Helper()
	s := &model.CommissionStatement{
		DistributorId:        distributorId,
		Period:               period,
		TotalTopupCents:      (totalCommission - adjusted) * 10,
		TotalCommissionCents: totalCommission,
		AdjustedCents:        adjusted,
		SettleAmountCents:    totalCommission + adjusted,
		Status:               status,
		LockedAt:             common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(s).Error)
	return s
}

// seedStmtItem 直插一条账单明细。
func seedStmtItem(t *testing.T, statementId int64, billingNo, paymentMethod string, topupCents, commissionCents int64, customerId int) *model.CommissionStatementItem {
	t.Helper()
	item := &model.CommissionStatementItem{
		StatementId:     statementId,
		FlowId:          0,
		BillingNo:       billingNo,
		PaymentMethod:   paymentMethod,
		PaymentProvider: "epay",
		TopupMoneyCents: topupCents,
		CommissionCents: commissionCents,
		CustomerId:      customerId,
		CompleteTime:    common.GetTimestamp(),
	}
	require.NoError(t, model.DB.Create(item).Error)
	return item
}

// seedStmtAdjustment 直插一条调整单。
func seedStmtAdjustment(t *testing.T, statementId int64, deltaCents int64, reason string, operatorId int) *model.StatementAdjustment {
	t.Helper()
	adj := &model.StatementAdjustment{
		StatementId: statementId,
		DeltaCents:  deltaCents,
		Reason:      reason,
		OperatorId:  operatorId,
	}
	require.NoError(t, model.DB.Create(adj).Error)
	return adj
}

// seedStmtFlow 直插一条佣金流水（B4 聚合测试用；uk_flow_billing 用唯一 billingNo 兜底）。
func seedStmtFlow(t *testing.T, billingNo, flowType string, customerId, distributorId int, amountCents int64) {
	t.Helper()
	f := &model.CommissionFlow{
		BillingNo:     billingNo,
		FlowType:      flowType,
		CustomerId:    customerId,
		DistributorId: distributorId,
		AmountCents:   amountCents,
		Status:        model.CommissionFlowAvailable,
		Period:        "2026-07",
	}
	require.NoError(t, model.DB.Create(f).Error)
}

// performGetStatements 直调 GetCommissionStatements（A4），query 为原始查询串。
func performGetStatements(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/commission/statements"+query, nil)
	GetCommissionStatements(ctx)
	return w
}

// performGetStatement 直调 GetCommissionStatement（A5）。
func performGetStatement(t *testing.T, idStr string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/commission/statements/"+idStr, nil)
	ctx.Params = gin.Params{{Key: "id", Value: idStr}}
	GetCommissionStatement(ctx)
	return w
}

// performGetStatementItems 直调 GetCommissionStatementItems（A6）。
func performGetStatementItems(t *testing.T, idStr, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/commission/statements/"+idStr+"/items"+query, nil)
	ctx.Params = gin.Params{{Key: "id", Value: idStr}}
	GetCommissionStatementItems(ctx)
	return w
}

// performGetStatementSummary 直调 GetCommissionStatementSummary（A14）。
func performGetStatementSummary(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/commission/statements/summary"+query, nil)
	GetCommissionStatementSummary(ctx)
	return w
}

// seedStatementQueryFixture 固定种子（plan behavior：root + 2 分销商 + 各期账单 + 明细 + 1 条 adjustment）：
//   - S1: distA "2026-07" payable，total_commission=1000、adjusted=-50（挂 1 条调整单）、明细 2 条（cust1/cust2，username JOIN 断言对象）
//   - S2: distB "2026-08" withdrawing，total_commission=2000、明细 3 条（A6 分页断言对象）
//   - S3: distA "2026-08" payable，total_commission=500（A14 跨分销商 totals 合计断言对象）
//
// 排序语义：A4 period DESC + distributor_id ASC → [S3(0808/distA), S2(0808/distB), S1(0707)]（distA.Id < distB.Id）。
func seedStatementQueryFixture(t *testing.T) (admin, distA, distB, cust1 *model.User, s1, s2, s3 *model.CommissionStatement) {
	t.Helper()
	admin = seedStmtUserWithToken(t, "stmt_admin", common.RoleAdminUser, "stmt-admin-token")
	distA = seedStmtUserWithToken(t, "stmt_dist_a", common.RoleDistributorUser, "stmt-dist-a-token")
	distB = seedStmtUser(t, "stmt_dist_b", common.RoleDistributorUser)
	cust1 = seedStmtUser(t, "stmt_cust_1", common.RoleCommonUser)
	cust2 := seedStmtUser(t, "stmt_cust_2", common.RoleCommonUser)
	cust3 := seedStmtUser(t, "stmt_cust_3", common.RoleCommonUser)

	s1 = seedStmtRow(t, distA.Id, "2026-07", model.StatementPayable, 1000, -50)
	seedStmtAdjustment(t, s1.Id, -50, "人工差额补正", admin.Id)
	seedStmtItem(t, s1.Id, "PAY-0701-001", "wxpay", 7000, 700, cust1.Id)
	seedStmtItem(t, s1.Id, "PAY-0702-002", "alipay", 3000, 300, cust2.Id)

	s2 = seedStmtRow(t, distB.Id, "2026-08", model.StatementWithdrawing, 2000, 0)
	seedStmtItem(t, s2.Id, "PAY-0801-001", "wxpay", 10000, 500, cust3.Id)
	seedStmtItem(t, s2.Id, "PAY-0802-002", "alipay", 20000, 700, cust3.Id)
	seedStmtItem(t, s2.Id, "PAY-0803-003", "wxpay", 30000, 800, cust3.Id)

	s3 = seedStmtRow(t, distA.Id, "2026-08", model.StatementPayable, 500, 0)
	return admin, distA, distB, cust1, s1, s2, s3
}

// TestCommissionStatementQuery A4/A5/A6/A14 全分支集成测试（gin test context 直调 handler，
// 权限与路由互斥分支走真实中间件链）。
func TestCommissionStatementQuery(t *testing.T) {
	setupStatementQueryTestDB(t)
	admin, distA, distB, cust1, s1, s2, s3 := seedStatementQueryFixture(t)

	t.Run("a4_no_filter_lists_all_ordered", func(t *testing.T) {
		w := performGetStatements(t, "?p=1&page_size=10")
		require.Equal(t, 200, w.Code)
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(3), data["total"], "3 张账单全量")
		items := data["items"].([]interface{})
		require.Len(t, items, 3)
		// ORDER BY period DESC, distributor_id ASC → [S3(2026-08/distA), S2(2026-08/distB), S1(2026-07/distA)]
		first := items[0].(map[string]interface{})
		require.Equal(t, "2026-08", first["period"])
		require.Equal(t, float64(distA.Id), first["distributor_id"])
		second := items[1].(map[string]interface{})
		require.Equal(t, "2026-08", second["period"])
		require.Equal(t, float64(distB.Id), second["distributor_id"])
		third := items[2].(map[string]interface{})
		require.Equal(t, "2026-07", third["period"])
		require.Equal(t, float64(1000), third["total_commission_cents"])
	})

	t.Run("a4_filter_distributor_id", func(t *testing.T) {
		w := performGetStatements(t, fmt.Sprintf("?distributor_id=%d", distA.Id))
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(2), data["total"], "distA 名下 S1+S3")
		items := data["items"].([]interface{})
		for _, it := range items {
			require.Equal(t, float64(distA.Id), it.(map[string]interface{})["distributor_id"])
		}
	})

	t.Run("a4_filter_period", func(t *testing.T) {
		w := performGetStatements(t, "?period=2026-08")
		resp := parseRateEnvelope(t, w)
		data := resp["data"].(map[string]interface{})
		require.True(t, resp["success"].(bool), "got: %v", resp)
		require.Equal(t, float64(2), data["total"], "仅 2026-08 期 S2+S3")
		w2 := performGetStatements(t, "?period=2026-07")
		data2 := parseRateEnvelope(t, w2)["data"].(map[string]interface{})
		require.Equal(t, float64(1), data2["total"], "仅 2026-07 期 S1")
	})

	t.Run("a4_filter_status", func(t *testing.T) {
		w := performGetStatements(t, "?status=payable")
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(2), data["total"], "payable: S1+S3")
		w2 := performGetStatements(t, "?status=settled")
		resp2 := parseRateEnvelope(t, w2)
		require.True(t, resp2["success"].(bool), "got: %v", resp2)
		data2 := resp2["data"].(map[string]interface{})
		require.Equal(t, float64(0), data2["total"], "无 settled 账单 → 空结果")
	})

	t.Run("a4_combined_filters", func(t *testing.T) {
		w := performGetStatements(t, fmt.Sprintf("?distributor_id=%d&period=2026-08", distB.Id))
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(1), data["total"], "distB+2026-08 仅 S2")
	})

	t.Run("a4_page_size_capped_at_100", func(t *testing.T) {
		w := performGetStatements(t, "?p=1&page_size=500")
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(100), data["page_size"], "page_size>100 截 100（GetPageQuery 冻结语义）")
	})

	t.Run("a4_invalid_distributor_id_rejected", func(t *testing.T) {
		w := performGetStatements(t, "?distributor_id=abc")
		resp := parseRateEnvelope(t, w)
		require.False(t, resp["success"].(bool), "非法 distributor_id → success=false, got: %v", resp)
	})

	t.Run("a5_detail_with_adjustments", func(t *testing.T) {
		w := performGetStatement(t, fmt.Sprintf("%d", s1.Id))
		require.Equal(t, 200, w.Code)
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		stmt := data["statement"].(map[string]interface{})
		require.Equal(t, float64(s1.Id), stmt["id"])
		require.Equal(t, "2026-07", stmt["period"])
		require.Equal(t, float64(1000), stmt["total_commission_cents"])
		require.Equal(t, float64(-50), stmt["adjusted_cents"])
		adjustments := data["adjustments"].([]interface{})
		require.Len(t, adjustments, 1, "S1 挂 1 条调整单")
		adj := adjustments[0].(map[string]interface{})
		require.Equal(t, float64(-50), adj["delta_cents"])
		require.Equal(t, "人工差额补正", adj["reason"])
		require.Equal(t, float64(admin.Id), adj["operator_id"])
	})

	t.Run("a5_invalid_id_400", func(t *testing.T) {
		w := performGetStatement(t, "abc")
		require.Equal(t, 400, w.Code, "strconv 400 语义（commission_rate.go 先例）")
		resp := parseRateEnvelope(t, w)
		require.False(t, resp["success"].(bool))
	})

	t.Run("a5_not_found", func(t *testing.T) {
		w := performGetStatement(t, "99999")
		resp := parseRateEnvelope(t, w)
		require.False(t, resp["success"].(bool), "不存在的账单 → success=false, got: %v", resp)
	})

	t.Run("a6_items_pagination_and_username_join", func(t *testing.T) {
		// S2 挂 3 条明细：p=1&page_size=2 → 2 行 + total=3；p=2 → 1 行（id ASC）
		w := performGetStatementItems(t, fmt.Sprintf("%d", s2.Id), "?p=1&page_size=2")
		require.Equal(t, 200, w.Code)
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, float64(3), data["total"])
		items := data["items"].([]interface{})
		require.Len(t, items, 2)
		first := items[0].(map[string]interface{})
		require.Equal(t, "PAY-0801-001", first["billing_no"], "id ASC")
		require.Equal(t, "stmt_cust_3", first["username"], "A6 username 冗余（LEFT JOIN users on customer_id）")

		w2 := performGetStatementItems(t, fmt.Sprintf("%d", s2.Id), "?p=2&page_size=2")
		items2 := parseRateEnvelope(t, w2)["data"].(map[string]interface{})["items"].([]interface{})
		require.Len(t, items2, 1)
		require.Equal(t, "PAY-0803-003", items2[0].(map[string]interface{})["billing_no"])
	})

	t.Run("a6_item_fields_complete", func(t *testing.T) {
		w := performGetStatementItems(t, fmt.Sprintf("%d", s1.Id), "?p=1&page_size=10")
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		items := resp["data"].(map[string]interface{})["items"].([]interface{})
		require.Len(t, items, 2)
		row := items[0].(map[string]interface{})
		require.Equal(t, "PAY-0701-001", row["billing_no"])
		require.Equal(t, "wxpay", row["payment_method"])
		require.Equal(t, float64(7000), row["topup_money_cents"])
		require.Equal(t, float64(700), row["commission_cents"])
		require.Equal(t, float64(cust1.Id), row["customer_id"])
		require.Equal(t, "stmt_cust_1", row["username"])
		require.Greater(t, row["complete_time"].(float64), float64(0), "到账时间")
	})

	t.Run("a14_summary_multi_distributor_totals", func(t *testing.T) {
		w := performGetStatementSummary(t, "?period=2026-08")
		require.Equal(t, 200, w.Code)
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		require.Equal(t, "2026-08", data["period"])
		items := data["items"].([]interface{})
		require.Len(t, items, 2, "S2(distB)+S3(distA) 按 distributor 分组")
		// ORDER BY total_commission_cents DESC → distB(2000) 在前
		first := items[0].(map[string]interface{})
		require.Equal(t, float64(distB.Id), first["distributor_id"])
		require.Equal(t, "stmt_dist_b", first["username"])
		require.Equal(t, float64(2000), first["total_commission_cents"])
		require.Equal(t, float64(0), first["adjusted_cents"])
		require.Equal(t, model.StatementWithdrawing, first["statement_status"])
		require.Equal(t, float64(s2.Id), first["statement_id"])
		second := items[1].(map[string]interface{})
		require.Equal(t, float64(distA.Id), second["distributor_id"])
		require.Equal(t, "stmt_dist_a", second["username"])
		require.Equal(t, float64(500), second["total_commission_cents"])
		require.Equal(t, model.StatementPayable, second["statement_status"])
		require.Equal(t, float64(s3.Id), second["statement_id"])
		totals := data["totals"].(map[string]interface{})
		require.Equal(t, float64(2500), totals["total_commission_cents"], "2000+500 合计")
		require.Equal(t, float64(0), totals["adjusted_cents"])
		require.Equal(t, float64(2), totals["distributor_count"])
	})

	t.Run("a14_summary_single_period_with_adjustment", func(t *testing.T) {
		w := performGetStatementSummary(t, "?period=2026-07")
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		items := data["items"].([]interface{})
		require.Len(t, items, 1)
		row := items[0].(map[string]interface{})
		require.Equal(t, float64(1000), row["total_commission_cents"])
		require.Equal(t, float64(-50), row["adjusted_cents"], "调整单累计进汇总")
		totals := data["totals"].(map[string]interface{})
		require.Equal(t, float64(1000), totals["total_commission_cents"])
		require.Equal(t, float64(-50), totals["adjusted_cents"], "adjusted 合计含负数")
		require.Equal(t, float64(1), totals["distributor_count"])
	})

	t.Run("a14_missing_period_rejected", func(t *testing.T) {
		w := performGetStatementSummary(t, "")
		resp := parseRateEnvelope(t, w)
		require.False(t, resp["success"].(bool), "period 必填, got: %v", resp)
	})

	t.Run("a14_empty_period_zero_totals_ok", func(t *testing.T) {
		w := performGetStatementSummary(t, "?period=2026-05")
		require.Equal(t, 200, w.Code, "该期无账单 → 200 非 404")
		resp := parseRateEnvelope(t, w)
		require.True(t, resp["success"].(bool), "got: %v", resp)
		data := resp["data"].(map[string]interface{})
		items := data["items"].([]interface{})
		require.Len(t, items, 0, "items 空数组")
		totals := data["totals"].(map[string]interface{})
		require.Equal(t, float64(0), totals["total_commission_cents"])
		require.Equal(t, float64(0), totals["adjusted_cents"])
		require.Equal(t, float64(0), totals["distributor_count"])
	})

	t.Run("route_admin_auth_rejects_distributor", func(t *testing.T) {
		// 权限回归（威胁 T-04-02-01）：role=5 经真实 AdminAuth 中间件链被拒。
		// authHelper 阈值拒绝（5<10）返回 200+success:false（auth.go:145-152）；
		// 数据面不得有任何 items 泄漏。
		r := gin.New()
		r.Use(sessions.Sessions("session", cookie.NewStore([]byte("stmt-query-test-secret"))))
		r.GET("/api/commission/statements", middleware.AdminAuth(), GetCommissionStatements)

		req := httptest.NewRequest("GET", "/api/commission/statements", nil)
		req.Header.Set("Authorization", "stmt-dist-a-token")
		req.Header.Set("New-Api-User", fmt.Sprintf("%d", distA.Id))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code == http.StatusForbidden {
			// 精确匹配拒绝形态
		} else {
			require.Equal(t, 200, w.Code, "阈值拒绝形态 200+success:false, got: %d %s", w.Code, w.Body.String())
			resp := parseRateEnvelope(t, w)
			require.False(t, resp["success"].(bool), "role=5 必须被拒")
		}
		require.NotContains(t, w.Body.String(), `"items"`, "role=5 不得泄漏账单数据")
	})

	t.Run("summary_route_not_swallowed_by_id_param", func(t *testing.T) {
		// gin v1.9.1 静态段与 :id 参数段共存，静态优先：
		// /statements/summary 必须命中 A14 handler（缺失 period → "period 必填"），
		// 若被 :id 吞噬则会走 A5 strconv 失败（"无效的账单ID"）。
		r := gin.New()
		r.Use(sessions.Sessions("session", cookie.NewStore([]byte("stmt-query-test-secret"))))
		r.GET("/api/commission/statements/summary", middleware.AdminAuth(), GetCommissionStatementSummary)
		r.GET("/api/commission/statements/:id", middleware.AdminAuth(), GetCommissionStatement)

		req := httptest.NewRequest("GET", "/api/commission/statements/summary", nil)
		req.Header.Set("Authorization", "stmt-admin-token")
		req.Header.Set("New-Api-User", fmt.Sprintf("%d", admin.Id))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		require.Equal(t, 200, w.Code)
		resp := parseRateEnvelope(t, w)
		require.False(t, resp["success"].(bool))
		require.Equal(t, "period 必填", resp["message"], "summary 路由未被 :id 吞噬, got: %v", resp)

		// 带 period 走通全链路（中间件+handler）
		req2 := httptest.NewRequest("GET", "/api/commission/statements/summary?period=2026-08", nil)
		req2.Header.Set("Authorization", "stmt-admin-token")
		req2.Header.Set("New-Api-User", fmt.Sprintf("%d", admin.Id))
		w2 := httptest.NewRecorder()
		r.ServeHTTP(w2, req2)
		resp2 := parseRateEnvelope(t, w2)
		require.True(t, resp2["success"].(bool), "got: %v", resp2)
		items := resp2["data"].(map[string]interface{})["items"].([]interface{})
		require.Len(t, items, 2)
	})

	t.Run("rate_bp_join_propagation", func(t *testing.T) {
		// 契约 v1.5 附录 F1：明细比例快照经 flow_id 关联佣金流水传播（A6/A11 消费同一查询函数）。
		// 直插一条携带比例快照的流水（harness 已 AutoMigrate commission_flows），再把新明细的
		// flow_id 更新指向它 → 查询结果该行比例快照为 500；
		// 既有 FlowId=0 明细 LEFT JOIN 无匹配行 → 比例快照缺省 0。
		flow := &model.CommissionFlow{
			BillingNo:     "RB-FLOW-001",
			FlowType:      model.CommissionFlowCommission,
			CustomerId:    cust1.Id,
			DistributorId: distA.Id,
			AmountCents:   700,
			RateBp:        500,
			Status:        model.CommissionFlowAvailable,
			Period:        "2026-07",
		}
		require.NoError(t, model.DB.Create(flow).Error)
		item := seedStmtItem(t, s1.Id, "PAY-0703-003", "wxpay", 7000, 700, cust1.Id)
		require.NoError(t, model.DB.Model(&model.CommissionStatementItem{}).
			Where("id = ?", item.Id).Update("flow_id", flow.Id).Error)

		details, total, err := model.ListStatementItems(s1.Id, 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(3), total, "原 2 条 + 新增 1 条")
		rateByBilling := map[string]int{}
		for _, d := range details {
			rateByBilling[d.BillingNo] = d.RateBp
		}
		require.Equal(t, 500, rateByBilling["PAY-0703-003"], "flow 关联明细比例快照传播")
		require.Equal(t, 0, rateByBilling["PAY-0701-001"], "FlowId=0 明细无匹配流水 → 比例快照 0")
	})
}

// TestB4TotalCommission B4 total_commission_cents SUM(amount_cents) 聚合接线（契约附录 D5.3）：
// 正负相抵（reversal 为负数）、无流水客户为 0、不串他分销商数据（WHERE distributor_id 限定，威胁 T-04-02-03）。
func TestB4TotalCommission(t *testing.T) {
	setupStatementQueryTestDB(t)

	d1 := seedStmtUser(t, "b4_dist_a", common.RoleDistributorUser)
	d2 := seedStmtUser(t, "b4_dist_b", common.RoleDistributorUser)
	c1 := seedStmtUser(t, "b4_cust_1", common.RoleCommonUser)
	c2 := seedStmtUser(t, "b4_cust_2", common.RoleCommonUser)
	c3 := seedStmtUser(t, "b4_cust_3", common.RoleCommonUser)
	c4 := seedStmtUser(t, "b4_cust_4", common.RoleCommonUser)
	for _, cu := range []*model.User{c1, c2, c3} {
		cu.InviterId = d1.Id
		require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", cu.Id).Update("inviter_id", d1.Id).Error)
	}
	c4.InviterId = d2.Id
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", c4.Id).Update("inviter_id", d2.Id).Error)

	// c1: +500 commission 与 -200 reversal 正负相抵 → 300
	seedStmtFlow(t, "B4-FLOW-001", model.CommissionFlowCommission, c1.Id, d1.Id, 500)
	seedStmtFlow(t, "B4-FLOW-002", model.CommissionFlowReversal, c1.Id, d1.Id, -200)
	// c2: +1000
	seedStmtFlow(t, "B4-FLOW-003", model.CommissionFlowCommission, c2.Id, d1.Id, 1000)
	// c3: 无流水 → 0
	// c4: D2 名下 +999，不得串入 D1 结果
	seedStmtFlow(t, "B4-FLOW-004", model.CommissionFlowCommission, c4.Id, d2.Id, 999)

	// 直调 B4（DistributorAuth 身份经 c.Set("id") 注入，B 域测试模式）
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest("GET", "/api/distributor/customers?p=1&page_size=10", nil)
	ctx.Set("id", d1.Id)
	GetDistributorCustomers(ctx)
	require.Equal(t, 200, w.Code)
	resp := parseRateEnvelope(t, w)
	require.True(t, resp["success"].(bool), "got: %v", resp)
	data := resp["data"].(map[string]interface{})
	require.Equal(t, float64(3), data["total"], "D1 名下 3 客户")
	items := data["items"].([]interface{})
	require.Len(t, items, 3)

	totals := map[int]int64{}
	for _, it := range items {
		m := it.(map[string]interface{})
		totals[int(m["id"].(float64))] = int64(m["total_commission_cents"].(float64))
	}
	require.Equal(t, int64(300), totals[c1.Id], "正负相抵 500-200=300")
	require.Equal(t, int64(1000), totals[c2.Id])
	require.Equal(t, int64(0), totals[c3.Id], "无流水客户为 0")
	for _, it := range items {
		m := it.(map[string]interface{})
		require.NotEqual(t, int64(999), int64(m["total_commission_cents"].(float64)), "D2 流水不得串入 D1")
	}

	// D2 视角：c4 = 999（归属谓词反向验证）
	w2 := httptest.NewRecorder()
	ctx2, _ := gin.CreateTestContext(w2)
	ctx2.Request = httptest.NewRequest("GET", "/api/distributor/customers?p=1&page_size=10", nil)
	ctx2.Set("id", d2.Id)
	GetDistributorCustomers(ctx2)
	resp2 := parseRateEnvelope(t, w2)
	require.True(t, resp2["success"].(bool), "got: %v", resp2)
	items2 := resp2["data"].(map[string]interface{})["items"].([]interface{})
	require.Len(t, items2, 1)
	row := items2[0].(map[string]interface{})
	require.Equal(t, float64(c4.Id), row["id"])
	require.Equal(t, float64(999), row["total_commission_cents"], "D2 自查见自己的聚合")
}
