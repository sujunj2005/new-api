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

import { formatUnixTime } from '@/features/distributor-console/api'

import { fetchAttributionChanges, type AttributionChangeRow } from '../api'

const PAGE_SIZE = 10

interface AttributionChangesSheetProps {
  open: boolean
  /** 目标客户（B3 user_id 过滤；null=关闭） */
  userId: number | null
  username: string
  onOpenChange: (open: boolean) => void
}

/**
 * G-3 归属审计抽屉（B3 GET /api/attribution/changes?user_id=，AdminAuth）。
 * 时间线逐行渲染：变更前后归属（inviter id，0=无归属）+ source + operator +
 * 时间 + reason。行形状 = model/attribution_change.go json tag 逐字。
 */
export function AttributionChangesSheet({
  open,
  userId,
  username,
  onOpenChange,
}: AttributionChangesSheetProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<AttributionChangeRow[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [loading, setLoading] = useState(false)
  // 请求序号守卫：仅最新一次请求允许写回状态（rate-history-sheet 同款）
  const reqSeq = useRef(0)

  const load = useCallback(async () => {
    if (userId == null) return
    const seq = ++reqSeq.current
    setLoading(true)
    try {
      const res = await fetchAttributionChanges({ userId, page })
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
  }, [userId, page])

  // 关闭或切换目标用户时重置分页与旧数据（关闭即重置语义）
  useEffect(() => {
    setPage(1)
    setRows([])
    setTotal(0)
  }, [userId])

  useEffect(() => {
    if (!open || userId == null) return
    void load()
  }, [open, load])

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent
        side='right'
        className='flex flex-col gap-4 overflow-y-auto'
      >
        <SheetHeader>
          <SheetTitle>
            {t('Attribution Audit')} · {username || '-'}
          </SheetTitle>
          <SheetDescription>{t('Attribution Changes')}</SheetDescription>
        </SheetHeader>

        {rows.length === 0 ? (
          <div className='text-muted-foreground py-8 text-center text-sm'>
            {t('No attribution changes')}
          </div>
        ) : (
          <div className='flex flex-col gap-3 border-l-2 pl-3'>
            {rows.map((h) => (
              <div key={h.id} className='flex flex-col gap-0.5 text-sm'>
                <span className='font-mono'>
                  #{h.old_inviter_id} → #{h.new_inviter_id}
                </span>
                <span className='text-muted-foreground'>
                  {h.source === 'admin_bind'
                    ? t('Admin Bind')
                    : t('Self Bind')}{' '}
                  · {t('Operator')} #{h.operator_id} ·{' '}
                  {formatUnixTime(h.created_at)}
                </span>
                {h.reason && (
                  <span className='text-muted-foreground text-xs'>
                    {t('Reason')}: {h.reason}
                  </span>
                )}
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
