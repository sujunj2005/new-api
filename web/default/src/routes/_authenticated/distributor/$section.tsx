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

import { DistributorConsole } from '@/features/distributor-console'
import {
  DISTRIBUTOR_DEFAULT_SECTION,
  isDistributorSectionId,
} from '@/features/distributor-console/section-registry'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 分销商控制台路由（D-01/D-02）。
 * beforeLoad 精确守卫自 index.tsx 逐字平移：非 role=5 一律 /403，
 * 禁阈值式（admin(10) 不是分销商，阈值会让 admin 进页面但 API 全 403 成死入口）。
 * 前端守卫仅体验层，服务端 DistributorAuth（精确 role==5）是真防线。
 */
export const Route = createFileRoute('/_authenticated/distributor/$section')({
  beforeLoad: ({ params }) => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role !== ROLE.DISTRIBUTOR) {
      throw redirect({
        to: '/403',
      })
    }
    if (!isDistributorSectionId(params.section)) {
      throw redirect({
        to: '/distributor/$section',
        params: { section: DISTRIBUTOR_DEFAULT_SECTION },
      })
    }
  },
  component: DistributorConsole,
})
