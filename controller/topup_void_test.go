package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 本文件为 A13 充值单作废 API（契约 01-CONTRACT.md 附录 C1 冻结）的 controller 集成测试。
// gin test context 直调 handler（不走路由中间件——RootAuth 挂载由路由层注册验证）。
//
// 测试库纪律（Pitfall 1，02-04 死锁教训）：独立内存库 + _busy_timeout=30000，
// 禁止 SetMaxOpenConns(1)——VoidTopUp 内 DB.Transaction 嵌套查询需要多连接。

// topupVoidSeq 测试数据序列号：保证 Username/AffCode/TradeNo 唯一。
var topupVoidSeq atomic.Int64

// setupTopupVoidTestDB 初始化独立内存 SQLite 测试库并覆盖全局 DB/LOG_DB
// （平移 setupAttributionTestDB 模式）。
func setupTopupVoidTestDB(t *testing.T) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_busy_timeout=30000", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	// 禁止 SetMaxOpenConns(1)：事务内嵌套查询需要多连接，单连接死锁（02-04 教训）
	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(
		&model.User{}, &model.TopUp{}, &model.CommissionFlow{}, &model.CommissionRate{}, &model.Log{},
	))
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		_ = sqlDB.Close()
	})
}

// withCommissionEnabledForVoid 临时打开佣金总开关（记账 seed 需要），测试结束恢复。
func withCommissionEnabledForVoid(t *testing.T, enabled bool) {
	t.Helper()
	orig := common.CommissionEnabled
	common.CommissionEnabled = enabled
	t.Cleanup(func() { common.CommissionEnabled = orig })
}

// topupVoidFixture 一键作废链路种子：root 操作者 + 分销商 + 客户（inviter_id）+ rate + success TopUp + 原 commission 流水。
type topupVoidFixture struct {
	Root        *model.User
	Distributor *model.User
	Customer    *model.User
	TopUp       *model.TopUp
	Original    *model.CommissionFlow
}

// seedTopupVoidFixture 构造完整冲销前置链路；customerQuota 指定客户初始额度。
// 原 commission 流水通过真实记账路径（model.RecordCommissionTx）产生。
func seedTopupVoidFixture(t *testing.T, customerQuota int) *topupVoidFixture {
	t.Helper()
	withCommissionEnabledForVoid(t, true)
	seq := topupVoidSeq.Add(1)

	f := &topupVoidFixture{
		Root: &model.User{
			Username: fmt.Sprintf("voidroot%d", seq),
			Password: "testpass1234",
			Role:     common.RoleRootUser,
			Status:   common.UserStatusEnabled,
			AffCode:  fmt.Sprintf("VR%d", seq),
		},
		Distributor: &model.User{
			Username: fmt.Sprintf("voiddist%d", seq),
			Password: "testpass1234",
			Role:     common.RoleDistributorUser,
			Status:   common.UserStatusEnabled,
			AffCode:  fmt.Sprintf("VD%d", seq),
		},
		Customer: &model.User{
			Username: fmt.Sprintf("voidcust%d", seq),
			Password: "testpass1234",
			Role:     common.RoleCommonUser,
			Status:   common.UserStatusEnabled,
			AffCode:  fmt.Sprintf("VC%d", seq),
			Quota:    customerQuota,
		},
	}
	require.NoError(t, model.DB.Create(f.Root).Error)
	require.NoError(t, model.DB.Create(f.Distributor).Error)
	f.Customer.InviterId = f.Distributor.Id
	require.NoError(t, model.DB.Create(f.Customer).Error)
	require.NoError(t, model.DB.Create(&model.CommissionRate{
		DistributorId: f.Distributor.Id,
		RateBp:        1000,
	}).Error)

	f.TopUp = &model.TopUp{
		UserId:          f.Customer.Id,
		Amount:          1000, // 额度数 → 扣回 quota = 1000 × QuotaPerUnit
		Money:           100.00,
		TradeNo:         fmt.Sprintf("TV%d%d", time.Now().UnixNano(), seq),
		PaymentProvider: model.PaymentProviderEpay,
		PaymentMethod:   model.PaymentProviderEpay,
		CreateTime:      common.GetTimestamp(),
		CompleteTime:    time.Date(2026, 8, 15, 12, 0, 0, 0, time.Local).Unix(),
		Status:          common.TopUpStatusSuccess,
	}
	require.NoError(t, model.DB.Create(f.TopUp).Error)

	// 真实记账产生原 commission 流水（Period="2026-08"，Money 100 元 → 10000 分 × 1000bp = 1000 分）
	require.NoError(t, model.RecordCommissionTx(model.DB, f.TopUp))
	f.Original = &model.CommissionFlow{}
	require.NoError(t, model.DB.Where("trade_no = ? AND flow_type = ?", f.TopUp.TradeNo, model.CommissionFlowCommission).First(f.Original).Error)
	return f
}

// callVoidTopUp gin test context 直调 VoidTopUp handler（RootAuth 语义用 c.Set("id") 模拟）。
// 返回解析后的响应信封；并断言响应恒为 HTTP 200（/api/* 信封约定）。
func callVoidTopUp(t *testing.T, tradeNo string, body string, operatorId int) map[string]any {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/topup/"+tradeNo+"/void", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Params = gin.Params{{Key: "tradeNo", Value: tradeNo}}
	c.Set("id", operatorId)
	VoidTopUp(c)
	require.Equal(t, http.StatusOK, w.Code, "信封约定：/api/* 一律 HTTP 200")
	var resp map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	return resp
}

// TestTopupVoid A13 全分支矩阵（契约 C1）：
// happy path 三态同事务（refunded + 扣回 + 负数 reversal）、重复作废拒绝、pending 拒绝、
// reason 缺失拒绝、ErrAlreadyStatemented 半作废整体回滚、负余额无钳制。
func TestTopupVoid(t *testing.T) {
	t.Run("happy_path_three_states_same_tx", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		const initialQuota = 10_000_000
		f := seedTopupVoidFixture(t, initialQuota)

		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"客户退款"}`, f.Root.Id)
		require.Equal(t, true, resp["success"], "message: %v", resp["message"])

		// ① TopUp.Status → refunded
		var tu model.TopUp
		require.NoError(t, model.DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&tu).Error)
		require.Equal(t, common.TopUpStatusRefunded, tu.Status)

		// ② 客户 quota 扣回 Amount×QuotaPerUnit（与加账对称）
		var customer model.User
		require.NoError(t, model.DB.First(&customer, f.Customer.Id).Error)
		wantDeduct := int(float64(f.TopUp.Amount) * common.QuotaPerUnit)
		require.Equal(t, initialQuota-wantDeduct, customer.Quota, "quota 扣回 = Amount×QuotaPerUnit")

		// ③ 原 commission 流水保留 + 恰好 1 条负数 reversal
		var flows []model.CommissionFlow
		require.NoError(t, model.DB.Where("trade_no = ?", f.TopUp.TradeNo).Order("id ASC").Find(&flows).Error)
		require.Len(t, flows, 2, "原流水保留 + 1 条 reversal")
		require.Equal(t, model.CommissionFlowCommission, flows[0].FlowType)
		require.Equal(t, f.Original.Id, flows[0].Id, "原流水未被删除")
		rev := flows[1]
		require.Equal(t, model.CommissionFlowReversal, rev.FlowType)
		require.Equal(t, -f.Original.AmountCents, rev.AmountCents, "reversal 为负数")
		require.Equal(t, f.Original.Id, rev.RelatedFlowId, "RelatedFlowId 指向原流水")
		require.Equal(t, "客户退款", rev.Reason, "reason 透传")
		require.Equal(t, f.Original.Period, rev.Period, "同账期")
	})

	t.Run("double_void_rejected", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		f := seedTopupVoidFixture(t, 10_000_000)
		require.NoError(t, model.VoidTopUp(f.TopUp.TradeNo, "首次作废", f.Root.Id))

		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"二次作废"}`, f.Root.Id)
		require.Equal(t, false, resp["success"], "重复作废必须拒绝（契约 C1 400 语义）")

		// 幂等：仍只有 1 条 reversal
		var count int64
		require.NoError(t, model.DB.Model(&model.CommissionFlow{}).Where("flow_type = ?", model.CommissionFlowReversal).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})

	t.Run("pending_topup_rejected", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		f := seedTopupVoidFixture(t, 10_000_000)
		require.NoError(t, model.DB.Model(f.TopUp).Update("status", common.TopUpStatusPending).Error)

		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"pending 单"}`, f.Root.Id)
		require.Equal(t, false, resp["success"], "pending 单不得作废（仅 success 可作废）")
	})

	t.Run("reason_missing_rejected", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		f := seedTopupVoidFixture(t, 10_000_000)

		// reason 缺字段
		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{}`, f.Root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "作废原因不能为空")

		// reason 空白字符串
		resp = callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"   "}`, f.Root.Id)
		require.Equal(t, false, resp["success"])
		require.Contains(t, resp["message"], "作废原因不能为空")
	})

	t.Run("statemented_rollback_no_half_void", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		const initialQuota = 10_000_000
		f := seedTopupVoidFixture(t, initialQuota)
		// 直改原流水 status=available 模拟已出账
		require.NoError(t, model.DB.Model(&model.CommissionFlow{}).
			Where("trade_no = ? AND flow_type = ?", f.TopUp.TradeNo, model.CommissionFlowCommission).
			Update("status", model.CommissionFlowAvailable).Error)

		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"已出账作废尝试"}`, f.Root.Id)
		require.Equal(t, false, resp["success"], "ErrAlreadyStatemented → 作废失败")
		require.Contains(t, resp["message"], "already statemented")

		// 单事务整体回滚证据（三点一致，禁止半作废，T-03-03-03）：
		// a. TopUp.Status 仍 success
		var tu model.TopUp
		require.NoError(t, model.DB.Where("trade_no = ?", f.TopUp.TradeNo).First(&tu).Error)
		require.Equal(t, common.TopUpStatusSuccess, tu.Status, "回滚后 status 仍 success")
		// b. quota 未扣
		var customer model.User
		require.NoError(t, model.DB.First(&customer, f.Customer.Id).Error)
		require.Equal(t, initialQuota, customer.Quota, "回滚后 quota 未扣")
		// c. 零 reversal 流水
		var count int64
		require.NoError(t, model.DB.Model(&model.CommissionFlow{}).Where("flow_type = ?", model.CommissionFlowReversal).Count(&count).Error)
		require.Zero(t, count, "回滚后零 reversal")
	})

	t.Run("negative_balance_no_clamping", func(t *testing.T) {
		setupTopupVoidTestDB(t)
		f := seedTopupVoidFixture(t, 0) // 客户 quota=0

		resp := callVoidTopUp(t, f.TopUp.TradeNo, `{"reason":"负余额验证"}`, f.Root.Id)
		require.Equal(t, true, resp["success"], "message: %v", resp["message"])

		var customer model.User
		require.NoError(t, model.DB.First(&customer, f.Customer.Id).Error)
		wantDeduct := int(float64(f.TopUp.Amount) * common.QuotaPerUnit)
		require.Equal(t, -wantDeduct, customer.Quota, "允许扣成负数（契约 C1，禁钳制）")
	})
}
