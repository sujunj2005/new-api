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
 * 对账单共享视图组件（契约附录 D4）
 * Phase 4：管理端只读入口（scope="admin" → /api/commission/statements*，全分销商视角）
 * Phase 6：分销商控制台复用（scope="self" → /api/commission/statements/self*，DistributorAuth 数据收窄）
 * 复用方式：传 scope prop 切换 API 基路径与列收窄，禁止复制本文件派生第二份实现
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

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
  fetchStatements,
  formatCents,
  formatUnixTime,
  type CommissionStatement,
  type StatementScope,
} from '../api'

const PAGE_SIZE = 10

interface StatementTableProps {
  /** admin=全分销商视角（A4 三过滤）；self=分销商本人（A10，Phase 6 复用，隐藏分销商列与过滤条） */
  scope: StatementScope
  /** 固定分销商过滤（self 场景由服务端收窄，无需传） */
  distributorId?: number
  /** 行点击回调（管理端打开详情；Phase 6 self 场景同样可挂明细跳转） */
  onOpenDetail?: (statement: CommissionStatement) => void
  /** Phase 5 additive：self 视角 payable 行「申请提现」回调（三条件齐备才渲染，undefined 时 admin 零影响，D4 禁复制派生） */
  onApplyWithdraw?: (statement: CommissionStatement) => void
}

/**
 * 对账单列表（A4/A10）：period/distributor_id/status 过滤条 + 分页表格。
 * 最小只读：无导出（契约 D4 范围纪律）；Phase 5 additive 行内「申请提现」入口。
 */
export function StatementTable({
  scope,
  distributorId,
  onOpenDetail,
  onApplyWithdraw,
}: StatementTableProps) {
  const { t } = useTranslation()
  const [rows, setRows] = useState<CommissionStatement[]>([])
  const [total, setTotal] = useState(0)
  const [page, setPage] = useState(1)
  const [period, setPeriod] = useState('')
  const [status, setStatus] = useState('')
  const [distributorInput, setDistributorInput] = useState('')
  const [distributorFilter, setDistributorFilter] = useState<number | undefined>(
    distributorId
  )
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchStatements(scope, {
        page,
        pageSize: PAGE_SIZE,
        distributorId: distributorFilter,
        period: period || undefined,
        status: status || undefined,
      })
      if (res.success && res.data) {
        setRows(res.data.items ?? [])
        setTotal(res.data.total ?? 0)
      }
    } finally {
      setLoading(false)
    }
  }, [scope, page, distributorFilter, period, status])

  useEffect(() => {
    void load()
  }, [load])

  const totalPages = Math.max(1, Math.ceil(total / PAGE_SIZE))

  return (
    <div className='flex flex-col gap-4'>
      {/* 过滤条：self 场景服务端已收窄数据，隐藏分销商/期过滤（列收窄由 scope 驱动） */}
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
          <Input
            className='w-36'
            placeholder={t('Period')}
            value={period}
            onChange={(e) => {
              setPage(1)
              setPeriod(e.target.value)
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
            <option value='payable'>payable</option>
            <option value='withdrawing'>withdrawing</option>
            <option value='settled'>settled</option>
          </select>
        </div>
      )}

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Period')}</TableHead>
            {scope === 'admin' && <TableHead>{t('Distributor ID')}</TableHead>}
            <TableHead>{t('Total Commission')}</TableHead>
            <TableHead>{t('Adjusted Amount')}</TableHead>
            <TableHead>{t('Settle Amount')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Created At')}</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.length === 0 ? (
            <TableRow>
              <TableCell
                colSpan={scope === 'admin' ? 8 : 7}
                className='text-muted-foreground h-24 text-center'
              >
                {t('No statements found')}
              </TableCell>
            </TableRow>
          ) : (
            rows.map((row) => (
              <TableRow key={row.id}>
                <TableCell>{row.period}</TableCell>
                {scope === 'admin' && (
                  <TableCell>{row.distributor_id}</TableCell>
                )}
                <TableCell>{formatCents(row.total_commission_cents)}</TableCell>
                <TableCell>{formatCents(row.adjusted_cents)}</TableCell>
                <TableCell>{formatCents(row.settle_amount_cents)}</TableCell>
                <TableCell>{row.status}</TableCell>
                <TableCell>{formatUnixTime(row.created_at)}</TableCell>
                <TableCell>
                  <div className='flex flex-wrap gap-1'>
                    {onOpenDetail && (
                      <Button
                        variant='outline'
                        size='sm'
                        onClick={() => onOpenDetail(row)}
                      >
                        {t('Detail')}
                      </Button>
                    )}
                    {/* Phase 5 additive：self + payable + 回调齐备才出「申请提现」（undefined 时行为与现状一致） */}
                    {scope === 'self' &&
                      row.status === 'payable' &&
                      onApplyWithdraw && (
                        <Button
                          variant='outline'
                          size='sm'
                          onClick={() => onApplyWithdraw(row)}
                        >
                          {t('Apply Withdrawal')}
                        </Button>
                      )}
                  </div>
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
  )
}
