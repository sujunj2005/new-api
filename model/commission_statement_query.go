package model

// 对账单查询面（04-02，契约 01-CONTRACT.md §4.3 A4/A5/A6 + 附录 D3 A14 冻结形状）。
// 金额全程 int64 cents（契约 §3.0 禁浮点运算）；聚合谓词不依赖时间列（与 04-01 A1 裁决一致）。

// StatementItemDetail A6 明细列表项：CommissionStatementItem 扁平嵌入 + 客户 username 冗余
// （契约 §4.3 A6；JOIN 先例 model/commission_rate.go ListRates）。
// RateBp 为 v1.5 附录 F1 增补：比例快照（万分比），无关联流水时为 0。
type StatementItemDetail struct {
	CommissionStatementItem
	Username string `json:"username"`
	RateBp   int    `json:"rate_bp"`
}

// ListStatements A4 管理员账单列表分页（契约 §4.3 A4）。
// 三过滤参数可选：distributorId=0 / period、status 空串时跳过该谓词。
// 排序 period DESC, distributor_id ASC（新期在前、同期按分销商）；COUNT 与查询同谓词。
func ListStatements(distributorId int, period, status string, page, pageSize int) ([]CommissionStatement, int64, error) {
	query := DB.Model(&CommissionStatement{})
	if distributorId != 0 {
		query = query.Where("distributor_id = ?", distributorId)
	}
	if period != "" {
		query = query.Where("period = ?", period)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var statements []CommissionStatement
	if err := query.Order("period DESC, distributor_id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&statements).Error; err != nil {
		return nil, 0, err
	}
	return statements, total, nil
}

// GetStatementWithAdjustments A5 账单详情：本体 + adjustments 数组（契约 §4.3 A5）。
// ErrRecordNotFound 原样透传，由 controller 判空转业务语义。
func GetStatementWithAdjustments(id int64) (*CommissionStatement, []StatementAdjustment, error) {
	var stmt CommissionStatement
	if err := DB.First(&stmt, "id = ?", id).Error; err != nil {
		return nil, nil, err
	}
	var adjustments []StatementAdjustment
	if err := DB.Where("statement_id = ?", id).Order("id ASC").Find(&adjustments).Error; err != nil {
		return nil, nil, err
	}
	return &stmt, adjustments, nil
}

// ListStatementItems A6 账单明细分页（契约 §4.3 A6）。
// LEFT JOIN users 冗余客户 username（customer_id 关联）；v1.5 附录 F1 增补
// 关联佣金流水表冗余比例快照（flow_id 关联，无匹配行时为 0）。
// 干净 COUNT + 分页，按 id ASC；一处改动 A6/A11 双端点同形状生效。
func ListStatementItems(statementId int64, page, pageSize int) ([]StatementItemDetail, int64, error) {
	var total int64
	if err := DB.Model(&CommissionStatementItem{}).
		Where("statement_id = ?", statementId).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var details []StatementItemDetail
	if err := DB.Model(&CommissionStatementItem{}).
		Select("commission_statement_items.*, users.username, commission_flows.rate_bp").
		Joins("LEFT JOIN users ON users.id = commission_statement_items.customer_id").
		Joins("LEFT JOIN commission_flows ON commission_flows.id = commission_statement_items.flow_id").
		Where("commission_statement_items.statement_id = ?", statementId).
		Order("commission_statement_items.id ASC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&details).Error; err != nil {
		return nil, 0, err
	}
	return details, total, nil
}

// StatementSummaryItem A14 汇总列表项（契约附录 D3 冻结字段名）。
type StatementSummaryItem struct {
	DistributorId        int    `json:"distributor_id"`
	Username             string `json:"username"`
	TotalCommissionCents int64  `json:"total_commission_cents"`
	AdjustedCents        int64  `json:"adjusted_cents"`
	StatementStatus      string `json:"statement_status"`
	StatementId          int64  `json:"statement_id"`
}

// StatementSummaryTotals A14 汇总合计（Go 侧累加，D3）。
type StatementSummaryTotals struct {
	TotalCommissionCents int64 `json:"total_commission_cents"`
	AdjustedCents        int64 `json:"adjusted_cents"`
	DistributorCount     int   `json:"distributor_count"`
}

// StatementSummary A14 汇总响应体（契约附录 D3 冻结形状）。
type StatementSummary struct {
	Period string                 `json:"period"`
	Items  []StatementSummaryItem `json:"items"`
	Totals StatementSummaryTotals `json:"totals"`
}

// SummarizeStatements A14 跨分销商汇总（契约附录 D3：GROUP BY 聚合该期全部对账单）。
// uk_stmt_period 保证每 (distributor, period) 至多一行，故逐行即逐分销商；
// 明细列直接取 statement 快照列（无需二次聚合），totals 在 Go 侧累加（int64 精确）。
// 按 total_commission_cents DESC 排序（大头在前，管理端报表惯例）。
func SummarizeStatements(period string) (*StatementSummary, error) {
	// 显式 make 空 slice：空期时 JSON 序列化为 [] 而非 null（契约附录 D3 冻结「items 空数组」）
	items := make([]StatementSummaryItem, 0)
	if err := DB.Model(&CommissionStatement{}).
		Select("commission_statements.distributor_id, users.username, commission_statements.total_commission_cents, commission_statements.adjusted_cents, commission_statements.status AS statement_status, commission_statements.id AS statement_id").
		Joins("LEFT JOIN users ON users.id = commission_statements.distributor_id").
		Where("commission_statements.period = ?", period).
		Order("commission_statements.total_commission_cents DESC").
		Scan(&items).Error; err != nil {
		return nil, err
	}
	summary := &StatementSummary{Period: period, Items: items}
	for _, it := range items {
		summary.Totals.TotalCommissionCents += it.TotalCommissionCents
		summary.Totals.AdjustedCents += it.AdjustedCents
	}
	summary.Totals.DistributorCount = len(items)
	return summary, nil
}
