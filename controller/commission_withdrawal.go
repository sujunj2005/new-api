package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// 按单提现 WDRAW 面（契约 01-CONTRACT.md 附录 E2 冻结形状 A15-A21，v1.4）。
// 权限中间件挂载于路由层注册处（分销商侧精确匹配 role==5 / 管理侧阈值 role>=10），
// 本文件零权限字样（03-03 grep 纪律）；全端点零分佣总开关门控分支（D-14/Q5=A）。
//
// controller 零业务状态判断——五态迁移白名单与账单联动全部在 model Tx 内；
// 本文件只做：strconv 绑定 → JSON 绑定 → 必填前置拦截 → Tx → 信封。
//
// 审计纪律（A7/A8 先例）：A18-A21 留痕在 model Tx return nil（事务已提交）之后
// 事务外执行管理操作审计留痕——失败路径零成功留痕；A15 无事务外审计（申请留痕
// 即提现单行本身，且分销商侧无兜底审计链路）；管理侧写操作自动获得中间件兜底
// 审计（三层留痕的第三层，A18-A21 双条为既有现状语义）。
// 错误路径一律信封（success=false）——兜底审计的 success 判定依赖它（Pitfall 4/T-05-01-10）。

// CreateWithdrawal A15 发起提现申请（按单全额，D-08：请求零金额输入面）。
// POST /api/commission/withdrawals  {"statement_id": int64}
func CreateWithdrawal(c *gin.Context) {
	var req struct {
		StatementId int64 `json:"statement_id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if _, err := model.CreateWithdrawalTx(req.StatementId, c.GetInt("id")); err != nil {
		if errors.Is(err, model.ErrWithdrawalNotOwner) { // 归属越权 → 403（CUST-05/ROLE-02）
			c.JSON(http.StatusForbidden, gin.H{"success": false, "message": err.Error()})
			return
		}
		common.ApiError(c, err) // payable 白名单等业务错误走信封（Pitfall 6 文案透传）
		return
	}
	common.ApiSuccess(c, nil) // 附录 E2 冻结响应形状（A7/A8 先例）
}

// GetSelfWithdrawals A16 分销商提现单列表（服务端强制 WHERE distributor_id = self）。
// GET /api/commission/withdrawals/self?p=&page_size=
func GetSelfWithdrawals(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.ListWithdrawals(c.GetInt("id"), 0, "", pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetWithdrawals A17 管理员提现单列表（distributor_id/status 过滤可选，非法 distributor_id 拒绝）。
// GET /api/commission/withdrawals?distributor_id=&status=&p=
func GetWithdrawals(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	distributorId := 0
	if s := c.Query("distributor_id"); s != "" {
		v, err := strconv.Atoi(s)
		if err != nil {
			common.ApiErrorMsg(c, "无效的分销商ID")
			return
		}
		distributorId = v
	}
	items, total, err := model.ListWithdrawals(0, distributorId, c.Query("status"), pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// withdrawalAmountCents 事务外取账单 settle 金额做审计快照（D-08 实时值兜底；
// 仅用于审计日志内容，不参与任何写路径）。
func withdrawalAmountCents(statementId int64) int64 {
	if stmt, _, err := model.GetStatementWithAdjustments(statementId); err == nil {
		return stmt.SettleAmountCents
	}
	return 0
}

// AcceptWithdrawal A18 受理（pending→reviewing，账单零操作）。
// POST /api/commission/withdrawals/:id/accept（无 body）
func AcceptWithdrawal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的提现单ID")
		return
	}
	wd, err := model.AcceptWithdrawalTx(int64(id), c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕（失败路径零成功留痕）
	model.RecordLog(c.GetInt("id"), model.LogTypeManage, fmt.Sprintf("提现受理 withdrawal_no=%s statement_id=%d", wd.WithdrawalNo, wd.StatementId))
	common.ApiSuccess(c, nil)
}

// RejectWithdrawal A19 驳回（仅 reviewing 可发起，Q2=B；同事务账单回退 payable）。
// POST /api/commission/withdrawals/:id/reject  {"reason": string 必填}
func RejectWithdrawal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的提现单ID")
		return
	}
	var req struct {
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if strings.TrimSpace(req.Reason) == "" {
		common.ApiErrorMsg(c, "驳回原因不能为空")
		return
	}
	operatorId := c.GetInt("id")
	wd, err := model.RejectWithdrawalTx(int64(id), req.Reason, operatorId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕：嵌 withdrawal_no/statement_id/amount_cents 快照/reason
	// （amount 实时取 statement.settle_amount_cents，D-08 审计兜底）
	model.RecordLog(operatorId, model.LogTypeManage, fmt.Sprintf("提现驳回 withdrawal_no=%s statement_id=%d amount_cents=%d reason=%s", wd.WithdrawalNo, wd.StatementId, withdrawalAmountCents(wd.StatementId), req.Reason))
	common.ApiSuccess(c, nil)
}

// ApproveWithdrawal A20 批准（reviewing→approved，账单保持 withdrawing，Q1=B）。
// POST /api/commission/withdrawals/:id/approve（无 body）
func ApproveWithdrawal(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的提现单ID")
		return
	}
	wd, err := model.ApproveWithdrawalTx(int64(id), c.GetInt("id"))
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕（失败路径零成功留痕）
	model.RecordLog(c.GetInt("id"), model.LogTypeManage, fmt.Sprintf("提现批准 withdrawal_no=%s statement_id=%d", wd.WithdrawalNo, wd.StatementId))
	common.ApiSuccess(c, nil)
}

// MarkWithdrawalPaid A21 打款登记（仅 approved 可发起，Q1=B；同事务账单 settled）。
// POST /api/commission/withdrawals/:id/paid  {"voucher_no": string 必填}
func MarkWithdrawalPaid(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的提现单ID")
		return
	}
	var req struct {
		VoucherNo string `json:"voucher_no"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ApiErrorMsg(c, "无效的请求参数")
		return
	}
	if strings.TrimSpace(req.VoucherNo) == "" {
		common.ApiErrorMsg(c, "打款凭证号不能为空")
		return
	}
	operatorId := c.GetInt("id")
	wd, err := model.MarkWithdrawalPaidTx(int64(id), req.VoucherNo, operatorId)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	// 事务提交成功后的审计留痕：嵌 withdrawal_no/voucher_no/amount_cents 快照
	model.RecordLog(operatorId, model.LogTypeManage, fmt.Sprintf("提现打款 withdrawal_no=%s statement_id=%d amount_cents=%d voucher_no=%s", wd.WithdrawalNo, wd.StatementId, withdrawalAmountCents(wd.StatementId), req.VoucherNo))
	common.ApiSuccess(c, nil)
}
