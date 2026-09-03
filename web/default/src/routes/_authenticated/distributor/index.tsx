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
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import type { CommissionStatement } from '@/features/commission-statements/api'
import { StatementDetail } from '@/features/commission-statements/components/statement-detail'
import { StatementTable } from '@/features/commission-statements/components/statement-table'
import { WithdrawApplyDialog } from '@/features/commission-withdrawals/components/withdraw-apply-dialog'
import { WithdrawalTable } from '@/features/commission-withdrawals/components/withdrawal-table'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 分销商控制台（Phase 5 最小提现页，D-01/D-02；控制台主体仍归 Phase 6）。
 *
 * beforeLoad 守卫用 `role === ROLE.DISTRIBUTOR` 精确匹配（契约 §2.2 张力点 #3）：
 * 非 role=5 用户重定向 /403。不用 `role < ROLE.DISTRIBUTOR` 阈值——否则 admin(10)/root(100)
 * 能进页面但后端 DistributorAuth 返回 403，体验差（死入口）。
 * 服务端 DistributorAuth 是真防线，前端守卫只管体验。
 *
 * 组装纪律（D4 禁复制派生）：账单表/明细/提现记录/申请弹窗全部来自既有/新 feature 域
 * 共享组件（scope='self'），本文件零表格/弹窗实现；申请/驳回/打款成功后经同一
 * refreshKey 同步刷新账单表与提现记录表。
 */
function DistributorConsole() {
  const { t } = useTranslation()
  const [selected, setSelected] = useState<CommissionStatement | null>(null)
  const [applyTarget, setApplyTarget] = useState<CommissionStatement | null>(
    null
  )
  const [refreshKey, setRefreshKey] = useState(0)

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Distributor Console')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-8'>
          <section className='flex flex-col gap-4'>
            <h2 className='text-base font-semibold'>{t('Statements')}</h2>
            <StatementTable
              scope='self'
              onOpenDetail={setSelected}
              onApplyWithdraw={setApplyTarget}
              refreshKey={refreshKey}
            />
            {selected && (
              <StatementDetail
                scope='self'
                statement={selected}
                onClose={() => setSelected(null)}
              />
            )}
          </section>
          <section className='flex flex-col gap-4'>
            <h2 className='text-base font-semibold'>
              {t('Withdrawal Records')}
            </h2>
            <WithdrawalTable scope='self' refreshKey={refreshKey} />
          </section>
          {/* 弹窗置于 Content 内：SectionPageLayout 仅渲染命名插槽，外部子树不挂载 */}
          <WithdrawApplyDialog
            statement={applyTarget}
            onOpenChange={(open) => {
              if (!open) setApplyTarget(null)
            }}
            onSuccess={() => setRefreshKey((k) => k + 1)}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
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
