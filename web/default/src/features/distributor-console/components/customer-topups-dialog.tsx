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
/**
 * B5 客户充值明细弹层（CUST-02）。
 * 金额单位纪律（Pitfall 10/T-06-02-04）：TopUp.money 为 float 元（model/topup.go），
 * 直显两位小数，禁走分为单位的展示换算入口——单位混用即金额放大百倍。
 * 状态五值走常量映射渲染，禁透传服务端字符串（T-06-02-03）。
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  fetchDistributorCustomerTopups,
  formatUnixTime,
  TOPUP_STATUS_BADGE_VARIANT,
  TOPUP_STATUS_LABEL_KEYS,
  type CustomerTopupRow,
  type DistributorCustomerRow,
  type TopupStatus,
} from '../api'

const TOPUP_PAGE_SIZE = 10

interface CustomerTopupsDialogProps {
  /** 目标客户（null=关闭） */
  customer: DistributorCustomerRow | null
  onOpenChange: (open: boolean) => void
}

/** 充值状态徽标（映射表命中才渲染，未知值显示占位符不透传服务端字符串） */
function TopupStatusBadge({ status }: { status: string }) {
  const { t } = useTranslation()
  const labelKey = TOPUP_STATUS_LABEL_KEYS[status as TopupStatus]
  if (!labelKey) return <span>-</span>
  return (
    <Badge variant={TOPUP_STATUS_BADGE_VARIANT[status as TopupStatus]}>
      {t(labelKey)}
    </Badge>
  )
}

/** 客户充值明细分页弹层（列：充值单号/金额/状态/到账时间） */
export function CustomerTopupsDialog({
  customer,
  onOpenChange,
}: CustomerTopupsDialogProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<CustomerTopupRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  const customerId = customer?.id

  // 切换客户时回到第一页
  useEffect(() => {
    setPage(1)
  }, [customerId])

  const load = useCallback(async () => {
    if (!customerId) return
    setLoading(true)
    try {
      const res = await fetchDistributorCustomerTopups(customerId, page)
      if (res.success && res.data) {
        setRows(res.data.items ?? [])
        setTotal(res.data.total ?? 0)
      }
    } finally {
      setLoading(false)
    }
  }, [customerId, page])

  useEffect(() => {
    void load()
  }, [load])

  const totalPages = Math.max(1, Math.ceil(total / TOPUP_PAGE_SIZE))

  return (
    <Dialog
      open={!!customer}
      onOpenChange={onOpenChange}
      title={t('Customer Topups')}
      description={customer ? `${t('Customer')}: ${customer.username}` : undefined}
      footer={
        <Button variant='outline' onClick={() => onOpenChange(false)}>
          {t('Cancel')}
        </Button>
      }
    >
      <div className='flex flex-col gap-2'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Billing No')}</TableHead>
              <TableHead>{t('Topup Amount')}</TableHead>
              <TableHead>{t('Status')}</TableHead>
              <TableHead>{t('Complete Time')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className='h-16 text-center'>
                  {t('No topups found')}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className='font-mono'>{row.trade_no}</TableCell>
                  {/* money 为 float 元直显两位小数（非分单位，禁分换算） */}
                  <TableCell>{row.money.toFixed(2)}</TableCell>
                  <TableCell>
                    <TopupStatusBadge status={row.status} />
                  </TableCell>
                  <TableCell>{formatUnixTime(row.complete_time)}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
        <div className='flex items-center justify-end gap-2 text-sm'>
          <span>
            {t('Page')} {page} / {totalPages} · {total} {t('Records')}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={page <= 1 || loading}
            onClick={() => setPage((p) => Math.max(1, p - 1))}
          >
            {t('Previous')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={page >= totalPages || loading}
            onClick={() => setPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      </div>
    </Dialog>
  )
}
