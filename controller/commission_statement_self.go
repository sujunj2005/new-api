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

// 分销商 self 对账单面（契约 01-CONTRACT.md §4.3 A10/A11 冻结形状，STMT-04/CUST-05）。
// 权限中间件（精确匹配 role==5）挂载于路由层注册处，本文件零权限字样（03-03 grep 纪律）。
// 数据面服务端收窄：distributor_id 强制取会话身份，禁信任何客户端参数（ROLE-02）。
// 查询函数原样复用 04-02 交付（model.ListStatements / GetStatementWithAdjustments / ListStatementItems）。

// GetSelfCommissionStatements A10 分销商账单列表（仅本人，STMT-04）。
// GET /api/commission/statements/self?p=&page_size=
func GetSelfCommissionStatements(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	// 服务端收窄（ROLE-02）：distributor_id 强制 = 当前登录分销商
	items, total, err := model.ListStatements(c.GetInt("id"), "", "", pageInfo.GetPage(), pageInfo.GetPageSize())
	if err != nil {
		common.ApiError(c, err)
		return
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

// GetSelfCommissionStatementItems A11 分销商账单明细（归属校验，越权 403；items 含客户 username 冗余，A6 同构）。
// GET /api/commission/statements/self/:id/items?p=&page_size=
func GetSelfCommissionStatementItems(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		common.ApiErrorMsg(c, "无效的账单ID")
		return
	}
	stmt, _, err := model.GetStatementWithAdjustments(int64(id))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			common.ApiErrorMsg(c, "账单不存在")
			return
		}
		common.ApiError(c, err)
		return
	}
	if stmt.DistributorId != c.GetInt("id") { // 归属校验（Pitfall 3：越权 403）
		c.JSON(http.StatusForbidden, gin.H{"success": false, "message": "无权访问该账单"})
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
