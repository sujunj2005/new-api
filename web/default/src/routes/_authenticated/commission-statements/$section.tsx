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

import { CommissionStatements } from '@/features/commission-statements'
import {
  COMMISSION_STATEMENTS_DEFAULT_SECTION,
  isCommissionStatementsSectionId,
} from '@/features/commission-statements/section-registry'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 管理端对账单路由（契约附录 D4）。
 * beforeLoad 阈值式守卫（role < ROLE.ADMIN → /403，users 页惯例）：
 * 前端守卫仅体验层，服务端 AdminAuth（T-04-02-05）才是真防线。
 */
export const Route = createFileRoute(
  '/_authenticated/commission-statements/$section'
)({
  beforeLoad: ({ params }) => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
    if (!isCommissionStatementsSectionId(params.section)) {
      throw redirect({
        to: '/commission-statements/$section',
        params: { section: COMMISSION_STATEMENTS_DEFAULT_SECTION },
      })
    }
  },
  component: CommissionStatements,
})
