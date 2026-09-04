package model

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// 按单提现 C 域提现单（契约 01-CONTRACT.md 附录 E 冻结，v1.4；05-CONTEXT D-04~D-14）。
//
// 五态状态机（用户裁决 Q1=B）：pending → reviewing → approved → paid / rejected；
// 驳回仅可从 reviewing 发起（Q2=B）；rejected/paid 为终态。
//
// 防重设计（附录 E4 冻结，无任何唯一索引方案）：账单状态机条件更新
// （payable→withdrawing 同事务翻转 + RowsAffected==1 断言）+ statement 行
// FOR UPDATE 串行化——账单 status 本身就是 DB 级防重状态位；禁 statement_id
// 唯一索引（§6 禁 partial index 且全列唯一阻断 D-06 驳回后重新申请）。
//
// 写路径纪律：statement 行更新一律条件 Updates（前置态入 WHERE，禁整对象写回），
// RowsAffected != 1 → return error 整体回滚（禁半状态，Pitfall 2）；
// 驳回回退/打款结清与提现单迁移同事务（契约 L308 禁联动拆事务）。
// 门控语义（D-14/Q5=A）：提现全链路零门控分支——开关只管新记账。
// 锁纪律：statement 行锁为提现五写路径主锁（A8 母本平移）。

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
	Reason        string `json:"reason" gorm:"type:varchar(255)"`     // 驳回原因（仅 rejected 必填）
	VoucherNo     string `json:"voucher_no" gorm:"type:varchar(255)"` // 打款凭证号（仅 paid 必填）
	OperatorId    int    `json:"operator_id" gorm:"type:int;default:0"` // 最后操作管理员（申请时 0）
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime;column:created_at"` // 即申请时间
	ReviewedAt    int64  `json:"reviewed_at" gorm:"column:reviewed_at"`
	ApprovedAt    int64  `json:"approved_at" gorm:"column:approved_at"`
	PaidAt        int64  `json:"paid_at" gorm:"column:paid_at"`
	RejectedAt    int64  `json:"rejected_at" gorm:"column:rejected_at"`
}

// TableName 显式指定表名（附录 E1 冻结）。
func (CommissionWithdrawal) TableName() string { return "commission_withdrawals" }

// ErrWithdrawalNotOwner 账单归属 sentinel（controller 转 403 语义；T-05-01-01）。
var ErrWithdrawalNotOwner = errors.New("无权操作该账单")

// CreateWithdrawalTx A15 申请事务（WDRAW-01/SC1，RESEARCH Example 1 权威骨架平移）。
// ① statement 行 FOR UPDATE → ② 归属校验 → ③ payable 白名单（文案区分 Pitfall 6）→
// ④ INSERT pending（D-08 无金额字段）→ ⑤ 账单条件翻转 + RowsAffected 断言。
func CreateWithdrawalTx(statementId int64, distributorId int) (*CommissionWithdrawal, error) {
	var created *CommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		stmt := &CommissionStatement{}
		// ① statement 行锁：串行化同账单的申请/调账/驳回回退/打款结清（附录 E4）
		if err := tx.Set("gorm:query_option", "FOR UPDATE").First(stmt, "id = ?", statementId).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return errors.New("账单不存在")
			}
			return err
		}
		// ② 归属校验（WDRAW-01/CUST-05；中间件只保证「是分销商」，这里保证「是这行账单的分销商」）
		if stmt.DistributorId != distributorId {
			return ErrWithdrawalNotOwner
		}
		// ③ 白名单：仅 payable 放行（禁黑名单式判断；文案区分便于分销商理解）
		if stmt.Status != StatementPayable {
			switch stmt.Status {
			case StatementWithdrawing:
				return errors.New("账单提现中，不可重复申请")
			case StatementSettled:
				return errors.New("账单已结清")
			default:
				return fmt.Errorf("账单状态 %q 不可发起提现", stmt.Status)
			}
		}
		// ④ 建单（D-08：无金额字段，金额经 JOIN 实时取 statement）
		wd := &CommissionWithdrawal{
			WithdrawalNo:  fmt.Sprintf("WD-%s-%s", time.Now().Format("20060102150405"), common.GetRandomString(4)),
			StatementId:   statementId,
			DistributorId: distributorId,
			Status:        WithdrawalPending,
		}
		if err := tx.Create(wd).Error; err != nil {
			return err
		}
		// ⑤ 账单联动（D-05）：条件更新 + 断言，前置态入 WHERE 兼作白名单
		res := tx.Model(&CommissionStatement{}).
			Where("id = ? AND status = ?", statementId, StatementPayable).
			Updates(map[string]any{"status": StatementWithdrawing})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return fmt.Errorf("statement status flip mismatch: want 1 got %d", res.RowsAffected)
		}
		created = wd
		return nil
	})
	return created, err
}

// AcceptWithdrawalTx A18 受理（pending→reviewing，账单零操作——D-05 withdrawing 保持）。
func AcceptWithdrawalTx(id int64, operatorId int) (*CommissionWithdrawal, error) {
	var updated *CommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		wd, err := lockWithdrawal(tx, id)
		if err != nil {
			return err
		}
		if wd.Status != WithdrawalPending {
			return fmt.Errorf("提现单状态 %q 不可受理（仅申请中可受理）", wd.Status)
		}
		if err := updateWithdrawalStatus(tx, id, WithdrawalPending, map[string]any{
			"status":      WithdrawalReviewing,
			"reviewed_at": common.GetTimestamp(),
			"operator_id": operatorId,
		}); err != nil {
			return err
		}
		wd.Status = WithdrawalReviewing
		updated = wd
		return nil
	})
	return updated, err
}

// RejectWithdrawalTx A19 驳回（仅 reviewing 可发起，Q2=B；终态留原因 D-06）+
// 同事务账单 withdrawing→payable 回退（D-05，漏断言 = 永久 withdrawing 死单，Pitfall 2）。
// 返回迁移后的提现单行（controller 事务外审计嵌 withdrawal_no，与其他三个管理 Tx 签名一致）。
func RejectWithdrawalTx(id int64, reason string, operatorId int) (*CommissionWithdrawal, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("驳回原因必填") // model 侧保险（controller 已前置拦截）
	}
	if utf8.RuneCountInString(reason) > 255 {
		return nil, errors.New("驳回原因长度不能超过 255 字符")
	}
	var updated *CommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		wd, err := lockWithdrawal(tx, id)
		if err != nil {
			return err
		}
		if wd.Status != WithdrawalReviewing {
			return fmt.Errorf("仅审核中可驳回，请先受理（当前状态 %q）", wd.Status)
		}
		if err := updateWithdrawalStatus(tx, id, WithdrawalReviewing, map[string]any{
			"status":      WithdrawalRejected,
			"reason":      reason,
			"rejected_at": common.GetTimestamp(),
			"operator_id": operatorId,
		}); err != nil {
			return err
		}
		// 账单回退（D-05）：同事务条件更新 + 断言
		if err := flipStatementStatus(tx, wd.StatementId, StatementWithdrawing, StatementPayable); err != nil {
			return err
		}
		wd.Status = WithdrawalRejected
		wd.Reason = reason
		updated = wd
		return nil
	})
	return updated, err
}

// ApproveWithdrawalTx A20 批准（reviewing→approved，账单零操作——D-12 approved 期间 withdrawing 保持）。
func ApproveWithdrawalTx(id int64, operatorId int) (*CommissionWithdrawal, error) {
	var updated *CommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		wd, err := lockWithdrawal(tx, id)
		if err != nil {
			return err
		}
		if wd.Status != WithdrawalReviewing {
			return fmt.Errorf("提现单状态 %q 不可批准（仅审核中可批准）", wd.Status)
		}
		if err := updateWithdrawalStatus(tx, id, WithdrawalReviewing, map[string]any{
			"status":      WithdrawalApproved,
			"approved_at": common.GetTimestamp(),
			"operator_id": operatorId,
		}); err != nil {
			return err
		}
		wd.Status = WithdrawalApproved
		updated = wd
		return nil
	})
	return updated, err
}

// MarkWithdrawalPaidTx A21 打款登记（仅 approved 可发起，Q1=B；voucher_no 必填 ≤255）+
// 同事务账单 withdrawing→settled 结清（D-05）。
func MarkWithdrawalPaidTx(id int64, voucherNo string, operatorId int) (*CommissionWithdrawal, error) {
	voucherNo = strings.TrimSpace(voucherNo)
	if voucherNo == "" {
		return nil, errors.New("打款凭证号必填")
	}
	if utf8.RuneCountInString(voucherNo) > 255 {
		return nil, errors.New("打款凭证号长度不能超过 255 字符")
	}
	var updated *CommissionWithdrawal
	err := DB.Transaction(func(tx *gorm.DB) error {
		wd, err := lockWithdrawal(tx, id)
		if err != nil {
			return err
		}
		if wd.Status != WithdrawalApproved {
			return fmt.Errorf("提现单状态 %q 不可打款登记（仅已批准可打款）", wd.Status)
		}
		if err := updateWithdrawalStatus(tx, id, WithdrawalApproved, map[string]any{
			"status":      WithdrawalPaid,
			"voucher_no":  voucherNo,
			"paid_at":     common.GetTimestamp(),
			"operator_id": operatorId,
		}); err != nil {
			return err
		}
		// 账单结清（D-05）：同事务条件更新 + 断言
		if err := flipStatementStatus(tx, wd.StatementId, StatementWithdrawing, StatementSettled); err != nil {
			return err
		}
		wd.Status = WithdrawalPaid
		updated = wd
		return nil
	})
	return updated, err
}

// lockWithdrawal 提现单行锁（串行化同单并发管理动作：受理/驳回/批准/打款互斥）。
func lockWithdrawal(tx *gorm.DB, id int64) (*CommissionWithdrawal, error) {
	wd := &CommissionWithdrawal{}
	if err := tx.Set("gorm:query_option", "FOR UPDATE").First(wd, "id = ?", id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, errors.New("提现单不存在")
		}
		return nil, err
	}
	return wd, nil
}

// updateWithdrawalStatus 提现单条件迁移：前置态入 WHERE 兼作白名单，
// RowsAffected != 1 → 明确报错整体回滚（禁静默）。
func updateWithdrawalStatus(tx *gorm.DB, id int64, fromStatus string, updates map[string]any) error {
	res := tx.Model(&CommissionWithdrawal{}).
		Where("id = ? AND status = ?", id, fromStatus).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return errors.New("提现单状态已变化，请刷新后重试")
	}
	return nil
}

// flipStatementStatus 账单条件翻转（驳回回退/打款结清共用）：前置态入 WHERE +
// RowsAffected 断言，失败整体回滚（Pitfall 2 禁漏断言死单）。
func flipStatementStatus(tx *gorm.DB, statementId int64, fromStatus, toStatus string) error {
	res := tx.Model(&CommissionStatement{}).
		Where("id = ? AND status = ?", statementId, fromStatus).
		Updates(map[string]any{"status": toStatus})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return fmt.Errorf("statement flip mismatch: want 1 got %d", res.RowsAffected)
	}
	return nil
}

// WithdrawalDetail A16/A17 列表项：提现单扁平嵌入 + username / period / settle_amount_cents
// 冗余（D-08 金额实时取 statement；03-02 ListRates 嵌入扁平先例）。
type WithdrawalDetail struct {
	CommissionWithdrawal
	Username          string `json:"username"`
	Period            string `json:"period"`
	SettleAmountCents int64  `json:"settle_amount_cents"`
}

// ListWithdrawals A16/A17 提现单列表分页（附录 E2 冻结形状）。
// selfId > 0（A16）：服务端强制 WHERE distributor_id = selfId，忽略 distributorId 过滤参数
// （禁信客户端，ROLE-02/CUST-05）；selfId = 0（A17）：distributorId/status 可选过滤（0/空串跳过谓词）。
// 双 LEFT JOIN 冗余 username（users）与 period/settle_amount_cents（statement）；排序 id DESC（新申请在前）。
func ListWithdrawals(selfId int, distributorId int, status string, page, pageSize int) ([]WithdrawalDetail, int64, error) {
	query := DB.Model(&CommissionWithdrawal{})
	if selfId != 0 {
		query = query.Where("commission_withdrawals.distributor_id = ?", selfId)
	} else if distributorId != 0 {
		query = query.Where("commission_withdrawals.distributor_id = ?", distributorId)
	}
	if status != "" {
		query = query.Where("commission_withdrawals.status = ?", status)
	}
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	details := make([]WithdrawalDetail, 0) // 空结果序列化为 [] 而非 null（04-02 A14 先例）
	if err := query.
		Select("commission_withdrawals.*, users.username, commission_statements.period, commission_statements.settle_amount_cents").
		Joins("LEFT JOIN users ON users.id = commission_withdrawals.distributor_id").
		Joins("LEFT JOIN commission_statements ON commission_statements.id = commission_withdrawals.statement_id").
		Order("commission_withdrawals.id DESC").
		Offset((page - 1) * pageSize).Limit(pageSize).
		Find(&details).Error; err != nil {
		return nil, 0, err
	}
	return details, total, nil
}
