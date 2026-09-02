package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetCommissionStatements A4 管理员账单列表（AdminAuth，契约 01-CONTRACT.md §4.3 A4 冻结形状）。
// GET /api/commission/statements?distributor_id=&period=&status=&p=&page_size=
// 三过滤可选：distributor_id 非法值拒绝（不信任客户端输入）；period/status 空串跳过谓词。
func GetCommissionStatements(c *gin.Context) {
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
	items, total, err := model.ListStatements(distributorId, c.Query("period"), c.Query("status"), pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetCommissionStatement A5 账单详情（AdminAuth，契约 §4.3 A5：账单本体 + adjustments 数组）。
// GET /api/commission/statements/:id
// 校验链：id 解析失败 → HTTP 400（commission_rate.go:41-48 先例）→ 不存在 → success=false。
func GetCommissionStatement(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的账单ID",
		})
		return
	}
	stmt, adjustments, err := model.GetStatementWithAdjustments(int64(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "账单不存在")
			return
		}
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, gin.H{
		"statement":   stmt,
		"adjustments": adjustments,
	})
}

// GetCommissionStatementItems A6 账单明细分页（AdminAuth，契约 §4.3 A6：items 含客户 username 冗余）。
// GET /api/commission/statements/:id/items?p=&page_size=
func GetCommissionStatementItems(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的账单ID",
		})
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.ListStatementItems(int64(id), pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetCommissionStatementSummary A14 跨分销商汇总报表（AdminAuth，契约附录 D3 冻结形状）。
// GET /api/commission/statements/summary?period=YYYY-MM
// → data: {period, items[{distributor_id,username,total_commission_cents,adjusted_cents,statement_status,statement_id}], totals{total_commission_cents,adjusted_cents,distributor_count}}
// period 必填；该期无账单 → items 空数组 + totals 零值（200，承载 ROADMAP 验收 4）。
func GetCommissionStatementSummary(c *gin.Context) {
	period := c.Query("period")
	if period == "" {
		common.ApiErrorMsg(c, "period 必填")
		return
	}
	summary, err := model.SummarizeStatements(period)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, summary)
}
