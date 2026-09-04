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
 * 佣金比例 API 层（契约 01-CONTRACT.md §4.3 A1/A2/A3 冻结形状，RATE-01/RATE-03）。
 * 端点映射：
 *   fetchCommissionRates      → GET  /api/commission/rates（A1 列表，扁平嵌入含 username 冗余）
 *   setCommissionRate         → PUT  /api/commission/rates/:distributorId（A2 upsert，body {"rate_bp":N}）
 *   fetchCommissionRateHistory → GET /api/commission/rates/:distributorId/history（A3 变更历史）
 * 业务校验全部在服务端（万分比 0~10000 / role=5 精确匹配 / 路径参数解析），
 * 前端零业务校验依赖，失败信封 message 原样回显。
 */

/** A1 行（model.CommissionRateDetail 扁平 JSON，json tag 逐字对齐） */
export interface CommissionRateRow {
  id: number
  distributor_id: number
  rate_bp: number
  updated_by: number
  updated_at: number
  username: string
}

/** A3 行（model.CommissionRateHistory json tag 逐字：old_rate_bp/new_rate_bp 全称，禁简写） */
export interface RateHistoryRow {
  id: number
  distributor_id: number
  old_rate_bp: number
  new_rate_bp: number
  operator_id: number
  created_at: number
}

export type CommissionRatePageInfo<T> = StatementPageInfo<T>

/** A1 比例列表分页（AdminAuth，按 distributor_id 升序） */
export async function fetchCommissionRates(
  page = 1
): Promise<ApiEnvelope<CommissionRatePageInfo<CommissionRateRow>>> {
  const res = await api.get(`/api/commission/rates?p=${page}`)
  return res.data
}

/** A2 设置比例（upsert 语义：无行 Create / 有行 Updates，服务端 role=5 精确校验兜底防错绑） */
export async function setCommissionRate(
  distributorId: number,
  rateBp: number
): Promise<ApiEnvelope<null>> {
  const res = await api.put(`/api/commission/rates/${distributorId}`, {
    rate_bp: rateBp,
  })
  return res.data
}

/** A3 比例变更历史分页（old_rate_bp→new_rate_bp + operator_id + created_at，RATE-03） */
export async function fetchCommissionRateHistory(
  distributorId: number,
  page = 1
): Promise<ApiEnvelope<CommissionRatePageInfo<RateHistoryRow>>> {
  const res = await api.get(
    `/api/commission/rates/${distributorId}/history?p=${page}`
  )
  return res.data
}

/**
 * 万分比 → 百分比展示（全项目唯一 bp 换算点，D-07「同一换算」）：
 * 500 → '5%'，5000 → '50%'，0 → '0%'，1250 → '12.5%'。
 * 对账单明细「比例」列同样 import 此函数（禁内联禁复制派生）。
 */
export function rateBpToPercent(bp: number): string {
  const percent = bp / 100
  const normalized = Number.isInteger(percent) ? percent : Number(percent.toFixed(2))
  return `${normalized}%`
}

/** 金额/时间展示复用对账单域唯一入口（禁复制派生，S9 格式化唯一入口纪律） */
export { formatCents, formatUnixTime }
