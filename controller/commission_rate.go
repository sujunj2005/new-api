package controller

import (
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// GetCommissionRates A1 比例列表（AdminAuth，契约 01-CONTRACT.md §4.3 A1 冻结形状）。
// GET /api/commission/rates?p=&page_size=
// items 为 CommissionRateDetail（CommissionRate 扁平嵌入 + distributor username 冗余展示）。
func GetCommissionRates(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.ListRates(pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// SetCommissionRate A2 设置比例（AdminAuth，契约 §4.3 A2 + §3.2 同事务历史冻结）。
// PUT /api/commission/rates/:distributorId  请求体 {"rate_bp": 500}
//
// 校验链（失败即 return，T-03-02-02 不信任任何客户端值）：
//  1. :distributorId strconv.Atoi 解析失败 → HTTP 400（distributor.go 先例）
//  2. rate_bp ∈ [0,10000] 越界 → 400 语义（RATE-01：万分比服务端强制）
//  3. 目标用户存在且 Role == RoleDistributorUser（精确 ==，AdminBindAttribution 模式，
//     禁阈值式——admin=10/root=100 不是分销商）→ 400 语义
//  4. 同事务写 rate + history（先 rate 后 history，RESEARCH Anti-Pattern）；
//     operator 取 session id（T-03-02-03 审计留痕）
func SetCommissionRate(c *gin.Context) {
	// 1. 路径参数解析（注意冻结参数名是 :distributorId 非 :id）
	distributorId, err := strconv.Atoi(c.Param("distributorId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的分销商ID",
		})
		return
	}
	// 2. 请求体绑定 + 万分比范围校验（0~10000，服务端强制）
	var req struct {
		RateBp int `json:"rate_bp"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的请求参数",
		})
		return
	}
	if req.RateBp < 0 || req.RateBp > 10000 {
		common.ApiErrorMsg(c, "rate_bp 必须在 0~10000 之间（万分比）")
		return
	}
	// 3. 目标必须是分销商（精确 == RoleDistributorUser）
	target, err := model.GetUserById(distributorId, false)
	if err != nil || target.Role != common.RoleDistributorUser {
		common.ApiErrorMsg(c, "目标用户不是分销商")
		return
	}
	// 4. 同事务写 rate + history（契约 §3.2 冻结）
	operatorId := c.GetInt("id")
	if err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.UpsertRateWithHistory(tx, distributorId, req.RateBp, operatorId)
	}); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}

// GetCommissionRateHistory A3 比例变更历史（AdminAuth，契约 §4.3 A3，RATE-03）。
// GET /api/commission/rates/:distributorId/history?p=&page_size=
// items 为 CommissionRateHistory（operator/old/new/时间完整）。
func GetCommissionRateHistory(c *gin.Context) {
	distributorId, err := strconv.Atoi(c.Param("distributorId"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "无效的分销商ID",
		})
		return
	}
	pageInfo := common.GetPageQuery(c)
	items, total, err := model.GetRateHistories(distributorId, pageInfo.GetStartIdx(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
