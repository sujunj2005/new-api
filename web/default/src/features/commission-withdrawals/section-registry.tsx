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
import { createSectionRegistry } from '@/features/system-settings/utils/section-registry'

/**
 * 提现审核工作台 section 定义（Phase 5 最小范围：仅 list 一节）。
 * 内容由工作台路由组件渲染（操作弹窗/快照明细态需组件内状态）。
 */
const COMMISSION_WITHDRAWALS_SECTIONS = [
  {
    id: 'list',
    titleKey: 'Withdrawal Review',
    build: () => null,
  },
] as const

export type CommissionWithdrawalsSectionId =
  (typeof COMMISSION_WITHDRAWALS_SECTIONS)[number]['id']

const commissionWithdrawalsRegistry = createSectionRegistry<
  CommissionWithdrawalsSectionId,
  Record<string, never>,
  []
>({
  sections: COMMISSION_WITHDRAWALS_SECTIONS,
  defaultSection: 'list',
  basePath: '/commission-withdrawals',
  urlStyle: 'path',
})

export const COMMISSION_WITHDRAWALS_SECTION_IDS =
  commissionWithdrawalsRegistry.sectionIds
export const COMMISSION_WITHDRAWALS_DEFAULT_SECTION =
  commissionWithdrawalsRegistry.defaultSection

/** Type guard for validating section IDs without casting. */
export function isCommissionWithdrawalsSectionId(
  s: string
): s is CommissionWithdrawalsSectionId {
  return (COMMISSION_WITHDRAWALS_SECTION_IDS as readonly string[]).includes(s)
}
