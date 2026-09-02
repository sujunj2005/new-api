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
 * 对账单页 section 定义（契约附录 D4 最小只读范围：list + summary 两节即全部，
 * 无调账 UI/无导出——A7/A8 调账面归 04-03 且契约不冻结其 UI）
 */
const COMMISSION_STATEMENTS_SECTIONS = [
  {
    id: 'list',
    titleKey: 'Statement List',
    build: () => null, // 内容由 feature index.tsx 渲染（含详情展开态，需组件内状态）
  },
  {
    id: 'summary',
    titleKey: 'Statement Summary',
    build: () => null,
  },
] as const

export type CommissionStatementsSectionId =
  (typeof COMMISSION_STATEMENTS_SECTIONS)[number]['id']

const commissionStatementsRegistry = createSectionRegistry<
  CommissionStatementsSectionId,
  Record<string, never>,
  []
>({
  sections: COMMISSION_STATEMENTS_SECTIONS,
  defaultSection: 'list',
  basePath: '/commission-statements',
  urlStyle: 'path',
})

export const COMMISSION_STATEMENTS_SECTION_IDS =
  commissionStatementsRegistry.sectionIds
export const COMMISSION_STATEMENTS_DEFAULT_SECTION =
  commissionStatementsRegistry.defaultSection

/** Type guard for validating section IDs without casting. */
export function isCommissionStatementsSectionId(
  s: string
): s is CommissionStatementsSectionId {
  return (COMMISSION_STATEMENTS_SECTION_IDS as readonly string[]).includes(s)
}
