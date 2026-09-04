package controller

import (
	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 分销商佣金总览面（契约 01-CONTRACT.md §4.3 A9/A12 冻结形状 + v1.5 附录 F，CUST-03/STMT-04/CUST-05）。
// 权限中间件（精确匹配 role==5）挂载于路由层注册处，本文件零权限字样（03-03 grep 纪律）。
// 数据面服务端收窄：distributor_id 强制取会话身份，禁信任何客户端参数（ROLE-02）。
// A9/A12 无请求参数无分页：handler 只做会话派生 → model 聚合 → 信封（零业务判断，
// 聚合口径与四卡恒等式全部收口在 model/commission_dashboard.go）。

// GetCommissionDashboard A9 分销商佣金仪表盘（四项汇总 + 附录 F2 笔数，服务端聚合唯一事实源）。
// GET /api/commission/dashboard
func GetCommissionDashboard(c *gin.Context) {
	distributorId := c.GetInt("id") // 服务端会话派生，禁信任何客户端参数（ROLE-02）
	data, err := model.GetCommissionDashboard(distributorId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}

// GetCurrentCommission A12 未出账实时预览（period/笔数/金额 三冻结字段，与 A9-① 同谓词绑死）。
// GET /api/commission/current
func GetCurrentCommission(c *gin.Context) {
	distributorId := c.GetInt("id") // 服务端会话派生，禁信任何客户端参数（ROLE-02）
	data, err := model.GetCurrentPending(distributorId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, data)
}
