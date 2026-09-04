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
 * 分销商控制台 section 定义（D-01：四节 overview/customers/statements/withdrawals）。
 * 内容由 feature index.tsx 渲染（节内状态/弹窗需组件内持有），build 恒空。
 */
export const DISTRIBUTOR_SECTIONS = [
  {
    id: 'overview',
    titleKey: 'Overview',
    build: () => null,
  },
  {
    id: 'customers',
    titleKey: 'Customers',
    build: () => null,
  },
  {
    id: 'statements',
    titleKey: 'Statement List',
    build: () => null,
  },
  {
    id: 'withdrawals',
    titleKey: 'Withdrawals',
    build: () => null,
  },
] as const

export type DistributorSectionId = (typeof DISTRIBUTOR_SECTIONS)[number]['id']

const distributorRegistry = createSectionRegistry<
  DistributorSectionId,
  Record<string, never>,
  []
>({
  sections: DISTRIBUTOR_SECTIONS,
  defaultSection: 'overview',
  basePath: '/distributor',
  urlStyle: 'path',
})

export const DISTRIBUTOR_SECTION_IDS = distributorRegistry.sectionIds
export const DISTRIBUTOR_DEFAULT_SECTION = distributorRegistry.defaultSection

/** Type guard for validating section IDs without casting. */
export function isDistributorSectionId(
  s: string
): s is DistributorSectionId {
  return (DISTRIBUTOR_SECTION_IDS as readonly string[]).includes(s)
}
