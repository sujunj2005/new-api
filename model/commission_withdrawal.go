package model

// 按单提现 C 域提现单表（契约 01-CONTRACT.md 附录 E1 冻结，v1.4）。
//
// 五态状态机（用户裁决 Q1=B）：pending → reviewing → approved → paid / rejected；
// 驳回仅可从 reviewing 发起（Q2=B）；rejected/paid 为终态。
// 事务函数（Create/Accept/Reject/Approve/PaidTx）见本文件后半部（五迁移白名单条件更新）。

// Withdrawal Status 枚举（附录 E1 冻结五值）。
const (
	WithdrawalPending   = "pending"   // 申请中（D-04）
	WithdrawalReviewing = "reviewing" // 审核中（受理后）
	WithdrawalApproved  = "approved"  // 已批准待打款（Q1=B 新增态）
	WithdrawalPaid      = "paid"      // 已打款（终态，凭证号必填）
	WithdrawalRejected  = "rejected"  // 已驳回（终态，原因必填，D-06）
)

// CommissionWithdrawal 提现单表（附录 E1 冻结形状）。
//
// 无金额字段（D-08）：申请金额 = statement.settle_amount_cents，查询经 JOIN 实时取值；
// 时间线五列（created_at/reviewed_at/approved_at/paid_at/rejected_at）支撑 SC4 流转可查。
type CommissionWithdrawal struct {
	Id            int64  `json:"id" gorm:"primaryKey"`
	WithdrawalNo  string `json:"withdrawal_no" gorm:"type:varchar(64);uniqueIndex:uk_wd_no,unique"` // WD-{yyyyMMddHHmmss}-{4位随机}
	StatementId   int64  `json:"statement_id" gorm:"type:bigint;index:idx_wd_statement"`
	DistributorId int    `json:"distributor_id" gorm:"type:int;index:idx_wd_distributor"`
	Status        string `json:"status" gorm:"type:varchar(20);index:idx_wd_status"`
	Reason        string `json:"reason" gorm:"type:varchar(255)"`       // 驳回原因（仅 rejected 必填）
	VoucherNo     string `json:"voucher_no" gorm:"type:varchar(255)"`   // 打款凭证号（仅 paid 必填）
	OperatorId    int    `json:"operator_id" gorm:"type:int;default:0"` // 最后操作管理员（申请时 0）
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"` // 即申请时间
	ReviewedAt    int64  `json:"reviewed_at" gorm:"column:reviewed_at"`
	ApprovedAt    int64  `json:"approved_at" gorm:"column:approved_at"`
	PaidAt        int64  `json:"paid_at" gorm:"column:paid_at"`
	RejectedAt    int64  `json:"rejected_at" gorm:"column:rejected_at"`
}

// TableName 显式指定表名（附录 E1 冻结）。
func (CommissionWithdrawal) TableName() string { return "commission_withdrawals" }
