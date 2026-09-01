package controller

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// VoidTopUp A13 充值单作废（超管权限中间件挂载于路由层，契约 01-CONTRACT.md 附录 C1 冻结）。
// POST /api/topup/:tradeNo/void  请求体 {"reason": string}（必填，审计留痕）
//
// 响应信封（项目约定 /api/* 一律 HTTP 200 + {success, message, data}，勿手写 4xx）：
//   - 成功 → {"success": true}
//   - reason 缺失/空白 → success:false（400 语义）
//   - ErrTopUpNotFound / ErrTopUpStatusInvalid / ErrAlreadyStatemented → 经既有 sentinel
//     由 common.ApiError 返回（契约 C1：已出账作废整体回滚并报错，禁止半作废）
//
// 事务语义（model.VoidTopUp 单事务同生共死）：① Status → refunded ② 扣回额度
// Amount×QuotaPerUnit（允许负余额）③ 生成负数 reversal 冲销流水 ④ 审计留痕。
func VoidTopUp(c *gin.Context) {
	tradeNo := c.Param("tradeNo")
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Reason) == "" {
		common.ApiErrorMsg(c, "作废原因不能为空")
		return
	}
	operatorId := c.GetInt("id")
	if err := model.VoidTopUp(tradeNo, req.Reason, operatorId); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
