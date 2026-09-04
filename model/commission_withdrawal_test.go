package model

import (
	"errors"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"

	"github.com/stretchr/testify/require"
)

// 提现五态状态机测试（契约 v1.4 附录 E + 05-CONTEXT D-04~D-14）。
// 覆盖：WDRAW-01（payable 申请）/ WDRAW-02（五态迁移白名单 + 时间线五列）/
// WDRAW-04（防重 = 账单状态机条件更新，零唯一索引；D-06 驳回后重新申请回路）。
// 库纪律沿用 setupCommissionTestDB：_txlock=immediate 串行化语义与生产行锁一致。

var wdNoPattern = regexp.MustCompile(`^WD-\d{14}-[0-9A-Za-z]{4}$`)

// reloadStatement / reloadWithdrawal 重新读行断言库内终态（禁半状态）。
func reloadStatement(t *testing.T, id int64) *CommissionStatement {
	t.Helper()
	var s CommissionStatement
	require.NoError(t, DB.First(&s, "id = ?", id).Error)
	return &s
}

func reloadWithdrawal(t *testing.T, id int64) *CommissionWithdrawal {
	t.Helper()
	var w CommissionWithdrawal
	require.NoError(t, DB.First(&w, "id = ?", id).Error)
	return &w
}

// TestWithdrawalApply A15 申请事务全分支（WDRAW-01/SC1/SC2 + D-06/D-08/D-14）。
func TestWithdrawalApply(t *testing.T) {
	t.Run("payable_apply_success_statement_flips_withdrawing", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{
			distributorId:        1001,
			totalCommissionCents: 1000,
			adjustedCents:        -50, // settle = 950
		})
		before := *stmt

		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		require.NotNil(t, wd)
		require.Equal(t, WithdrawalPending, wd.Status)
		require.Regexp(t, wdNoPattern, wd.WithdrawalNo, "WD-{yyyyMMddHHmmss}-{4位随机}（§3.1.1 风格）")
		require.Equal(t, stmt.Id, wd.StatementId)
		require.Equal(t, 1001, wd.DistributorId)
		require.Greater(t, wd.CreatedAt, int64(0), "created_at = 申请时间（时间线五列之一）")

		// D-08：结构体零金额列断言（金额经 JOIN 实时取 statement）
		typ := reflect.TypeOf(CommissionWithdrawal{})
		for i := 0; i < typ.NumField(); i++ {
			name := typ.Field(i).Name
			require.NotContains(t, name, "Cents", "提现单禁金额字段（D-08）")
			require.NotContains(t, name, "Amount", "提现单禁金额字段（D-08）")
		}

		// 账单联动：status 翻 withdrawing，其余列全部不变（immutable-by-absence-of-write-path）
		after := reloadStatement(t, stmt.Id)
		require.Equal(t, StatementWithdrawing, after.Status)
		require.Equal(t, before.TotalTopupCents, after.TotalTopupCents)
		require.Equal(t, before.TotalCommissionCents, after.TotalCommissionCents)
		require.Equal(t, before.AdjustedCents, after.AdjustedCents)
		require.Equal(t, before.SettleAmountCents, after.SettleAmountCents)
		require.Equal(t, before.Period, after.Period)
		require.Equal(t, before.LockedAt, after.LockedAt)

		var count int64
		require.NoError(t, DB.Model(&CommissionWithdrawal{}).Count(&count).Error)
		require.Equal(t, int64(1), count)
	})

	t.Run("not_owner_rejected_with_sentinel", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})

		_, err := CreateWithdrawalTx(stmt.Id, 1002)
		require.True(t, errors.Is(err, ErrWithdrawalNotOwner), "controller 转 403 语义, got: %v", err)
		require.Equal(t, StatementPayable, reloadStatement(t, stmt.Id).Status, "账单不得翻转")
		var count int64
		require.NoError(t, DB.Model(&CommissionWithdrawal{}).Count(&count).Error)
		require.Zero(t, count)
	})

	t.Run("status_whitelist_distinct_messages", func(t *testing.T) {
		setupCommissionTestDB(t)

		withdrawing := seedStatement(t, seedStatementOpts{distributorId: 1001, status: StatementWithdrawing})
		_, err := CreateWithdrawalTx(withdrawing.Id, 1001)
		require.ErrorContains(t, err, "提现中", "Pitfall 6 文案区分（等待中的正常态）")

		settled := seedStatement(t, seedStatementOpts{distributorId: 1001, status: StatementSettled})
		_, err = CreateWithdrawalTx(settled.Id, 1001)
		require.ErrorContains(t, err, "已结清", "Pitfall 6 文案区分（无需申请）")

		_, err = CreateWithdrawalTx(999999, 1001)
		require.ErrorContains(t, err, "账单不存在")
	})

	t.Run("concurrent_apply_only_one_wins", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})

		const racers = 2
		errs := make(chan error, racers)
		var wg sync.WaitGroup
		for i := 0; i < racers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := CreateWithdrawalTx(stmt.Id, 1001)
				errs <- err
			}()
		}
		wg.Wait()
		close(errs)

		successes := 0
		for err := range errs {
			if err == nil {
				successes++
			}
		}
		require.Equal(t, 1, successes, "_txlock=immediate 串行化：恰好一单成功（SC2/WDRAW-04）")
		var count int64
		require.NoError(t, DB.Model(&CommissionWithdrawal{}).Count(&count).Error)
		require.Equal(t, int64(1), count, "提现单表仅 1 行")
		require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status)
	})

	t.Run("reapply_after_reject_creates_second_withdrawal", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})

		wd1, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		_, err = AcceptWithdrawalTx(wd1.Id, 9)
		require.NoError(t, err)
		_, err = RejectWithdrawalTx(wd1.Id, "凭证信息不符", 9)
		require.NoError(t, err)
		require.Equal(t, StatementPayable, reloadStatement(t, stmt.Id).Status, "驳回回退 payable（D-05）")

		// D-06 回路：重新申请 = 新建第二条提现单（历史单不复用）
		wd2, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		require.NotEqual(t, wd1.Id, wd2.Id)
		var count int64
		require.NoError(t, DB.Model(&CommissionWithdrawal{}).Where("statement_id = ?", stmt.Id).Count(&count).Error)
		require.Equal(t, int64(2), count, "rejected 单与新 pending 单并存是合法终态布局（零唯一索引方案正确性证明）")
		require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status)
	})

	t.Run("gate_off_apply_still_succeeds", func(t *testing.T) {
		setupCommissionTestDB(t)
		withCommissionEnabled(t, false) // D-14/Q5=A：提现不受分佣总开关门控

		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		_, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err, "开关只管新记账，不管已出账金额的提现（D-14）")
		require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status)
	})
}

// TestWithdrawalStateMachine A18-A21 五迁移白名单全分支（WDRAW-02/SC4 + Q1=B 五态/Q2=B 仅 reviewing 可驳回）。
func TestWithdrawalStateMachine(t *testing.T) {
	t.Run("accept_pending_to_reviewing_statement_untouched", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)

		updated, err := AcceptWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)
		require.Equal(t, WithdrawalReviewing, updated.Status)
		got := reloadWithdrawal(t, wd.Id)
		require.Equal(t, WithdrawalReviewing, got.Status)
		require.Greater(t, got.ReviewedAt, int64(0), "reviewed_at 落值（SC4）")
		require.Equal(t, 9, got.OperatorId)
		require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status, "受理账单零操作（D-05）")
	})

	t.Run("reject_reviewing_reverts_statement_payable", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		_, err = AcceptWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)

		_, err = RejectWithdrawalTx(wd.Id, "资料有误", 9)
		require.NoError(t, err)
		got := reloadWithdrawal(t, wd.Id)
		require.Equal(t, WithdrawalRejected, got.Status)
		require.Equal(t, "资料有误", got.Reason)
		require.Greater(t, got.RejectedAt, int64(0))
		require.Equal(t, 9, got.OperatorId)
		require.Equal(t, StatementPayable, reloadStatement(t, stmt.Id).Status, "驳回回退（D-05，Pitfall 2 同事务+断言）")
	})

	t.Run("approve_reviewing_to_approved_statement_untouched", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		_, err = AcceptWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)

		updated, err := ApproveWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)
		require.Equal(t, WithdrawalApproved, updated.Status)
		got := reloadWithdrawal(t, wd.Id)
		require.Equal(t, WithdrawalApproved, got.Status)
		require.Greater(t, got.ApprovedAt, int64(0), "approved_at 落值（Q1=B 时间线第五列）")
		require.Equal(t, 9, got.OperatorId)
		require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status, "批准期间账单保持 withdrawing（D-12）")
	})

	t.Run("paid_approved_to_paid_settles_statement", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		_, err = AcceptWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)
		_, err = ApproveWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)

		updated, err := MarkWithdrawalPaidTx(wd.Id, "VOUCHER-2026-001", 9)
		require.NoError(t, err)
		require.Equal(t, WithdrawalPaid, updated.Status)
		got := reloadWithdrawal(t, wd.Id)
		require.Equal(t, WithdrawalPaid, got.Status)
		require.Equal(t, "VOUCHER-2026-001", got.VoucherNo)
		require.Greater(t, got.PaidAt, int64(0))
		require.Equal(t, 9, got.OperatorId)
		require.Equal(t, StatementSettled, reloadStatement(t, stmt.Id).Status, "打款结清（D-05）")
	})

	t.Run("illegal_transitions_all_rejected", func(t *testing.T) {
		cases := []struct {
			name   string
			seed   string
			action func(id int64) error
		}{
			{"pending_direct_reject", WithdrawalPending, func(id int64) error { _, err := RejectWithdrawalTx(id, "测试原因", 9); return err }},
			{"pending_direct_approve", WithdrawalPending, func(id int64) error { _, err := ApproveWithdrawalTx(id, 9); return err }},
			{"pending_direct_paid", WithdrawalPending, func(id int64) error { _, err := MarkWithdrawalPaidTx(id, "V-1", 9); return err }},
			{"reviewing_direct_paid_skips_approved", WithdrawalReviewing, func(id int64) error { _, err := MarkWithdrawalPaidTx(id, "V-1", 9); return err }},
			{"approved_rejected", WithdrawalApproved, func(id int64) error { _, err := RejectWithdrawalTx(id, "测试原因", 9); return err }},
			{"approved_approve_again", WithdrawalApproved, func(id int64) error { _, err := ApproveWithdrawalTx(id, 9); return err }},
			{"rejected_accept", WithdrawalRejected, func(id int64) error { _, err := AcceptWithdrawalTx(id, 9); return err }},
			{"rejected_approve", WithdrawalRejected, func(id int64) error { _, err := ApproveWithdrawalTx(id, 9); return err }},
			{"rejected_paid", WithdrawalRejected, func(id int64) error { _, err := MarkWithdrawalPaidTx(id, "V-1", 9); return err }},
			{"paid_accept", WithdrawalPaid, func(id int64) error { _, err := AcceptWithdrawalTx(id, 9); return err }},
			{"paid_approve", WithdrawalPaid, func(id int64) error { _, err := ApproveWithdrawalTx(id, 9); return err }},
			{"paid_reject", WithdrawalPaid, func(id int64) error { _, err := RejectWithdrawalTx(id, "测试原因", 9); return err }},
			{"paid_paid_again", WithdrawalPaid, func(id int64) error { _, err := MarkWithdrawalPaidTx(id, "V-2", 9); return err }},
		}
		for _, tc := range cases {
			tc := tc
			t.Run(tc.name, func(t *testing.T) {
				setupCommissionTestDB(t)
				stmt := seedStatement(t, seedStatementOpts{distributorId: 1001, status: StatementWithdrawing})
				wd := seedWithdrawal(t, seedWithdrawalOpts{
					statementId:   stmt.Id,
					distributorId: 1001,
					status:        tc.seed,
					reason:        map[string]string{WithdrawalRejected: "原始驳回原因"}[tc.seed],
					voucherNo:     map[string]string{WithdrawalPaid: "V-0"}[tc.seed],
				})

				err := tc.action(wd.Id)
				require.Error(t, err, "非法迁移（跳步/终态再迁移）必须拒绝")

				got := reloadWithdrawal(t, wd.Id)
				require.Equal(t, tc.seed, got.Status, "库内提现单状态不变")
				require.Equal(t, StatementWithdrawing, reloadStatement(t, stmt.Id).Status, "账单零部分写")
			})
		}
	})

	t.Run("reason_and_voucher_double_validation", func(t *testing.T) {
		setupCommissionTestDB(t)

		_, err := RejectWithdrawalTx(1, "", 9)
		require.ErrorContains(t, err, "驳回原因必填")
		_, err = RejectWithdrawalTx(1, "   ", 9)
		require.ErrorContains(t, err, "驳回原因必填")
		_, err = RejectWithdrawalTx(1, strings.Repeat("字", 256), 9)
		require.ErrorContains(t, err, "长度不能超过 255")

		_, err = MarkWithdrawalPaidTx(1, "", 9)
		require.ErrorContains(t, err, "打款凭证号必填")
		_, err = MarkWithdrawalPaidTx(1, "  ", 9)
		require.ErrorContains(t, err, "打款凭证号必填")
		_, err = MarkWithdrawalPaidTx(1, strings.Repeat("V", 256), 9)
		require.ErrorContains(t, err, "长度不能超过 255")

		var count int64
		require.NoError(t, DB.Model(&CommissionWithdrawal{}).Count(&count).Error)
		require.Zero(t, count, "校验失败零写入")
	})

	t.Run("timeline_five_columns_monotonic", func(t *testing.T) {
		setupCommissionTestDB(t)
		stmt := seedStatement(t, seedStatementOpts{distributorId: 1001})
		wd, err := CreateWithdrawalTx(stmt.Id, 1001)
		require.NoError(t, err)
		_, err = AcceptWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)
		_, err = ApproveWithdrawalTx(wd.Id, 9)
		require.NoError(t, err)
		_, err = MarkWithdrawalPaidTx(wd.Id, "V-1", 9)
		require.NoError(t, err)

		got := reloadWithdrawal(t, wd.Id)
		require.Greater(t, got.CreatedAt, int64(0))
		require.Greater(t, got.ReviewedAt, int64(0))
		require.Greater(t, got.ApprovedAt, int64(0))
		require.Greater(t, got.PaidAt, int64(0))
		require.LessOrEqual(t, got.ReviewedAt, got.ApprovedAt, "reviewed_at ≤ approved_at")
		require.LessOrEqual(t, got.ApprovedAt, got.PaidAt, "approved_at ≤ paid_at")
		require.Zero(t, got.RejectedAt, "未驳回 rejected_at 保持零值")
	})

	t.Run("list_withdrawals_self_narrowing_and_join", func(t *testing.T) {
		setupCommissionTestDB(t)
		u1 := &User{Username: "wd_dist_a", Password: "testpass1234", Role: common.RoleDistributorUser, Status: common.UserStatusEnabled, AffCode: "WDA1"}
		u2 := &User{Username: "wd_dist_b", Password: "testpass1234", Role: common.RoleDistributorUser, Status: common.UserStatusEnabled, AffCode: "WDB1"}
		require.NoError(t, DB.Create(u1).Error)
		require.NoError(t, DB.Create(u2).Error)

		s1 := seedStatement(t, seedStatementOpts{distributorId: u1.Id, totalCommissionCents: 950})
		s2 := seedStatement(t, seedStatementOpts{distributorId: u2.Id, totalCommissionCents: 420})
		wd1, err := CreateWithdrawalTx(s1.Id, u1.Id)
		require.NoError(t, err)
		wd2, err := CreateWithdrawalTx(s2.Id, u2.Id)
		require.NoError(t, err)

		// A16：selfId>0 强制 WHERE distributor_id = self（忽略 distributorId 过滤参数）
		items, total, err := ListWithdrawals(u1.Id, u2.Id, "", 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(1), total, "self 收窄：只看自己")
		require.Len(t, items, 1)
		require.Equal(t, wd1.Id, items[0].Id)
		require.Equal(t, "wd_dist_a", items[0].Username, "JOIN users 冗余 username")
		require.Equal(t, s1.Period, items[0].Period, "JOIN statement 冗余 period")
		require.Equal(t, int64(950), items[0].SettleAmountCents, "D-08 金额实时取 statement")

		// A17：selfId=0 走管理面过滤
		items, total, err = ListWithdrawals(0, u2.Id, "", 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.Equal(t, wd2.Id, items[0].Id)

		// 无过滤全量 + id DESC（新申请在前）
		items, total, err = ListWithdrawals(0, 0, WithdrawalPending, 1, 10)
		require.NoError(t, err)
		require.Equal(t, int64(2), total)
		require.Len(t, items, 2)
		require.Equal(t, wd2.Id, items[0].Id, "id DESC")

		// status 过滤
		_, total, err = ListWithdrawals(0, 0, WithdrawalApproved, 1, 10)
		require.NoError(t, err)
		require.Zero(t, total)
	})
}

