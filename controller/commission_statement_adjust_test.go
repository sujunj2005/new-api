package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// 本文件为 A7/A8 超管调账 API（契约 01-CONTRACT.md §4.3 冻结形状，COMM-05/06/07）
// 的 controller 集成测试。gin test context 直调 handler（不走路由中间件——
// 超管权限挂载由路由层注册验证，权限语义用 c.Set("id") 模拟会话，topup_void_test 先例）。
//
// 审计断言：成功路径后 logs 表恰 1 条新增管理留痕且 content 含关键信息；
// 失败路径 logs 零新增（事务外留痕不随失败产生）。

// performManualFlow 直调 CreateManualCommissionFlow（A7）。
func performManualFlow(t *testing.T, body string, operatorId int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/commission/flows/manual", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("id", operatorId)
	CreateManualCommissionFlow(c)
	require.Equal(t, http.StatusOK, w.Code, "信封约定：/api/* 一律 HTTP 200")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// performStatementAdjustment 直调 CreateStatementAdjustment（A8）。
func performStatementAdjustment(t *testing.T, idStr, body string, operatorId int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/commission/statements/"+idStr+"/adjustments", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "id", Value: idStr}}
	c.Set("id", operatorId)
	CreateStatementAdjustment(c)
	require.Equal(t, http.StatusOK, w.Code, "信封约定：/api/* 一律 HTTP 200")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// manageLogCount 当前库中管理留痕条数（type=LogTypeManage）。
func manageLogCount(t *testing.T) int64 {
	t.Helper()
	var cnt int64
	require.NoError(t, model.DB.Model(&model.Log{}).Where("type = ?", model.LogTypeManage).Count(&cnt).Error)
	return cnt
}

// lastManageLog 取最新一条管理留痕（id DESC）。
func lastManageLog(t *testing.T) model.Log {
	t.Helper()
	var lg model.Log
	require.NoError(t, model.DB.Where("type = ?", model.LogTypeManage).Order("id DESC").First(&lg).Error)
	return lg
}

// TestManualCommissionFlow A7 全分支（COMM-05：补录/冲销、reason 必填、目标校验、审计留痕）。
func TestManualCommissionFlow(t *testing.T) {
	t.Run("credit_success_with_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)

		before := manageLogCount(t)
		body := `{"distributor_id":` + itoa(int64(dist.Id)) + `,"flow_type":"manual_credit","amount_cents":100,"reason":"差错补录"}`
		resp := performManualFlow(t, body, root.Id)
		require.Equal(t, true, resp["success"], "message: %v", resp["message"])

		// 流水落库（model 层语义已在 TestCreateManualFlow 覆盖，此处断言 API 面）
		var flow model.CommissionFlow
		require.NoError(t, model.DB.Where("distributor_id = ?", dist.Id).First(&flow).Error)
		require.Equal(t, model.CommissionFlowManualCredit, flow.FlowType)
		require.EqualValues(t, 100, flow.AmountCents)
		require.Equal(t, root.Id, flow.OperatorId)

		// 审计留痕：恰 1 条新增，content 含关键信息，operator 归属正确
		require.Equal(t, before+1, manageLogCount(t), "成功路径恰 1 条管理留痕")
		lg := lastManageLog(t)
		require.Equal(t, root.Id, lg.UserId)
		require.Contains(t, lg.Content, "distributor_id=")
		require.Contains(t, lg.Content, "manual_credit")
		require.Contains(t, lg.Content, "amount_cents=100")
		require.Contains(t, lg.Content, "差错补录")
	})

	t.Run("debit_stores_negative", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)

		body := `{"distributor_id":` + itoa(int64(dist.Id)) + `,"flow_type":"manual_debit","amount_cents":100,"reason":"多记冲销"}`
		resp := performManualFlow(t, body, root.Id)
		require.Equal(t, true, resp["success"], "message: %v", resp["message"])

		var flow model.CommissionFlow
		require.NoError(t, model.DB.Where("distributor_id = ?", dist.Id).First(&flow).Error)
		require.Equal(t, model.CommissionFlowManualDebit, flow.FlowType)
		require.EqualValues(t, -100, flow.AmountCents, "请求恒正、落库取负（契约 A7）")
	})

	t.Run("reason_missing_rejected_no_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)

		before := manageLogCount(t)
		// reason 缺字段 / 空白字符串均拒绝
		resp := performManualFlow(t, `{"distributor_id":`+itoa(int64(dist.Id))+`,"flow_type":"manual_credit","amount_cents":100}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "原因")
		resp = performManualFlow(t, `{"distributor_id":`+itoa(int64(dist.Id))+`,"flow_type":"manual_credit","amount_cents":100,"reason":"   "}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "原因")

		require.Equal(t, before, manageLogCount(t), "失败路径零成功留痕")
		var flowCnt int64
		require.NoError(t, model.DB.Model(&model.CommissionFlow{}).Count(&flowCnt).Error)
		require.Zero(t, flowCnt, "失败路径零流水")
	})

	t.Run("amount_zero_rejected", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)

		resp := performManualFlow(t, `{"distributor_id":`+itoa(int64(dist.Id))+`,"flow_type":"manual_credit","amount_cents":0,"reason":"零金额"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "金额")
	})

	t.Run("non_distributor_target_rejected", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		admin := seedStmtUser(t, "adj_admin", common.RoleAdminUser)

		resp := performManualFlow(t, `{"distributor_id":`+itoa(int64(admin.Id))+`,"flow_type":"manual_credit","amount_cents":100,"reason":"误填管理员"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "分销商")
	})

	t.Run("distributor_id_missing_rejected", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)

		resp := performManualFlow(t, `{"flow_type":"manual_credit","amount_cents":100,"reason":"缺目标"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "分销商")
	})
}

// TestStatementAdjustmentAPI A8 全分支（COMM-06/07：白名单状态门、累计、审计留痕）。
func TestStatementAdjustmentAPI(t *testing.T) {
	t.Run("payable_success_with_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)
		stmt := seedStmtRow(t, dist.Id, "2026-08", model.StatementPayable, 10000, 0)

		before := manageLogCount(t)
		resp := performStatementAdjustment(t, itoa(stmt.Id), `{"delta_cents":-50,"reason":"多记冲正"}`, root.Id)
		require.Equal(t, true, resp["success"], "message: %v", resp["message"])

		var got model.CommissionStatement
		require.NoError(t, model.DB.First(&got, stmt.Id).Error)
		require.EqualValues(t, -50, got.AdjustedCents)
		require.EqualValues(t, 9950, got.SettleAmountCents)

		require.Equal(t, before+1, manageLogCount(t), "成功路径恰 1 条管理留痕")
		lg := lastManageLog(t)
		require.Equal(t, root.Id, lg.UserId)
		require.Contains(t, lg.Content, "statement_id=")
		require.Contains(t, lg.Content, "delta_cents=-50")
		require.Contains(t, lg.Content, "多记冲正")
	})

	t.Run("withdrawing_rejected_no_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)
		stmt := seedStmtRow(t, dist.Id, "2026-08", model.StatementWithdrawing, 10000, 0)

		before := manageLogCount(t)
		resp := performStatementAdjustment(t, itoa(stmt.Id), `{"delta_cents":-50,"reason":"提现中调整"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "提现中")
		require.Equal(t, before, manageLogCount(t), "失败路径零成功留痕")
	})

	t.Run("settled_rejected_no_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)
		stmt := seedStmtRow(t, dist.Id, "2026-08", model.StatementSettled, 10000, 0)

		before := manageLogCount(t)
		resp := performStatementAdjustment(t, itoa(stmt.Id), `{"delta_cents":-50,"reason":"已结清调整"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "已结清")
		require.Equal(t, before, manageLogCount(t), "失败路径零成功留痕")
	})

	t.Run("delta_zero_rejected", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)
		dist := seedStmtUser(t, "adj_dist", common.RoleDistributorUser)
		stmt := seedStmtRow(t, dist.Id, "2026-08", model.StatementPayable, 10000, 0)

		resp := performStatementAdjustment(t, itoa(stmt.Id), `{"delta_cents":0,"reason":"零差额"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "差额")
	})

	t.Run("id_not_number_rejected", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)

		resp := performStatementAdjustment(t, "abc", `{"delta_cents":-50,"reason":"非数字ID"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "账单")
	})

	t.Run("statement_not_found_rejected_no_audit", func(t *testing.T) {
		setupStatementQueryTestDB(t)
		root := seedStmtUser(t, "adj_root", common.RoleRootUser)

		before := manageLogCount(t)
		resp := performStatementAdjustment(t, "99999999", `{"delta_cents":-50,"reason":"不存在"}`, root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "不存在")
		require.Equal(t, before, manageLogCount(t), "失败路径零成功留痕")
	})
}

// itoa int64 → JSON 数字字符串（测试请求体拼接用；User.Id/Statement.Id 统一经 int64）。
func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
