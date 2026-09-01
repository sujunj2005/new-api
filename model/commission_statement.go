package model

// CommissionStatement 对账单表（契约 01-CONTRACT.md §3.3 冻结）。
// uk_stmt_period 唯一索引防重出账；生成即锁定（STMT-03 immutable）。
type CommissionStatement struct {
	Id                   int64  `json:"id" gorm:"primaryKey"`
	DistributorId        int    `json:"distributor_id" gorm:"type:int;uniqueIndex:uk_stmt_period,unique"`
	Period               string `json:"period" gorm:"type:varchar(7);uniqueIndex:uk_stmt_period,unique"` // "2026-08"，唯一索引防重出账
	TotalTopupCents      int64  `json:"total_topup_cents" gorm:"type:bigint"`
	TotalCommissionCents int64  `json:"total_commission_cents" gorm:"type:bigint"`
	AdjustedCents        int64  `json:"adjusted_cents" gorm:"type:bigint;default:0"` // 调整单累计差额（COMM-06）
	SettleAmountCents    int64  `json:"settle_amount_cents" gorm:"type:bigint"`      // = TotalCommission + Adjusted，按此金额结算
	Status               string `json:"status" gorm:"type:varchar(20);index:idx_stmt_status"`
	CreatedAt            int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"` // 即出账时间
	LockedAt             int64  `json:"locked_at" gorm:"column:locked_at"`                  // 生成即锁定（STMT-03，immutable）
}

// CommissionStatementItem 对账单明细表（契约 §3.3 冻结，STMT-02）。
type CommissionStatementItem struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	StatementId     int64  `json:"statement_id" gorm:"type:bigint;index:idx_stmt_item_stmt"`
	FlowId          int64  `json:"flow_id" gorm:"type:bigint"`                 // 关联 commission_flows.id（审计回溯）
	BillingNo       string `json:"billing_no" gorm:"type:varchar(64)"`         // 充值原始信息（STMT-02）
	PaymentMethod   string `json:"payment_method" gorm:"type:varchar(50)"`     // 快照自 TopUp
	PaymentProvider string `json:"payment_provider" gorm:"type:varchar(50)"`
	TopupMoneyCents int64  `json:"topup_money_cents" gorm:"type:bigint"`
	CommissionCents int64  `json:"commission_cents" gorm:"type:bigint"`
	CustomerId      int    `json:"customer_id" gorm:"type:int"`                // 客户标识
	CompleteTime    int64  `json:"complete_time" gorm:"column:complete_time"`  // 到账时间
}

// StatementAdjustment 对账单调整单（契约 §3.3 冻结，COMM-06）。
// 仅 status=payable 可开（settled 拒绝，COMM-07）；同事务累计 statement.adjusted_cents。
type StatementAdjustment struct {
	Id          int64  `json:"id" gorm:"primaryKey"`
	StatementId int64  `json:"statement_id" gorm:"type:bigint;index:idx_stmt_adj_stmt"`
	DeltaCents  int64  `json:"delta_cents" gorm:"type:bigint"` // 正负差额补正（COMM-06）
	Reason      string `json:"reason" gorm:"type:varchar(255)"` // 必填
	OperatorId  int    `json:"operator_id" gorm:"type:int"`     // 仅 RootAuth
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
}

// Statement Status 枚举（契约 §3.3 冻结）。
const (
	StatementPayable     = "payable"     // 应付
	StatementWithdrawing = "withdrawing" // 提现中（C 域提现申请后置位，WDRAW-04）
	StatementSettled     = "settled"     // 已结清（C 域打款后置位）
)

// TableName 显式指定表名（契约 §3.3 冻结）。
func (CommissionStatement) TableName() string { return "commission_statements" }

// TableName 显式指定表名（契约 §3.3 冻结）。
func (CommissionStatementItem) TableName() string { return "commission_statement_items" }

// TableName 显式指定表名（契约 §3.3 冻结）。
func (StatementAdjustment) TableName() string { return "commission_statement_adjustments" }
