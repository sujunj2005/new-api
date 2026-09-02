package controller

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 超管调账面（契约 01-CONTRACT.md §4.3 A7/A8 冻结形状，COMM-05/06）。
// 权限中间件（超管阈值）挂载于路由层注册处，本文件零权限字样（03-03 grep 纪律）。
// 请求形状冻结——勿增删字段：
//   A7 POST /api/commission/flows/manual  {"distributor_id":int,"flow_type":string,"amount_cents":int64,"reason":string}
//   A8 POST /api/commission/statements/:id/adjustments  {"delta_cents":int64,"reason":string}（仅应付状态可开）
//
// 审计纪律（VoidTopUp 先例）：留痕写操作在 model 函数 return nil（事务已提交）之后
// 事务外执行——失败路径零成功留痕；留痕内部走全局 DB，事务内调用会绕过 tx 且不应随回滚消失。
// reason 双层校验的 controller 侧：TrimSpace 必填（缺失 → 400 语义信封）。

// CreateManualCommissionFlow A7 超管人工补录/冲销流水。
// 校验链主体在 model.CreateManualFlow（flow_type 白名单/金额恒正/目标角色精确判定），
// controller 侧仅做请求绑定与 reason 必填前置拦截。
func CreateManualCommissionFlow(c *gin.Context) {
	var req struct {
		DistributorId int    `json:"distributor_id"`
		FlowType      string `json:"flow_type"`
		AmountCents   int64  `json:"amount_cents"`
		Reason        string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		common.ApiErrorMsg(c, "原因不能为空")
		return
	}
	operatorId := c.GetInt("id")
	if err := model.CreateManualFlow(req.DistributorId, req.FlowType, req.AmountCents, req.Reason, operatorId); err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕（失败路径不留成功留痕）
	model.RecordLog(operatorId, model.LogTypeManage, fmt.Sprintf("人工补录/冲销 distributor_id=%d flow_type=%s amount_cents=%d reason=%s", req.DistributorId, req.FlowType, req.AmountCents, req.Reason))
	common.ApiSuccess(c, nil)
}

// CreateStatementAdjustment A8 超管对账单调整单（仅应付状态可开，白名单在 model 层）。
// 服务端按账单当前累计重算 settle 金额（不信任客户端），原始账单 Total/明细零触碰。
func CreateStatementAdjustment(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的账单ID")
		return
	}
	var req struct {
		DeltaCents int64  `json:"delta_cents"`
		Reason     string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		common.ApiErrorMsg(c, "原因不能为空")
		return
	}
	operatorId := c.GetInt("id")
	if err := model.CreateStatementAdjustmentTx(int64(id), req.DeltaCents, req.Reason, operatorId); err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕（失败路径不留成功留痕）
	model.RecordLog(operatorId, model.LogTypeManage, fmt.Sprintf("账单调整 statement_id=%d delta_cents=%d reason=%s", id, req.DeltaCents, req.Reason))
	common.ApiSuccess(c, nil)
}
