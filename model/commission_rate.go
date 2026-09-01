package model

import (
	"errors"

	"gorm.io/gorm"
)

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

// GetRateByDistributorId 读取分销商当前生效比例（03-02，RESEARCH 冻结签名）。
// gorm.ErrRecordNotFound 原样上抛——调用方语义：无比例配置 = 未启用（契约 §7#3）。
// 供 A2/A9/A12 及后续 phase 使用；调用方负责在事务内传入 tx（Pitfall 2：禁用全局 DB）。
func GetRateByDistributorId(tx *gorm.DB, distributorId int) (*CommissionRate, error) {
	var rate CommissionRate
	if err := tx.First(&rate, "distributor_id = ?", distributorId).Error; err != nil {
		return nil, err
	}
	return &rate, nil
}

// UpsertRateWithHistory 比例变更 + 变更历史同事务写入（契约 §3.2 冻结：
// 更新当前值 + 插入历史行，保证 RATE-03 可查且不丢）。
// 调用方负责在 model.DB.Transaction 内传入 tx（A2 controller 事务骨架）。
//
// 顺序语义（RESEARCH Anti-Pattern 明令禁止颠倒）：先 rate 写入成功、后 history——
// history 是 rate 变更的从属记录；并发双写由 uk_rate_distributor 唯一索引兜底
// （契约 §5 冻结设计，不加应用层查重两步逻辑——TOCTOU 窗口）。
func UpsertRateWithHistory(tx *gorm.DB, distributorId, rateBp, operatorId int) error {
	// 1. 读旧行：ErrRecordNotFound → oldBp=0 且走 Create；其他 DB 错误上抛
	oldBp := 0
	var existing CommissionRate
	err := tx.First(&existing, "distributor_id = ?", distributorId).Error
	switch {
	case err == nil:
		oldBp = existing.RateBp
	case errors.Is(err, gorm.ErrRecordNotFound):
		// 首次设置：old=0
	default:
		return err
	}

	// 2. 先写 rate：有行 Updates（同一行），无行 Create（唯一索引兜底并发双写）
	if oldBp != 0 || existing.Id != 0 {
		if err := tx.Model(&CommissionRate{}).Where("distributor_id = ?", distributorId).
			Updates(map[string]interface{}{
				"rate_bp":    rateBp,
				"updated_by": operatorId,
			}).Error; err != nil {
			return err
		}
	} else {
		if err := tx.Create(&CommissionRate{
			DistributorId: distributorId,
			RateBp:        rateBp,
			UpdatedBy:     operatorId,
		}).Error; err != nil {
			return err
		}
	}

	// 3. 后写 history：rate 变更的从属记录（RATE-03 可审计性全部来源）
	return tx.Create(&CommissionRateHistory{
		DistributorId: distributorId,
		OldRateBp:     oldBp,
		NewRateBp:     rateBp,
		OperatorId:    operatorId,
	}).Error
}

// CommissionRateDetail A1 列表项：CommissionRate + distributor username 冗余（契约 §4.3 A1）。
// 匿名嵌入产生扁平 JSON：id/distributor_id/rate_bp/updated_by/updated_at/username。
type CommissionRateDetail struct {
	CommissionRate
	Username string `json:"username"`
}

// ListRates A1 比例列表分页（契约 §4.3 A1：items 含 distributor username 冗余展示）。
// 干净 count（不 JOIN）+ LEFT JOIN users 冗余 username，按 distributor_id ASC 排序。
func ListRates(startIdx, num int) ([]CommissionRateDetail, int64, error) {
	var total int64
	if err := DB.Model(&CommissionRate{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var details []CommissionRateDetail
	if err := DB.Model(&CommissionRate{}).
		Select("commission_rates.*, users.username").
		Joins("LEFT JOIN users ON users.id = commission_rates.distributor_id").
		Order("commission_rates.distributor_id ASC").
		Offset(startIdx).Limit(num).
		Find(&details).Error; err != nil {
		return nil, 0, err
	}
	return details, total, nil
}

// GetRateHistories A3 比例变更历史分页（契约 §4.3 A3，RATE-03）。
// 按 distributorId 过滤，id DESC（最新变更在前）。
func GetRateHistories(distributorId, startIdx, num int) ([]CommissionRateHistory, int64, error) {
	var total int64
	query := DB.Model(&CommissionRateHistory{}).Where("distributor_id = ?", distributorId)
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var hists []CommissionRateHistory
	if err := query.Order("id DESC").Offset(startIdx).Limit(num).Find(&hists).Error; err != nil {
		return nil, 0, err
	}
	return hists, total, nil
}
