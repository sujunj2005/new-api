package model

// CommissionFlow 佣金流水表（契约 01-CONTRACT.md §3.1 冻结）。
// 金额一律 int64 cents，比例 int 万分比（§3.0 精度决策）。
type CommissionFlow struct {
	Id              int64  `json:"id" gorm:"primaryKey"`
	BillingNo       string `json:"billing_no" gorm:"type:varchar(64);uniqueIndex:uk_flow_billing,unique"`  // 幂等键，见契约 §3.1.1
	FlowType        string `json:"flow_type" gorm:"type:varchar(20);uniqueIndex:uk_flow_billing,unique"`   // commission | reversal | manual_credit | manual_debit
	TradeNo         string `json:"trade_no" gorm:"type:varchar(255);index:idx_flow_trade"`                 // 关联充值单号；人工补录为空
	CustomerId      int    `json:"customer_id" gorm:"type:int;index:idx_flow_customer"`
	DistributorId   int    `json:"distributor_id" gorm:"type:int;index:idx_flow_distributor"`
	TopupMoneyCents int64  `json:"topup_money_cents" gorm:"type:bigint"`                                 // 充值金额快照（分）
	RateBp          int    `json:"rate_bp" gorm:"type:int"`                                              // 比例快照（万分比，RATE-02）
	AmountCents     int64  `json:"amount_cents" gorm:"type:bigint"`                                      // 本流水佣金金额（分）；冲销为负数
	Status          string `json:"status" gorm:"type:varchar(20);index:idx_flow_status"`                 // pending | available
	Period          string `json:"period" gorm:"type:varchar(7);index:idx_flow_period"`                  // 账期 "2026-08"（按到账时间所在自然月）
	StatementId     int64  `json:"statement_id" gorm:"type:bigint;default:0;index:idx_flow_statement"`   // 出账时回填
	RelatedFlowId   int64  `json:"related_flow_id" gorm:"type:bigint;default:0"`                         // 冲销指向的原流水 Id
	Reason          string `json:"reason" gorm:"type:varchar(255)"`                                      // 冲销/补录原因（必填场景）
	OperatorId      int    `json:"operator_id" gorm:"type:int;default:0"`                                // 人工操作者；系统流水为 0
	CreatedAt       int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"`
}

// FlowType 枚举（契约 §3.1 冻结）。
const (
	CommissionFlowCommission   = "commission"    // 正向佣金（充值到账产生）
	CommissionFlowReversal     = "reversal"      // 负数冲销（退款/作废产生）
	CommissionFlowManualCredit = "manual_credit" // 超管人工补录（COMM-05）
	CommissionFlowManualDebit  = "manual_debit"  // 超管人工冲销（COMM-05）

	CommissionFlowPending   = "pending"   // 未出账
	CommissionFlowAvailable = "available" // 已进入对账单
)

// TableName 显式指定表名（契约 §3.1 冻结）。
func (CommissionFlow) TableName() string { return "commission_flows" }
