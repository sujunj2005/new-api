package model

import (
	"time"
)

// 分销商佣金仪表盘聚合面（06-01，契约 01-CONTRACT.md §4.3 A9/A12 冻结形状 + v1.5 附录 F2 笔数扩展）。
// 全部数值服务端聚合（int64 cents，契约 §3.0 精度决策），前端零金额二次计算（D-04 用户裁决）。
// 四项口径（OQ-1/D-09 裁决）与四卡对账恒等式：已出账 = 已提现 + 待提现 + 提现中。
// 本文件为 A9 聚合唯一落点（聚合 SQL 禁写入 distributor.go，保持既有聚合点计数唯一）。

// CommissionDashboard A9 响应体：§4.3 四冻结 cents 字段（名称逐字不动）+ 附录 F2 四笔数加法式扩展。
type CommissionDashboard struct {
	CurrentPendingCents  int64 `json:"current_pending_cents"`
	TotalStatementCents  int64 `json:"total_statement_cents"`
	TotalWithdrawnCents  int64 `json:"total_withdrawn_cents"`
	PendingWithdrawCents int64 `json:"pending_withdraw_cents"`

	CurrentPendingCount  int64 `json:"current_pending_count"`
	TotalStatementCount  int64 `json:"total_statement_count"`
	TotalWithdrawnCount  int64 `json:"total_withdrawn_count"`
	PendingWithdrawCount int64 `json:"pending_withdraw_count"`
}

// dashboardAggregateRow 聚合查询行：金额合计 + 笔数（合计列空值包裹，禁 NULL 序列化）。
type dashboardAggregateRow struct {
	Total int64
	Cnt   int64
}

// GetCommissionDashboard A9 四项独立聚合（逐项可读、断言直观；B4 聚合先例模式平移）。
// 状态谓词一律状态常量禁字符串字面量；JOIN 串静态字面量禁拼接输入（T-06-01-02）。
// ① 当期未出账：仅未出账状态流水按佣金流水金额求和（负数冲销自然抵减；与 A12 同谓词绑死）
// ② 历史已出账：全部状态账单按结算额汇总（含调整单最终应付，OQ-1 settle 口径）
// ③ 已提现：仅已打款提现单按关联账单结算额汇总（金额实时取账单，附录 E D-08 无金额列）
// ④ 待提现：仅应付状态账单按结算额汇总
func GetCommissionDashboard(distributorId int) (*CommissionDashboard, error) {
	d := &CommissionDashboard{}

	// ① 当期未出账
	var flowRow dashboardAggregateRow
	if err := DB.Model(&CommissionFlow{}).
		Select("COALESCE(SUM(amount_cents),0) AS total, COUNT(*) AS cnt").
		Where("distributor_id = ? AND status = ?", distributorId, CommissionFlowPending).
		Scan(&flowRow).Error; err != nil {
		return nil, err
	}
	d.CurrentPendingCents = flowRow.Total
	d.CurrentPendingCount = flowRow.Cnt

	// ② 历史已出账（全状态账单，settle 口径）
	var stmtRow dashboardAggregateRow
	if err := DB.Model(&CommissionStatement{}).
		Select("COALESCE(SUM(settle_amount_cents),0) AS total, COUNT(*) AS cnt").
		Where("distributor_id = ?", distributorId).
		Scan(&stmtRow).Error; err != nil {
		return nil, err
	}
	d.TotalStatementCents = stmtRow.Total
	d.TotalStatementCount = stmtRow.Cnt

	// ③ 已提现（已打款提现单 JOIN 账单取结算额）
	var wdRow dashboardAggregateRow
	if err := DB.Model(&CommissionWithdrawal{}).
		Select("COALESCE(SUM(commission_statements.settle_amount_cents),0) AS total, COUNT(*) AS cnt").
		Joins("JOIN commission_statements ON commission_statements.id = commission_withdrawals.statement_id").
		Where("commission_withdrawals.distributor_id = ? AND commission_withdrawals.status = ?", distributorId, WithdrawalPaid).
		Scan(&wdRow).Error; err != nil {
		return nil, err
	}
	d.TotalWithdrawnCents = wdRow.Total
	d.TotalWithdrawnCount = wdRow.Cnt

	// ④ 待提现（仅应付状态账单）
	var payableRow dashboardAggregateRow
	if err := DB.Model(&CommissionStatement{}).
		Select("COALESCE(SUM(settle_amount_cents),0) AS total, COUNT(*) AS cnt").
		Where("distributor_id = ? AND status = ?", distributorId, StatementPayable).
		Scan(&payableRow).Error; err != nil {
		return nil, err
	}
	d.PendingWithdrawCents = payableRow.Total
	d.PendingWithdrawCount = payableRow.Cnt

	return d, nil
}

// CurrentPendingSummary A12 响应体（§4.3 三冻结字段逐字，附录 F3 零扩展确认）。
type CurrentPendingSummary struct {
	Period       string `json:"period"`
	PendingFlows int64  `json:"pending_flows"`
	PendingCents int64  `json:"pending_cents"`
}

// GetCurrentPending A12 未出账实时预览（与 A9-① 同谓词同源；period 取服务器当前自然月，
// 时区基线 docker-compose TZ=Asia/Shanghai，commission_manual.go 先例）。
func GetCurrentPending(distributorId int) (*CurrentPendingSummary, error) {
	var row dashboardAggregateRow
	if err := DB.Model(&CommissionFlow{}).
		Select("COALESCE(SUM(amount_cents),0) AS total, COUNT(*) AS cnt").
		Where("distributor_id = ? AND status = ?", distributorId, CommissionFlowPending).
		Scan(&row).Error; err != nil {
		return nil, err
	}
	return &CurrentPendingSummary{
		Period:       time.Now().Format("2006-01"),
		PendingFlows: row.Cnt,
		PendingCents: row.Total,
	}, nil
}
