package model

// CommissionRate 比例配置表（契约 01-CONTRACT.md §3.2 冻结）。
// 每分销商一行当前生效比例；变更历史在同一事务内写入 CommissionRateHistory（RATE-03）。
type CommissionRate struct {
	Id            int   `json:"id" gorm:"primaryKey"`
	DistributorId int   `json:"distributor_id" gorm:"type:int;uniqueIndex:uk_rate_distributor"` // 每分销商一行
	RateBp        int   `json:"rate_bp" gorm:"type:int"`            // 当前生效比例（万分比，0~10000）
	UpdatedBy     int   `json:"updated_by" gorm:"type:int"`
	UpdatedAt     int64 `json:"updated_at" gorm:"autoUpdateTime:milli;column:updated_at"`
}

// CommissionRateHistory 比例变更历史表（契约 §3.2 冻结）。
// 改比例不影响任何已生成流水（快照在流水侧，RATE-02）。
type CommissionRateHistory struct {
	Id            int   `json:"id" gorm:"primaryKey"`
	DistributorId int   `json:"distributor_id" gorm:"type:int;index:idx_rate_hist_distributor"`
	OldRateBp     int   `json:"old_rate_bp" gorm:"type:int"`
	NewRateBp     int   `json:"new_rate_bp" gorm:"type:int"`
	OperatorId    int   `json:"operator_id" gorm:"type:int"`
	CreatedAt     int64 `json:"created_at" gorm:"autoCreateTime;column:created_at"`
}

// TableName 显式指定表名（契约 §3.2 冻结）。
func (CommissionRate) TableName() string        { return "commission_rates" }

// TableName 显式指定表名（契约 §3.2 冻结）。
func (CommissionRateHistory) TableName() string { return "commission_rate_histories" }
