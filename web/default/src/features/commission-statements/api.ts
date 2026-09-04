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

/**
 * 对账单 API 层（契约 01-CONTRACT.md §4.3 A4/A5/A6 + 附录 D3 A14 冻结形状）。
 * 金额全链路 int64 cents（契约 §3.0），仅展示层 formatCents 除 100，无 float 运算。
 */

/** 视角：admin=全分销商（A4/A5/A6/A14），self=分销商数据收窄（A10/A11，Phase 6 复用） */
export type StatementScope = 'admin' | 'self'

export interface CommissionStatement {
  id: number
  distributor_id: number
  period: string
  total_topup_cents: number
  total_commission_cents: number
  adjusted_cents: number
  settle_amount_cents: number
  status: string
  created_at: number
  locked_at: number
}

export interface StatementAdjustment {
  id: number
  statement_id: number
  delta_cents: number
  reason: string
  operator_id: number
  created_at: number
}

export interface StatementItemWithUsername {
  id: number
  statement_id: number
  flow_id: number
  billing_no: string
  payment_method: string
  payment_provider: string
  topup_money_cents: number
  commission_cents: number
  customer_id: number
  complete_time: number
  username: string
  /** 契约 v1.5 附录 F1：比例快照（万分比），经流水表关联冗余；无关联流水（flow_id=0/人工/冲销）为 0 */
  rate_bp: number
}

export interface StatementSummaryItem {
  distributor_id: number
  username: string
  total_commission_cents: number
  adjusted_cents: number
  statement_status: string
  statement_id: number
}

export interface StatementSummaryTotals {
  total_commission_cents: number
  adjusted_cents: number
  distributor_count: number
}

export interface StatementSummaryData {
  period: string
  items: StatementSummaryItem[]
  totals: StatementSummaryTotals
}

export interface StatementPageInfo<T> {
  page: number
  page_size: number
  total: number
  items: T[]
}

export interface ApiEnvelope<T> {
  success: boolean
  message: string
  data?: T
}

/** scope → 对账单列表基路径（admin=A4；self=A10 收窄为本人，Phase 6 复用点） */
export function statementsBasePath(scope: StatementScope): string {
  return scope === 'self'
    ? '/api/commission/statements/self'
    : '/api/commission/statements'
}

/** scope → 账单明细路径（admin=A6；self=A11，契约 §4.3 冻结路径） */
export function statementItemsPath(
  scope: StatementScope,
  statementId: number
): string {
  return scope === 'self'
    ? `/api/commission/statements/self/${statementId}/items`
    : `/api/commission/statements/${statementId}/items`
}

export interface StatementListParams {
  page?: number
  pageSize?: number
  distributorId?: number
  period?: string
  status?: string
}

/** A4/A10 对账单列表（distributor_id/period/status 三过滤可选，仅 admin 路由支持） */
export async function fetchStatements(
  scope: StatementScope,
  params: StatementListParams = {}
): Promise<ApiEnvelope<StatementPageInfo<CommissionStatement>>> {
  const q = new URLSearchParams()
  if (params.page) q.set('p', String(params.page))
  if (params.pageSize) q.set('page_size', String(params.pageSize))
  if (params.distributorId) {
    q.set('distributor_id', String(params.distributorId))
  }
  if (params.period) q.set('period', params.period)
  if (params.status) q.set('status', params.status)
  const res = await api.get(
    `${statementsBasePath(scope)}?${q.toString()}`
  )
  return res.data
}

/** A5 账单详情（本体 + adjustments 数组）。契约仅 admin 路由；self 场景由 A10 行数据组装（Phase 6 复用点） */
export async function fetchStatementDetail(
  statementId: number
): Promise<
  ApiEnvelope<{ statement: CommissionStatement; adjustments: StatementAdjustment[] }>
> {
  const res = await api.get(`/api/commission/statements/${statementId}`)
  return res.data
}

/** A6/A11 账单明细分页（items 含客户 username 冗余） */
export async function fetchStatementItems(
  scope: StatementScope,
  statementId: number,
  page = 1,
  pageSize = 10
): Promise<ApiEnvelope<StatementPageInfo<StatementItemWithUsername>>> {
  const res = await api.get(
    `${statementItemsPath(scope, statementId)}?p=${page}&page_size=${pageSize}`
  )
  return res.data
}

/** A14 跨分销商汇总（契约附录 D3 冻结形状，AdminAuth） */
export async function fetchStatementSummary(
  period: string
): Promise<ApiEnvelope<StatementSummaryData>> {
  const res = await api.get(
    `/api/commission/statements/summary?period=${encodeURIComponent(period)}`
  )
  return res.data
}

/** 金额分 → 元展示（仅展示层换算；传输/运算层保持 int64 cents，禁浮点累积） */
export function formatCents(cents: number): string {
  return (cents / 100).toLocaleString('zh-CN', {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  })
}

/** Unix 秒 → 本地时间展示 */
export function formatUnixTime(seconds: number): string {
  if (!seconds) return '-'
  return new Date(seconds * 1000).toLocaleString('zh-CN', { hour12: false })
}
