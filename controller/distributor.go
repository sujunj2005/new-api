package controller

import (
	"fmt"
	"math"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// distributorCustomerItem B4 客户列表项（契约 §4.2 B4）。
type distributorCustomerItem struct {
	Id                   int    `json:"id"`
	Username             string `json:"username"`
	DisplayName          string `json:"display_name"`
	CreatedAt            int64  `json:"created_at"`
	TotalTopupCents      int64  `json:"total_topup_cents"`
	TotalCommissionCents int64  `json:"total_commission_cents"`
}

// GetDistributorCustomers B4 分销商客户列表（DistributorAuth）。
// 服务端强制 WHERE inviter_id = 当前分销商（ROLE-02，不依赖前端隔离）。
// total_topup_cents 从 TopUp.Money 聚合（SUM*100，F4 佣金基数语义）。
// total_commission_cents 返回 0 占位（A 域流水 Phase 3 前联调点，Open Questions #3 确认）。
func GetDistributorCustomers(c *gin.Context) {
	distributorId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)

	// 服务端强制归属过滤（ROLE-02）
	var users []model.User
	var total int64
	if err := model.DB.Model(&model.User{}).
		Where("inviter_id = ?", distributorId).
		Count(&total).Error; err != nil {
		common.ApiError(c, err)
		return
	}
	if err := model.DB.Where("inviter_id = ?", distributorId).
		Order("id desc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&users).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	// 逐客户聚合 total_topup_cents（SUM(money)*100，F4 佣金基数=实付金额）
	items := make([]distributorCustomerItem, 0, len(users))
	for _, u := range users {
		var totalMoney float64
		model.DB.Model(&model.TopUp{}).
			Where("user_id = ? AND status = ?", u.Id, common.TopUpStatusSuccess).
			Select("COALESCE(SUM(money), 0)").
			Scan(&totalMoney)
		items = append(items, distributorCustomerItem{
			Id:                   u.Id,
			Username:             u.Username,
			DisplayName:          u.DisplayName,
			CreatedAt:            u.CreatedAt,
			TotalTopupCents:      int64(math.Round(totalMoney * 100)),
			TotalCommissionCents: 0, // 占位：A 域流水 Phase 3 前联调点
		})
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetDistributorCustomerTopups B5 单客户充值明细（DistributorAuth）。
// IDOR 防护：服务端校验目标客户 inviter_id == 当前分销商，否则 403（契约 §4.2 B5）。
func GetDistributorCustomerTopups(c *gin.Context) {
	distributorId := c.GetInt("id")
	customerId, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "invalid customer id",
		})
		return
	}

	// IDOR 防护：校验目标客户归属当前分销商
	var customer model.User
	if err := model.DB.Select("id, inviter_id").First(&customer, "id = ?", customerId).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "customer not found",
		})
		return
	}
	if customer.InviterId != distributorId {
		c.JSON(http.StatusForbidden, gin.H{
			"success": false,
			"message": "access denied: customer does not belong to this distributor",
		})
		return
	}

	pageInfo := common.GetPageQuery(c)
	var topups []*model.TopUp
	var total int64
	model.DB.Model(&model.TopUp{}).
		Where("user_id = ?", customerId).
		Count(&total)
	if err := model.DB.Where("user_id = ?", customerId).
		Order("id desc").
		Limit(pageInfo.GetPageSize()).
		Offset(pageInfo.GetStartIdx()).
		Find(&topups).Error; err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

// GetDistributorProfile B6 分销商自己的资料（DistributorAuth）。
// 返回 aff_code / aff_link / customer_count。
func GetDistributorProfile(c *gin.Context) {
	distributorId := c.GetInt("id")
	user, err := model.GetUserById(distributorId, false)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	var customerCount int64
	model.DB.Model(&model.User{}).
		Where("inviter_id = ?", distributorId).
		Count(&customerCount)

	affLink := fmt.Sprintf("https://%s/register?aff=%s", c.Request.Host, user.AffCode)
	common.ApiSuccess(c, gin.H{
		"aff_code":       user.AffCode,
		"aff_link":       affLink,
		"customer_count": customerCount,
	})
}
