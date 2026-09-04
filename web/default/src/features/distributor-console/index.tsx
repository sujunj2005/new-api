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
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { SectionPageLayout } from '@/components/layout'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { Tabs, TabsList, TabsTrigger } from '@/components/ui/tabs'
import type { CommissionStatement } from '@/features/commission-statements/api'
import { StatementDetail } from '@/features/commission-statements/components/statement-detail'
import { StatementTable } from '@/features/commission-statements/components/statement-table'
import { WithdrawApplyDialog } from '@/features/commission-withdrawals/components/withdraw-apply-dialog'
import { WithdrawalTable } from '@/features/commission-withdrawals/components/withdrawal-table'

import {
  fetchCurrent,
  fetchDistributorCustomers,
  formatCents,
  formatUnixTime,
  type CurrentCommission,
  type DistributorCustomerRow,
} from './api'
import { DashboardCards } from './components/dashboard-cards'
import { PromoCard } from './components/promo-card'
import { CustomerTopupsDialog } from './components/customer-topups-dialog'
import {
  isDistributorSectionId,
  type DistributorSectionId,
} from './section-registry'

const route = getRouteApi('/_authenticated/distributor/$section')

const CUSTOMER_PAGE_SIZE = 10

const SECTION_META: Record<DistributorSectionId, { titleKey: string }> = {
  overview: { titleKey: 'Overview' },
  customers: { titleKey: 'Customers' },
  statements: { titleKey: 'Statement List' },
  withdrawals: { titleKey: 'Withdrawals' },
}

/**
 * 分销商控制台（D-01 四节 section-registry 组装页，SC-1~SC-5 UI 落点）。
 * 组装纪律（D4 禁复制派生）：账单表/明细/提现记录/申请弹窗全部来自共享组件层
 * 本人视角传参，本文件零复制派生；申请成功后页级 refreshKey 联动刷新
 * 四卡/账单表/预览条/提现记录（Pitfall 5）。
 * 弹窗一律置于 SectionPageLayout.Content 内（Pitfall 4：仅四命名插槽渲染，外部静默不挂载）。
 */
export function DistributorConsole() {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const params = route.useParams()
  const active: DistributorSectionId = isDistributorSectionId(params.section)
    ? params.section
    : 'overview'

  const [refreshKey, setRefreshKey] = useState(0)
  const [stmtSelected, setStmtSelected] = useState<CommissionStatement | null>(
    null
  )
  const [applyTarget, setApplyTarget] = useState<CommissionStatement | null>(
    null
  )
  const [customerSelected, setCustomerSelected] =
    useState<DistributorCustomerRow | null>(null)

  // A12 未出账实时预览（D-05 与 A9 分工不合并，statements 节顶部预览条数据面）
  const [current, setCurrent] = useState<CurrentCommission | null>(null)
  useEffect(() => {
    let cancelled = false
    void fetchCurrent().then((res) => {
      if (!cancelled && res.success && res.data) {
        setCurrent(res.data)
      }
    })
    return () => {
      cancelled = true
    }
  }, [refreshKey])

  // B4 名下客户分页列表（服务端强制归属过滤，前端零过滤逻辑）
  const [customerRows, setCustomerRows] = useState<DistributorCustomerRow[]>([])
  const [customerTotal, setCustomerTotal] = useState(0)
  const [customerPage, setCustomerPage] = useState(1)
  const [customerLoading, setCustomerLoading] = useState(false)

  const loadCustomers = useCallback(async () => {
    setCustomerLoading(true)
    try {
      const res = await fetchDistributorCustomers(customerPage)
      if (res.success && res.data) {
        setCustomerRows(res.data.items ?? [])
        setCustomerTotal(res.data.total ?? 0)
      }
    } finally {
      setCustomerLoading(false)
    }
  }, [customerPage])

  useEffect(() => {
    void loadCustomers()
  }, [loadCustomers, refreshKey])

  const customerTotalPages = Math.max(
    1,
    Math.ceil(customerTotal / CUSTOMER_PAGE_SIZE)
  )

  const handleSectionChange = useCallback(
    (section: string) => {
      setStmtSelected(null)
      setApplyTarget(null)
      void navigate({
        to: '/distributor/$section',
        params: { section: section as DistributorSectionId },
      })
    },
    [navigate]
  )

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t(SECTION_META[active].titleKey)}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <Tabs value={active} onValueChange={handleSectionChange}>
            <TabsList className='max-w-full flex-wrap justify-start'>
              <TabsTrigger value='overview'>{t('Overview')}</TabsTrigger>
              <TabsTrigger value='customers'>{t('Customers')}</TabsTrigger>
              <TabsTrigger value='statements'>
                {t('Statement List')}
              </TabsTrigger>
              <TabsTrigger value='withdrawals'>{t('Withdrawals')}</TabsTrigger>
            </TabsList>
          </Tabs>

          {active === 'overview' && (
            <>
              {/* G-4 推广卡（B6 aff_code/aff_link/客户数，四卡旁辅助信息卡） */}
              <PromoCard refreshKey={refreshKey} />
              <DashboardCards refreshKey={refreshKey} />
            </>
          )}

          {active === 'customers' && (
            <div className='flex flex-col gap-2'>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>{t('Customer')}</TableHead>
                    <TableHead>{t('Display Name')}</TableHead>
                    <TableHead>{t('Registered At')}</TableHead>
                    <TableHead>{t('Total Topup')}</TableHead>
                    <TableHead>{t('Total Commission')}</TableHead>
                    <TableHead>{t('Actions')}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {customerRows.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={6} className='h-16 text-center'>
                        {t('No customers found')}
                      </TableCell>
                    </TableRow>
                  ) : (
                    customerRows.map((row) => (
                      <TableRow key={row.id}>
                        <TableCell className='font-mono'>
                          {row.username}
                        </TableCell>
                        <TableCell>{row.display_name || '-'}</TableCell>
                        <TableCell>{formatUnixTime(row.created_at)}</TableCell>
                        <TableCell>
                          {formatCents(row.total_topup_cents)}
                        </TableCell>
                        <TableCell>
                          {formatCents(row.total_commission_cents)}
                        </TableCell>
                        <TableCell>
                          <Button
                            variant='outline'
                            size='sm'
                            onClick={() => setCustomerSelected(row)}
                          >
                            {t('Topup Details')}
                          </Button>
                        </TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
              <div className='flex items-center justify-end gap-2 text-sm'>
                <span>
                  {t('Page')} {customerPage} / {customerTotalPages} ·{' '}
                  {customerTotal} {t('Records')}
                </span>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={customerPage <= 1 || customerLoading}
                  onClick={() =>
                    setCustomerPage((p) => Math.max(1, p - 1))
                  }
                >
                  {t('Previous')}
                </Button>
                <Button
                  variant='outline'
                  size='sm'
                  disabled={customerPage >= customerTotalPages || customerLoading}
                  onClick={() => setCustomerPage((p) => p + 1)}
                >
                  {t('Next')}
                </Button>
              </div>
            </div>
          )}

          {active === 'statements' && (
            <>
              {/* A12 未出账实时预览条（STMT-04「含未出账」，轻量不与 A9 合并） */}
              <div className='bg-muted/30 flex items-center justify-between rounded-lg border px-4 py-3'>
                <div className='flex items-center gap-2 text-sm'>
                  <span className='font-medium'>{t('Unbilled Preview')}</span>
                  <span className='text-muted-foreground'>
                    {t('Period')} {current?.period ?? '-'}
                  </span>
                </div>
                <div className='flex items-baseline gap-3'>
                  <span className='text-base font-semibold'>
                    {current ? formatCents(current.pending_cents) : '-'}
                  </span>
                  <span className='text-muted-foreground text-sm'>
                    {current
                      ? `${current.pending_flows} ${t('Records')}`
                      : '-'}
                  </span>
                </div>
              </div>
              <StatementTable
                scope='self'
                onOpenDetail={setStmtSelected}
                onApplyWithdraw={setApplyTarget}
                refreshKey={refreshKey}
              />
              {stmtSelected && (
                <StatementDetail
                  scope='self'
                  statement={stmtSelected}
                  onClose={() => setStmtSelected(null)}
                />
              )}
            </>
          )}

          {active === 'withdrawals' && (
            <WithdrawalTable scope='self' refreshKey={refreshKey} />
          )}

          {/* 弹窗置于 Content 内：SectionPageLayout 仅渲染命名插槽，外部子树不挂载 */}
          <CustomerTopupsDialog
            customer={customerSelected}
            onOpenChange={(open) => {
              if (!open) setCustomerSelected(null)
            }}
          />
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
