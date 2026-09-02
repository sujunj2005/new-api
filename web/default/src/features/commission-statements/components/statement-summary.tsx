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
 * Phase 4：管理端只读入口（scope="admin" → A14 /api/commission/statements/summary，跨分销商汇总）
 * Phase 6：组件入共享层供分销商控制台仪表盘可能复用——A14 为 AdminAuth 端点，scope="self"
 *          时服务端无对应端点，组件显示不可用提示而非降级请求（Phase 6 若开放 self 汇总端点，
 *          仅需在 api.ts 增加路径分支，禁止复制本文件派生第二份实现）
 * 复用方式：传 scope prop，禁止复制本文件派生第二份实现
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import {
  fetchStatementSummary,
  formatCents,
  type StatementSummaryData,
  type StatementScope,
} from '../api'

/** 上月 YYYY-MM（汇总视图默认期，与出账引擎「上期出账」语义对齐） */
function lastMonthLabel(): string {
  const d = new Date()
  d.setDate(1)
  d.setMonth(d.getMonth() - 1)
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  return `${y}-${m}`
}

interface StatementSummaryProps {
  scope: StatementScope
}

/**
 * A14 跨分销商汇总（契约附录 D3 冻结形状）：期选择器 + items 表 + totals 卡片。
 * 最小只读：无导出、无图表（契约 D4 范围纪律）。
 */
export function StatementSummary({ scope }: StatementSummaryProps) {
  const { t } = useTranslation()
  const [period, setPeriod] = useState(lastMonthLabel)
  const [summary, setSummary] = useState<StatementSummaryData | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    if (scope !== 'admin') return
    setLoading(true)
    try {
      const res = await fetchStatementSummary(period)
      if (res.success && res.data) {
        setSummary(res.data)
      }
    } finally {
      setLoading(false)
    }
  }, [scope, period])

  useEffect(() => {
    void load()
  }, [load])

  if (scope !== 'admin') {
    return (
      <div className='text-muted-foreground rounded-lg border p-6 text-sm'>
        {t('Summary is only available for administrators')}
      </div>
    )
  }

  const totals = summary?.totals

  return (
    <div className='flex flex-col gap-4'>
      <div className='flex items-center gap-2'>
        <label className='text-sm' htmlFor='statement-summary-period'>
          {t('Period')}
        </label>
        <input
          id='statement-summary-period'
          type='month'
          className='border-input bg-background h-9 rounded-md border px-3 text-sm'
          value={period}
          onChange={(e) => setPeriod(e.target.value)}
        />
      </div>

      <div className='grid grid-cols-1 gap-3 md:grid-cols-3'>
        <Card>
          <CardHeader>
            <CardTitle className='text-sm font-medium'>
              {t('Total Commission')}
            </CardTitle>
          </CardHeader>
          <CardContent className='text-xl font-semibold'>
            {totals ? formatCents(totals.total_commission_cents) : '-'}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className='text-sm font-medium'>
              {t('Adjusted Amount')}
            </CardTitle>
          </CardHeader>
          <CardContent className='text-xl font-semibold'>
            {totals ? formatCents(totals.adjusted_cents) : '-'}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle className='text-sm font-medium'>
              {t('Distributor Count')}
            </CardTitle>
          </CardHeader>
          <CardContent className='text-xl font-semibold'>
            {totals ? totals.distributor_count : '-'}
          </CardContent>
        </Card>
      </div>

      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>{t('Distributor ID')}</TableHead>
            <TableHead>{t('Customer')}</TableHead>
            <TableHead>{t('Total Commission')}</TableHead>
            <TableHead>{t('Adjusted Amount')}</TableHead>
            <TableHead>{t('Status')}</TableHead>
            <TableHead>{t('Statement ID')}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {!summary || summary.items.length === 0 ? (
            <TableRow>
              <TableCell colSpan={6} className='h-16 text-center'>
                {t('No statements found')}
              </TableCell>
            </TableRow>
          ) : (
            summary.items.map((item) => (
              <TableRow key={item.statement_id}>
                <TableCell>{item.distributor_id}</TableCell>
                <TableCell>{item.username || '-'}</TableCell>
                <TableCell>{formatCents(item.total_commission_cents)}</TableCell>
                <TableCell>{formatCents(item.adjusted_cents)}</TableCell>
                <TableCell>{item.statement_status}</TableCell>
                <TableCell>{item.statement_id}</TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
      {loading && (
        <span className='text-muted-foreground text-sm'>{t('Loading...')}</span>
      )}
    </div>
  )
}
