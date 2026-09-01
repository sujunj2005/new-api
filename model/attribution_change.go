package model

// AttributionChange 归属变更审计表（契约 01-CONTRACT.md §2.3 冻结）。
// 覆盖 CUST-04（管理员补绑）与 ATTR-05（自助补绑）的审计留痕。
type AttributionChange struct {
	Id           int    `json:"id" gorm:"primaryKey"`
	UserId       int    `json:"user_id" gorm:"type:int;index:idx_attr_user"`              // 被变更归属的客户
	OldInviterId int    `json:"old_inviter_id" gorm:"type:int"`                           // 0 = 原无归属
	NewInviterId int    `json:"new_inviter_id" gorm:"type:int;index:idx_attr_distributor"`
	Source       string `json:"source" gorm:"type:varchar(20)"`                           // admin_bind | self_bind
	OperatorId   int    `json:"operator_id" gorm:"type:int"`                              // self_bind 时 = UserId
	Reason       string `json:"reason" gorm:"type:varchar(255)"`                          // admin_bind 必填；self_bind 记 "self"
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
}

// TableName 显式指定表名（契约 §2.3 冻结）。
func (AttributionChange) TableName() string { return "attribution_changes" }
