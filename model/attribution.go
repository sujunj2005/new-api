package model

import (
	"errors"
	"time"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

// 归属管理错误变量（契约 01-CONTRACT.md §4.2 B1/B2 + §5 幂等与并发约定）。
// controller 层据 sentinel 错误映射 HTTP 状态码：
//   - ErrInvalidAffCode / ErrNotDistributor / ErrAlreadyBound → 400
//   - ErrWindowExpired → 403
//   - ErrUserNotFound / ErrDistributorNotFound → 404
var (
	ErrInvalidAffCode    = errors.New("aff code not found")
	ErrNotDistributor    = errors.New("aff code owner is not a distributor")
	ErrAlreadyBound      = errors.New("user already has an inviter")
	ErrWindowExpired     = errors.New("rebind window expired")
	ErrUserNotFound      = errors.New("user not found")
	ErrDistributorNotFound = errors.New("distributor not found")
)

// CanRebind 判断客户是否仍在自助补绑窗口内（契约 §2.4 + §4.1 M2）。
// 纯函数：仅依赖入参与 time.Now()，不访问 DB 或全局可变状态。
//   - mode=="unlimited" → 恒 true（任何时候仅受 inviter_id=0 约束）
//   - mode=="days" → userCreatedAt + days*86400 > now（窗口内可补绑）
//   - 其他 → false（防御性，不应出现于正常配置）
func CanRebind(userCreatedAt int64, mode string, days int) bool {
	switch mode {
	case "unlimited":
		return true
	case "days":
		if days <= 0 {
			return false
		}
		return userCreatedAt+int64(days)*86400 > time.Now().Unix()
	default:
		return false
	}
}

// SelfBindAttribution 在事务内执行自助补绑（契约 §4.2 B1 + §5 幂等与并发约定）。
// 防重双道：
//  1. 预查 InviterId != 0 → ErrAlreadyBound（快速拒绝，减少条件 UPDATE 竞争）
//  2. 条件 UPDATE inviter_id=0 → affected=0 即 ErrAlreadyBound（并发防重最终防线）
//
// 窗口校验先行（days 模式 created_at + days*86400 > now，unlimited 恒通过）。
// 邀请码非分销商 → ErrNotDistributor（防止绑定到普通邀请人冒充分销商）。
// 成功同事务写 AttributionChange{source: "self_bind"}。
//
// 注意：此路径与 AdminBindAttribution 物理隔离——自助侧走条件 UPDATE（防重），
// 管理员侧走无条件 UPDATE（F5 可覆盖已有归属）。
func SelfBindAttribution(tx *gorm.DB, userId int, affCode string) error {
	if affCode == "" {
		return ErrInvalidAffCode
	}
	// 1. 邀请码对应用户
	inviterId, err := GetUserIdByAffCode(affCode)
	if err != nil || inviterId == 0 {
		return ErrInvalidAffCode
	}
	// 2. 校验邀请人为分销商（防止绑定到普通邀请人）
	var inviter User
	if e := tx.Select("id, role").First(&inviter, "id = ?", inviterId).Error; e != nil {
		return ErrInvalidAffCode
	}
	if inviter.Role != common.RoleDistributorUser {
		return ErrNotDistributor
	}
	// 3. 查当前用户（获取 CreatedAt + InviterId）
	var user User
	if e := tx.First(&user, "id = ?", userId).Error; e != nil {
		return ErrUserNotFound
	}
	// 4. 防重第一道：预查已有归属
	if user.InviterId != 0 {
		return ErrAlreadyBound
	}
	// 5. 窗口校验先行
	if !CanRebind(user.CreatedAt, common.AffRebindWindowMode, common.AffRebindWindowDays) {
		return ErrWindowExpired
	}
	// 6. 条件 UPDATE（防并发重复绑定，affected=0 → 已被并发抢绑）
	res := tx.Model(&User{}).
		Where("id = ? AND inviter_id = 0", userId).
		Update("inviter_id", inviterId)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrAlreadyBound
	}
	// 7. 同事务审计（source=self_bind，operator_id=userId）
	return tx.Create(&AttributionChange{
		UserId:       userId,
		OldInviterId: 0,
		NewInviterId: inviterId,
		Source:       "self_bind",
		OperatorId:   userId,
		Reason:       "self",
	}).Error
}

// AdminBindAttribution 在事务内执行管理员补绑（契约 §4.2 B2 + §5 F5 无条件覆盖）。
// 与 SelfBindAttribution 物理隔离：自助侧走条件 UPDATE inviter_id=0（防重），
// 管理员侧走无条件 UPDATE（F5，可覆盖已有归属，Old/New 全记录审计）。
// 校验：目标用户存在 + distributor 存在 + distributor.Role==5。
func AdminBindAttribution(tx *gorm.DB, userId int, distributorId int, operatorId int, reason string) error {
	var user User
	if err := tx.First(&user, "id = ?", userId).Error; err != nil {
		return ErrUserNotFound
	}
	var distributor User
	if err := tx.First(&distributor, "id = ?", distributorId).Error; err != nil {
		return ErrDistributorNotFound
	}
	if distributor.Role != common.RoleDistributorUser {
		return ErrNotDistributor
	}
	oldInviterId := user.InviterId
	if err := tx.Model(&User{}).Where("id = ?", userId).Update("inviter_id", distributorId).Error; err != nil {
		return err
	}
	return tx.Create(&AttributionChange{
		UserId:       userId,
		OldInviterId: oldInviterId,
		NewInviterId: distributorId,
		Source:       "admin_bind",
		OperatorId:   operatorId,
		Reason:       reason,
	}).Error
}

// GetAttributionChanges 分页查询归属变更审计记录（契约 §4.2 B3）。
func GetAttributionChanges(userId int, page *common.PageInfo) ([]AttributionChange, int64, error) {
	var changes []AttributionChange
	var total int64
	query := DB.Model(&AttributionChange{})
	if userId != 0 {
		query = query.Where("user_id = ?", userId)
	}
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err := query.Order("id desc").Limit(page.GetPageSize()).Offset(page.GetStartIdx()).Find(&changes).Error; err != nil {
		return nil, 0, err
	}
	return changes, total, nil
}
