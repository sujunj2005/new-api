package controller

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// affRebindRequest 自助补绑请求体（契约 §4.2 B1）。
type affRebindRequest struct {
	AffCode string `json:"aff_code"`
}

// AffRebind B1 自助补绑 handler（UserAuth）。
// 事务内条件 UPDATE inviter_id=0 防并发重复绑定，窗口校验先行。
// 错误映射（契约 §4.2 B1 + plan 02-03 Task 1）：
//   - aff_code 为空 → 400 参数错误
//   - errInvalidAffCode / errNotDistributor → 400
//   - errAlreadyBound → 400（已有归属）
//   - errWindowExpired → 403（超窗口）
//   - errUserNotFound → 404
func AffRebind(c *gin.Context) {
	var req affRebindRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.AffCode == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgInvalidParams),
		})
		return
	}
	userId := c.GetInt("id")
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.SelfBindAttribution(tx, userId, req.AffCode)
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrInvalidAffCode),
			errors.Is(err, model.ErrNotDistributor),
			errors.Is(err, model.ErrAlreadyBound):
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
		case errors.Is(err, model.ErrWindowExpired):
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"message": err.Error(),
			})
		case errors.Is(err, model.ErrUserNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": err.Error(),
			})
		default:
			common.ApiError(c, err)
		}
		return
	}
	common.ApiSuccess(c, gin.H{"success": true})
}

// adminBindRequest 管理员补绑请求体（契约 §4.2 B2）。
type adminBindRequest struct {
	UserId       int    `json:"user_id"`
	DistributorId int   `json:"distributor_id"`
	Reason       string `json:"reason"`
}

// AdminBindAttribution B2 管理员补绑 handler（RootAuth）。
// F5 无条件覆盖：不受 inviter_id=0 约束，可对已有归属客户修正归属。
// 同事务写审计 AttributionChange{source: "admin_bind"}。
// reason 必填（契约 §4.2 B2 明确）。
func AdminBindAttribution(c *gin.Context) {
	var req adminBindRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgInvalidParams),
		})
		return
	}
	if req.UserId == 0 || req.DistributorId == 0 || req.Reason == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": i18n.T(c, i18n.MsgInvalidParams),
		})
		return
	}
	operatorId := c.GetInt("id")
	err := model.DB.Transaction(func(tx *gorm.DB) error {
		return model.AdminBindAttribution(tx, req.UserId, req.DistributorId, operatorId, req.Reason)
	})
	if err != nil {
		switch {
		case errors.Is(err, model.ErrUserNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": err.Error(),
			})
		case errors.Is(err, model.ErrDistributorNotFound):
			c.JSON(http.StatusNotFound, gin.H{
				"success": false,
				"message": err.Error(),
			})
		case errors.Is(err, model.ErrNotDistributor):
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"message": err.Error(),
			})
		default:
			common.ApiError(c, err)
		}
		return
	}
	common.ApiSuccess(c, gin.H{"success": true})
}

// GetAttributionChanges B3 归属审计查询 handler（AdminAuth）。
// 分页查询 AttributionChange 记录，支持按 user_id 过滤。
func GetAttributionChanges(c *gin.Context) {
	userId := 0
	if uidStr := c.Query("user_id"); uidStr != "" {
		if uid, err := strconv.Atoi(uidStr); err == nil {
			userId = uid
		}
	}
	pageInfo := common.GetPageQuery(c)
	changes, total, err := model.GetAttributionChanges(userId, pageInfo)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(changes)
	common.ApiSuccess(c, pageInfo)
}
