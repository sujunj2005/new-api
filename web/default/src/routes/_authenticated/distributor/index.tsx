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
import { useTranslation } from 'react-i18next'

import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 分销商控制台入口（Phase 2 仅入口+守卫，页面主体 Phase 6 实现，A4）。
 *
 * beforeLoad 守卫用 `role === ROLE.DISTRIBUTOR` 精确匹配（契约 §2.2 张力点 #3）：
 * 非 role=5 用户重定向 /403。不用 `role < ROLE.DISTRIBUTOR` 阈值——否则 admin(10)/root(100)
 * 能进页面但后端 DistributorAuth 返回 403，体验差（死入口）。
 * 服务端 DistributorAuth 是真防线，前端守卫只管体验。
 */
function DistributorConsole() {
  const { t } = useTranslation()
  return (
    <div className='flex h-full items-center justify-center p-8'>
      <div className='text-center'>
        <h1 className='text-2xl font-semibold'>{t('Distributor Console')}</h1>
        <p className='mt-2 text-sm text-muted-foreground'>
          {t('This page is not yet available.')}
        </p>
      </div>
    </div>
  )
}

export const Route = createFileRoute('/_authenticated/distributor/')({
  beforeLoad: () => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role !== ROLE.DISTRIBUTOR) {
      throw redirect({
        to: '/403',
      })
    }
  },
  component: DistributorConsole,
})
