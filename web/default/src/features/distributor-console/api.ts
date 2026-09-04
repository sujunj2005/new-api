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
 * 分销商控制台 API 层（契约 01-CONTRACT.md §4.3 A9/A12 + §4.2 B4/B5，v1.5 附录 F）。
 * 响应 interface 与后端 json tag 逐字对齐（06-01 附录 F 冻结形状）；
 * 金额单位纪律：A9/A12/B4 为 int64 分，仅展示层 formatCents 换算；
 * B5 充值行 money 为 float 元（model/topup.go），展示禁用分换算入口。
 */

/** A9 佣金仪表盘（四冻结分字段逐字 + 附录 F2 四笔数扩展） */
export interface CommissionDashboard {
  current_pending_cents: number
  total_statement_cents: number
  total_withdrawn_cents: number
  pending_withdraw_cents: number
  current_pending_count: number
  total_statement_count: number
  total_withdrawn_count: number
  pending_withdraw_count: number
}

/** A12 未出账实时预览（三冻结字段逐字，§4.3） */
export interface CurrentCommission {
  period: string
  pending_flows: number
  pending_cents: number
}

/** A9 佣金仪表盘（DistributorAuth 会话派生，零客户端参数） */
export async function fetchDashboard(): Promise<
  ApiEnvelope<CommissionDashboard>
> {
  const res = await api.get('/api/commission/dashboard')
  return res.data
}

/** A12 未出账实时预览（与 A9 当期未出账同谓词，D-05 分工不合并） */
export async function fetchCurrent(): Promise<ApiEnvelope<CurrentCommission>> {
  const res = await api.get('/api/commission/current')
  return res.data
}

/** B4 客户列表项（controller/distributor.go distributorCustomerItem json tag 逐字） */
export interface DistributorCustomerRow {
  id: number
  username: string
  display_name: string
  created_at: number
  total_topup_cents: number
  total_commission_cents: number
}

export type DistributorCustomerPageInfo<T> = StatementPageInfo<T>

/** B4 名下客户列表（服务端强制归属过滤，分页信封） */
export async function fetchDistributorCustomers(
  page?: number
): Promise<ApiEnvelope<DistributorCustomerPageInfo<DistributorCustomerRow>>> {
  const q = new URLSearchParams()
  if (page) q.set('p', String(page))
  const res = await api.get(`/api/distributor/customers?${q.toString()}`)
  return res.data
}

/** B5 客户充值行（model/topup.go TopUp json tag 逐字；money 为 float 元非分） */
export interface CustomerTopupRow {
  id: number
  user_id: number
  amount: number
  money: number
  trade_no: string
  payment_method: string
  payment_provider: string
  create_time: number
  complete_time: number
  status: string
}

export type CustomerTopupPageInfo<T> = StatementPageInfo<T>

/** B5 单客户充值明细（IDOR：非本人名下客户服务端 403） */
export async function fetchDistributorCustomerTopups(
  customerId: number,
  page?: number
): Promise<ApiEnvelope<CustomerTopupPageInfo<CustomerTopupRow>>> {
  const q = new URLSearchParams()
  if (page) q.set('p', String(page))
  const res = await api.get(
    `/api/distributor/customers/${customerId}/topups?${q.toString()}`
  )
  return res.data
}

/** 充值单五态（common/constants.go TopUpStatus* 逐字） */
export type TopupStatus =
  | 'pending'
  | 'success'
  | 'failed'
  | 'expired'
  | 'refunded'

/** 五态 → i18n key（常量映射不透传服务端字符串，T-06-02-03 同款纪律） */
export const TOPUP_STATUS_LABEL_KEYS: Record<TopupStatus, string> = {
  pending: 'Topup Pending',
  success: 'Topup Success',
  failed: 'Topup Failed',
  expired: 'Topup Expired',
  refunded: 'Topup Refunded',
}

/** 五态 → 徽标变体（成功醒目，失败/退款警示） */
export const TOPUP_STATUS_BADGE_VARIANT: Record<
  TopupStatus,
  'default' | 'secondary' | 'outline' | 'destructive'
> = {
  pending: 'secondary',
  success: 'default',
  failed: 'destructive',
  expired: 'outline',
  refunded: 'destructive',
}

/** 金额/时间展示复用对账单域唯一入口（禁复制派生，S9） */
export { formatCents, formatUnixTime }
