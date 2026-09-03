/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { api } from '@/lib/api'

import {
  formatCents,
  formatUnixTime,
  type ApiEnvelope,
  type StatementPageInfo,
} from '@/features/commission-statements/api'

/**
 * 提现单 API 层（契约 01-CONTRACT.md 附录 E2 冻结形状 A15-A21，v1.4）。
 * 金额无独立字段（D-08）：settle_amount_cents 经后端 JOIN 实时取账单值，
 * 展示层复用 formatCents（契约 §3.0 禁 float 运算）。
 */

/** 提现单五态（附录 E1 冻结；Q1=B approved 独立态，Q2=B 驳回仅 reviewing 可发起） */
export type WithdrawalStatus =
  | 'pending'
  | 'reviewing'
  | 'approved'
  | 'paid'
  | 'rejected'

/** 视角：admin=审核工作台（A17-A21），self=分销商（A15/A16） */
export type WithdrawalScope = 'admin' | 'self'

/** 提现单（A16/A17 WithdrawalDetail 扁平 snake_case，附录 E2 冻结字段逐字对齐） */
export interface Withdrawal {
  id: number
  withdrawal_no: string
  statement_id: number
  distributor_id: number
  status: WithdrawalStatus
  reason: string
  voucher_no: string
  operator_id: number
  created_at: number
  reviewed_at: number
  approved_at: number
  paid_at: number
  rejected_at: number
  /** admin 列表 JOIN users 冗余（A17）；self 响应可能缺省 */
  username?: string
  /** JOIN 账单冗余（D-08 实时取值） */
  period: string
  settle_amount_cents: number
}

export type WithdrawalPageInfo<T> = StatementPageInfo<T>

/** 五态 → i18n key（动态标签走 t(key) 渲染，常量映射不透传服务端字符串 T-05-02-02） */
export const WITHDRAWAL_STATUS_LABEL_KEYS: Record<WithdrawalStatus, string> = {
  pending: 'Withdrawal Pending',
  reviewing: 'Withdrawal Reviewing',
  approved: 'Withdrawal Approved',
  paid: 'Withdrawal Paid',
  rejected: 'Withdrawal Rejected',
}

/** 五态 → 徽标变体（状态视觉区分；rejected 醒目红） */
export const WITHDRAWAL_STATUS_BADGE_VARIANT: Record<
  WithdrawalStatus,
  'default' | 'secondary' | 'outline' | 'destructive'
> = {
  pending: 'secondary',
  reviewing: 'default',
  approved: 'outline',
  paid: 'default',
  rejected: 'destructive',
}

/** 时间线五列（附录 E1；空值 0 不展示） */
export const WITHDRAWAL_TIMELINE_FIELDS: Array<{
  key: keyof Withdrawal
  labelKey: string
}> = [
  { key: 'created_at', labelKey: 'Applied At' },
  { key: 'reviewed_at', labelKey: 'Reviewed At' },
  { key: 'approved_at', labelKey: 'Approved At' },
  { key: 'paid_at', labelKey: 'Paid At' },
  { key: 'rejected_at', labelKey: 'Rejected At' },
]

/** scope → 提现单列表基路径（admin=A17；self=A16 服务端强制 WHERE distributor_id=self） */
export function withdrawalsBasePath(scope: WithdrawalScope): string {
  return scope === 'self'
    ? '/api/commission/withdrawals/self'
    : '/api/commission/withdrawals'
}

export interface WithdrawalListParams {
  page?: number
  pageSize?: number
  /** 仅 admin 路由支持（A17）；self 服务端忽略任何客户端传入 */
  distributorId?: number
  status?: string
}

/** A16/A17 提现单列表（distributor_id/status 过滤可选，排序 id DESC 新申请在前） */
export async function fetchWithdrawals(
  scope: WithdrawalScope,
  params: WithdrawalListParams = {}
): Promise<ApiEnvelope<WithdrawalPageInfo<Withdrawal>>> {
  const q = new URLSearchParams()
  if (params.page) q.set('p', String(params.page))
  if (params.pageSize) q.set('page_size', String(params.pageSize))
  if (params.distributorId) {
    q.set('distributor_id', String(params.distributorId))
  }
  if (params.status) q.set('status', params.status)
  const res = await api.get(`${withdrawalsBasePath(scope)}?${q.toString()}`)
  return res.data
}

/** A15 发起提现申请（按单全额 D-08：请求零金额输入面；响应 {success} 冻结形状） */
export async function applyWithdraw(
  statementId: number
): Promise<ApiEnvelope<null>> {
  const res = await api.post('/api/commission/withdrawals', {
    statement_id: statementId,
  })
  return res.data
}

/** A18 受理（pending→reviewing，账单零操作） */
export async function acceptWithdrawal(
  id: number
): Promise<ApiEnvelope<null>> {
  const res = await api.post(`/api/commission/withdrawals/${id}/accept`)
  return res.data
}

/** A19 驳回（reviewing→rejected，reason 必填；同事务账单回退 payable D-05） */
export async function rejectWithdrawal(
  id: number,
  reason: string
): Promise<ApiEnvelope<null>> {
  const res = await api.post(`/api/commission/withdrawals/${id}/reject`, {
    reason,
  })
  return res.data
}

/** A20 批准（reviewing→approved，账单保持 withdrawing，Q1=B） */
export async function approveWithdrawal(
  id: number
): Promise<ApiEnvelope<null>> {
  const res = await api.post(`/api/commission/withdrawals/${id}/approve`)
  return res.data
}

/** A21 打款登记（approved→paid，voucher_no 必填；同事务账单 settled D-05） */
export async function markWithdrawalPaid(
  id: number,
  voucherNo: string
): Promise<ApiEnvelope<null>> {
  const res = await api.post(`/api/commission/withdrawals/${id}/paid`, {
    voucher_no: voucherNo,
  })
  return res.data
}

/** 金额/时间展示复用对账单域唯一入口（禁复制派生） */
export { formatCents, formatUnixTime }
