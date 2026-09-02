package model

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"
)

// 对账单调整单（契约 01-CONTRACT.md §4.3 A8 + §3.3 不可变规则 + 附录 D5.1，COMM-06/07）。
//
// 事务语义（RESEARCH Example 3 权威骨架）：statement 行 FOR UPDATE 串行化并发调整 →
// 白名单 status==payable（withdrawing/settled 一并拒绝，COMM-07；禁阈值式 == settled
// 判定，未来新增枚举值由白名单默认拒绝）→ 同事务 INSERT adjustment + 累计
// adjusted_cents 并重算 settle_amount_cents（服务端计算，不信任客户端；行锁下先读后算安全）。
//
// 原始账单不变（COMM-06）：本文件唯一写列为 adjusted_cents 与 settle_amount_cents，
// Total 字段与明细零触碰——不可变经由「无写路径」实现（§3.3 冻结，§3.3 唯一合法写 =
// adjusted_cents 与 status）。
//
// 门控语义（附录 D5.1 冻结）：调整入口不受分佣总开关门控——纠错是账本完整性操作；
// 本函数零门控分支。

// CreateStatementAdjustmentTx 对 payable 账单开调整单并同事务累计差额。
// reason 双层校验的 model 侧保险（controller 侧先行校验）。
func CreateStatementAdjustmentTx(statementId int64, deltaCents int64, reason string, operatorId int) error {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("调整原因必填")
	}
	if utf8.RuneCountInString(reason) > 255 {
		return errors.New("调整原因长度不能超过 255 字符")
	}
	if deltaCents == 0 {
		return errors.New("调整差额不能为零（无意义调整拒绝）")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		stmt := &CommissionStatement{}
		// statement 行锁：串行化同账单并发调整（T-04-03-07）
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(stmt, "id = ?", statementId).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.New("账单不存在")
			}
			return err
		}
		// 白名单：仅 payable 放行（Pitfall 6/COMM-07），文案区分状态便于审计阅读
		if stmt.Status != StatementPayable {
			switch stmt.Status {
			case StatementWithdrawing:
				return errors.New("账单提现中，禁止开调整单")
			case StatementSettled:
				return errors.New("账单已结清，禁止开调整单（差错经人工补录下期体现）")
			default:
				return fmt.Errorf("账单状态 %q 不可开调整单（仅应付状态可调）", stmt.Status)
			}
		}
		if err := tx.Create(&StatementAdjustment{
			StatementId: statementId,
			DeltaCents:  deltaCents,
			Reason:      reason,
			OperatorId:  operatorId,
		}).Error; err != nil {
			return err
		}
		newAdjusted := stmt.AdjustedCents + deltaCents
		return tx.Model(&CommissionStatement{}).Where("id = ?", statementId).
			Updates(map[string]any{
				"adjusted_cents":      newAdjusted,
				"settle_amount_cents": stmt.TotalCommissionCents + newAdjusted, // 按调整后金额结算
			}).Error
	})
}
