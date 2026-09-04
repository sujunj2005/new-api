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
import { getRouteApi, useNavigate } from '@tanstack/react-router'
import { useCallback, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import type { CommissionStatement } from './api'
import { ManualFlowDialog } from './components/manual-flow-dialog'
import { StatementAdjustDialog } from './components/statement-adjust-dialog'
import { StatementDetail } from './components/statement-detail'
import { StatementSummary } from './components/statement-summary'
import { StatementTable } from './components/statement-table'
import {
  isCommissionStatementsSectionId,
  type CommissionStatementsSectionId,
} from './section-registry'

const route = getRouteApi('/_authenticated/commission-statements/$section')

const SECTION_META: Record<CommissionStatementsSectionId, { titleKey: string }> =
  {
    list: { titleKey: 'Statement List' },
    summary: { titleKey: 'Statement Summary' },
  }

/**
 * 管理端对账单页（契约附录 D4 最小只读交付）。
 * 视图组件全部位于共享层（components/*，scope prop 设计），
 * 本文件仅组装 section 与管理端 scope="admin" 传参——Phase 6 分销商控制台
 * 复用同一组件传 scope="self"，禁止复制组件派生第二份实现。
 */
export function CommissionStatements() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = route.useParams()
  const active: CommissionStatementsSectionId = isCommissionStatementsSectionId(
    params.section
  )
    ? params.section
    : 'list'
  const [selected, setSelected] = useState<CommissionStatement | null>(null)
  // 6.2 超管调账（A7/A8 RootAuth，Obs-1 补 UI 消费者；入口按 root 收窄，Obs-2 同口径）
  const currentUser = useAuthStore((s) => s.auth.user)
  const isRoot = currentUser?.role === ROLE.SUPER_ADMIN
  const [manualFlowOpen, setManualFlowOpen] = useState(false)
  const [adjustStatement, setAdjustStatement] =
    useState<CommissionStatement | null>(null)
  const [refreshKey, setRefreshKey] = useState(0)

  const handleSectionChange = useCallback(
    (section: string) => {
      setSelected(null)
      void navigate({
        to: '/commission-statements/$section',
        params: { section: section as CommissionStatementsSectionId },
      })
    },
    [navigate]
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t(SECTION_META[active].titleKey)}
      </SectionPageLayout.Title>
      {/* Obs-1/Obs-2：A7 补录流水入口仅 root 查看者可见（RootAuth，非 root 的死入口不渲染） */}
      {isRoot && (
        <SectionPageLayout.Actions>
          <Button size='sm' onClick={() => setManualFlowOpen(true)}>
            {t('Manual Flow')}
          </Button>
        </SectionPageLayout.Actions>
      )}
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <Tabs value={active} onValueChange={handleSectionChange}>
            <TabsList className='max-w-full flex-wrap justify-start'>
              <TabsTrigger value='list'>{t('Statement List')}</TabsTrigger>
              <TabsTrigger value='summary'>
                {t('Statement Summary')}
              </TabsTrigger>
            </TabsList>
          </Tabs>

          {active === 'summary' ? (
            <StatementSummary scope='admin' />
          ) : (
            <>
              <StatementTable
                scope='admin'
                onOpenDetail={(statement) => setSelected(statement)}
                onAdjust={isRoot ? setAdjustStatement : undefined}
                refreshKey={refreshKey}
              />
              {selected && (
                <StatementDetail
                  scope='admin'
                  statement={selected}
                  onClose={() => setSelected(null)}
                />
              )}
            </>
          )}

          {/* A7/A8 弹窗常驻挂载（受控 open，非条件渲染——弹窗插槽纪律） */}
          <ManualFlowDialog
            open={manualFlowOpen}
            onOpenChange={setManualFlowOpen}
            onSuccess={() => setRefreshKey((k) => k + 1)}
          />
          <StatementAdjustDialog
            statement={adjustStatement}
            onOpenChange={(open) => {
              if (!open) setAdjustStatement(null)
            }}
            onSuccess={() => setRefreshKey((k) => k + 1)}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
