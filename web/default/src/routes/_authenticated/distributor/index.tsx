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
import { createFileRoute, redirect } from '@tanstack/react-router'

import { DISTRIBUTOR_DEFAULT_SECTION } from '@/features/distributor-console/section-registry'

/**
 * /distributor 入口纯 redirect（D-01/D-02，母本 commission-withdrawals/index.tsx）。
 * 精确守卫已整体迁往 $section.tsx beforeLoad——redirect 后必经其守卫，此处零身份判断。
 */
export const Route = createFileRoute('/_authenticated/distributor/')({
  beforeLoad: () => {
    throw redirect({
      to: '/distributor/$section',
      params: { section: DISTRIBUTOR_DEFAULT_SECTION },
    })
  },
})
