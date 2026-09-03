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
 * 提现单共享视图组件（D4 禁复制派生，statement-table scope 母本平移）。
 * admin=审核工作台（A17 三过滤 + username 冗余列 + 状态门控操作列 + 查看账单）；
 * self=分销商提现记录（A16 服务端收窄，只读 + 时间线 + 驳回原因/凭证展示，SC5/D-03）。
 * 复用方式：传 scope prop 切换 API 基路径与列收窄，禁止复制本文件派生第二份实现。
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  fetchWithdrawals,
  formatCents,
  formatUnixTime,
  WITHDRAWAL_STATUS_BADGE_VARIANT,
  WITHDRAWAL_STATUS_LABEL_KEYS,
  WITHDRAWAL_TIMELINE_FIELDS,
  type Withdrawal,
  type WithdrawalScope,
  type WithdrawalStatus,
} from '../api'

const PAGE_SIZE = 10

interface WithdrawalTableProps {
  /** admin=审核工作台（A17）；self=分销商本人（A16，隐藏管理列与过滤条/操作列） */
  scope: WithdrawalScope
  /** 受理回调（admin，仅 pending 行渲染；API 调用由调用方组装） */
  onAccept?: (w: Withdrawal) => void
  /** 驳回回调（admin，仅 reviewing 行渲染；弹窗由调用方组装） */
  onReject?: (w: Withdrawal) => void
  /** 批准回调（admin，仅 reviewing 行渲染；确认弹窗由调用方组装） */
  onApprove?: (w: Withdrawal) => void
  /** 打款登记回调（admin，仅 approved 行渲染；voucher 弹窗由调用方组装） */
  onMarkPaid?: (w: Withdrawal) => void
  /** 查看账单回调（admin 全状态行渲染；调用方组装 StatementDetail(scope=admin) 快照明细，WDRAW-03） */
  onOpenStatement?: (w: Withdrawal) => void
  /** 变更时重取列表（申请/受理/驳回/批准/打款成功后由调用方 ++） */
  refreshKey?: number
}

/** 提现单状态徽标（标签走常量映射，不透传服务端字符串 T-05-02-02） */
function WithdrawalStatusBadge({ status }: { status: WithdrawalStatus }) {
  const { t } = useTranslation()
  return (
    <Badge variant={WITHDRAWAL_STATUS_BADGE_VARIANT[status]}>
      {t(WITHDRAWAL_STATUS_LABEL_KEYS[status])}
    </Badge>
  )
}

/** 时间线单元格（时间线五列，空值 0 不展示） */
function TimelineCell({ w }: { w: Withdrawal }) {
  const { t } = useTranslation()
  const marks = WITHDRAWAL_TIMELINE_FIELDS.filter((f) => (w[f.key] as number) > 0)
  return (
    <div className='flex flex-col gap-0.5 text-xs whitespace-nowrap'>
      {marks.map((f) => (
        <div key={f.key}>
          <span className='text-muted-foreground'>{t(f.labelKey)}: </span>
          {formatUnixTime(w[f.key] as number)}
        </div>
      ))}
    </div>
  )
}

/**
 * 提现单列表（A16/A17）：admin distributor_id/status 过滤 + 分页 + 状态门控操作列；
 * self 只读（时间线 + 驳回原因 + 凭证号，SC5/D-03）。
 */
export function WithdrawalTable({
  scope,
  onAccept,
  onReject,
  onApprove,
  onMarkPaid,
  onOpenStatement,
  refreshKey,
}: WithdrawalTableProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<Withdrawal[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [distributorInput, setDistributorInput] = useState('')
  const [distributorFilter, setDistributorFilter] = useState<number | undefined>(
    undefined
  )
  const [status, setStatus] = useState('')
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchWithdrawals(scope, {
        page,
        pageSize: PAGE_SIZE,
        distributorId: distributorFilter,
        status: status || undefined,
      })
      if (res.success && res.data) {
        setRows(res.data.items ?? [])
        setTotal(res.data.total ?? 0)
      }
    } finally {
      setLoading(false)
    }
  }, [scope, page, distributorFilter, status])

  useEffect(() => {
    void load()
  }, [load, refreshKey])

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className='flex flex-col gap-4'>
      {/* 过滤条：self 场景服务端已收窄数据，隐藏过滤（列收窄由 scope 驱动） */}
      {scope === 'admin' && (
        <div className='flex flex-wrap items-center gap-2'>
          <Input
            className='w-36'
            placeholder={t('Distributor ID')}
            value={distributorInput}
            onChange={(e) => setDistributorInput(e.target.value)}
            onBlur={() => {
              const v = Number(distributorInput)
              setPage(1)
              setDistributorFilter(
                distributorInput && Number.isFinite(v) && v > 0 ? v : undefined
              )
            }}
          />
          <select
            className='border-input bg-background h-9 rounded-md border px-3 text-sm'
            value={status}
            onChange={(e) => {
              setPage(1)
              setStatus(e.target.value)
            }}
          >
            <option value=''>{t('All Status')}</option>
            <option value='pending'>pending</option>
            <option value='reviewing'>reviewing</option>
            <option value='approved'>approved</option>
            <option value='paid'>paid</option>
            <option value='rejected'>rejected</option>
          </select>
        </div>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Withdrawal No')}</TableHead>
            {scope === 'admin' && <TableHead>{t('Distributor')}</TableHead>}
            <TableHead>{t('Period')}</TableHead>
            <TableHead>{t('Settle Amount')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Reject Reason')}</TableHead>
            <TableHead>{t('Voucher No')}</TableHead>
            {scope === 'self' && <TableHead>{t('Timeline')}</TableHead>}
            {scope === 'admin' && <TableHead>{t('Actions')}</TableHead>}
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={scope === 'admin' ? 8 : 7}
                className='text-muted-foreground h-24 text-center'
              >
                {t('No withdrawals found')}
              </TableCell>
            </TableRow>
          ) : (
            rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell className='font-mono'>{row.withdrawal_no}</TableCell>
                {scope === 'admin' && (
                  <TableCell>{row.username || row.distributor_id}</TableCell>
                )}
                <TableCell>{row.period}</TableCell>
                <TableCell>{formatCents(row.settle_amount_cents)}</TableCell>
                <TableCell>
                  <WithdrawalStatusBadge status={row.status} />
                </TableCell>
                <TableCell className='max-w-48 break-words'>
                  {row.status === 'rejected' && row.reason ? row.reason : '-'}
                </TableCell>
                <TableCell className='font-mono'>
                  {row.status === 'paid' && row.voucher_no
                    ? row.voucher_no
                    : '-'}
                </TableCell>
                {scope === 'self' && (
                  <TableCell>
                    <TimelineCell w={row} />
                  </TableCell>
                )}
                {scope === 'admin' && (
                  <TableCell>
                    <div className='flex flex-wrap gap-1'>
                      {row.status === 'pending' && onAccept && (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => onAccept(row)}
                        >
                          {t('Accept')}
                        </Button>
                      )}
                      {row.status === 'reviewing' && onReject && (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => onReject(row)}
                        >
                          {t('Reject')}
                        </Button>
                      )}
                      {row.status === 'reviewing' && onApprove && (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => onApprove(row)}
                        >
                          {t('Approve')}
                        </Button>
                      )}
                      {row.status === 'approved' && onMarkPaid && (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => onMarkPaid(row)}
                        >
                          {t('Mark Paid')}
                        </Button>
                      )}
                      {onOpenStatement && (
                        <Button
                          variant='ghost'
                          size='sm'
                          onClick={() => onOpenStatement(row)}
                        >
                          {t('View Statement')}
                        </Button>
                      )}
                    </div>
                  </TableCell>
                )}
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
  )
}
