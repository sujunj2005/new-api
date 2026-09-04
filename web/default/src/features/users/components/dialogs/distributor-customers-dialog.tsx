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
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Dialog } from '@/components/dialog'
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
  formatCents,
  formatUnixTime,
  type DistributorCustomerRow,
} from '@/features/distributor-console/api'

import { fetchAdminDistributorCustomers } from '../../api'

const PAGE_SIZE = 10

interface DistributorCustomersDialogProps {
  open: boolean
  /** 目标分销商用户 ID（B7 path :id） */
  distributorId: number | null
  distributorUsername: string
  onOpenChange: (open: boolean) => void
}

/**
 * G-1 客户列表弹窗（B7 GET /api/attribution/distributor/:id/customers，契约附录 G）。
 * 行形状与 B4 distributorCustomerItem 逐字一致；金额 cents 直用 formatCents 唯一换算。
 * 前端零业务校验：归属关系由服务端 inviter_id 过滤保证。
 */
export function DistributorCustomersDialog({
  open,
  distributorId,
  distributorUsername,
  onOpenChange,
}: DistributorCustomersDialogProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<DistributorCustomerRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  // 请求序号守卫：仅最新一次请求允许写回状态（rate-history-sheet 同款）
  const reqSeq = useRef(0)

  const load = useCallback(async () => {
    if (distributorId == null) return
    const seq = ++reqSeq.current
    setLoading(true)
    try {
      const res = await fetchAdminDistributorCustomers(distributorId, page)
      if (seq !== reqSeq.current) return
      if (res.success && res.data) {
        setRows(res.data.items ?? [])
        setTotal(res.data.total ?? 0)
      }
    } finally {
      if (seq === reqSeq.current) {
        setLoading(false)
      }
    }
  }, [distributorId, page])

  // 关闭或切换目标分销商时重置分页与旧数据（关闭即重置语义）
  useEffect(() => {
    setPage(1)
    setRows([])
    setTotal(0)
  }, [distributorId])

  useEffect(() => {
    if (!open || distributorId == null) return
    void load()
  }, [open, load])

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Customer List')}
      description={`${t('Distributor')} · ${distributorUsername || '-'}`}
    >
      <div className='flex flex-col gap-2'>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Customer')}</TableHead>
              <TableHead>{t('Registered At')}</TableHead>
              <TableHead>{t('Total Topup')}</TableHead>
              <TableHead>{t('Total Commission')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={4} className='h-16 text-center'>
                  {t('No customers found')}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((row) => (
                <TableRow key={row.id}>
                  <TableCell className='font-mono'>{row.username}</TableCell>
                  <TableCell>{formatUnixTime(row.created_at)}</TableCell>
                  <TableCell>{formatCents(row.total_topup_cents)}</TableCell>
                  <TableCell>
                    {formatCents(row.total_commission_cents)}
                  </TableCell>
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
