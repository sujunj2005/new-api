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

import { Button } from '@/components/ui/button'
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from '@/components/ui/sheet'

import {
  fetchCommissionRateHistory,
  formatUnixTime,
  rateBpToPercent,
  type RateHistoryRow,
} from '../api'

const PAGE_SIZE = 10

interface RateHistorySheetProps {
  /** 目标分销商（null=关闭） */
  distributorId: number | null
  onOpenChange: (open: boolean) => void
}

/**
 * 比例变更历史抽屉（A3，RATE-03）：变更时间线逐行渲染
 * old_rate_bp→new_rate_bp（两侧同经唯一换算函数）+ operator_id + created_at。
 * 字段名以 model/commission_rate.go json tag 逐字（全称 old_rate_bp/new_rate_bp，禁简写）。
 */
export function RateHistorySheet({
  distributorId,
  onOpenChange,
}: RateHistorySheetProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<RateHistoryRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  // 请求序号守卫：仅最新一次请求允许写回状态，防止上一个目标的在途响应乱序覆盖新数据
  const reqSeq = useRef(0)

  const load = useCallback(async () => {
    if (distributorId == null) return
    const seq = ++reqSeq.current
    setLoading(true)
    try {
      const res = await fetchCommissionRateHistory(distributorId, page)
      if (seq !== reqSeq.current) return
      if (res.success && res.data) {
        setRows(res.data.items ?? [])
        setTotal(res.data.total ?? 0)
      }
    } finally {
      // 旧请求不得复位新请求持有的 loading 态
      if (seq === reqSeq.current) {
        setLoading(false)
      }
    }
  }, [distributorId, page])

  // 关闭或切换目标分销商时重置分页与旧数据（对齐 customer-topups-dialog 关闭即重置语义）：
  // 重开新分销商时 page 已为 1，只发一个干净请求，不携带旧页码
  useEffect(() => {
    setPage(1)
    setRows([])
    setTotal(0)
  }, [distributorId])

  useEffect(() => {
    if (distributorId == null) return
    void load()
  }, [load])

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <Sheet open={distributorId != null} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex flex-col gap-4 overflow-y-auto'
      >
        <SheetHeader>
          <SheetTitle>
            {t('Rate History')} · {t('Distributor ID')} {distributorId ?? '-'}
          </SheetTitle>
          <SheetDescription>{t('Rate History')}</SheetDescription>
        </SheetHeader>

        {rows.length === 0 ? (
          <div className='text-muted-foreground py-8 text-center text-sm'>
            {t('No rate history')}
          </div>
        ) : (
          <div className='flex flex-col gap-3 border-l-2 pl-3'>
            {rows.map((h) => (
              <div key={h.id} className='flex flex-col gap-0.5 text-sm'>
                <span className='font-mono'>
                  {rateBpToPercent(h.old_rate_bp)} →{' '}
                  {rateBpToPercent(h.new_rate_bp)}
                </span>
                <span className='text-muted-foreground'>
                  {t('Operator')} #{h.operator_id} ·{' '}
                  {formatUnixTime(h.created_at)}
                </span>
              </div>
            ))}
          </div>
        )}

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
      </SheetContent>
    </Sheet>
  )
}
