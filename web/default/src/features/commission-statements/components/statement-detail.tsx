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
 * Phase 4：管理端只读入口（scope="admin" → A5 详情 + A6 明细，全分销商视角）
 * Phase 6：分销商控制台复用（scope="self" → A11 明细 /api/commission/statements/self/:id/items，
 *          账单本体由 A10 列表行数据直接传入——契约无 self 详情端点，adjustments 区块仅 admin 展示）
 * 复用方式：传 scope prop 切换明细 API 基路径，禁止复制本文件派生第二份实现
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'

import { rateBpToPercent } from '@/features/commission-rates/api'

import {
  fetchStatementDetail,
  fetchStatementItems,
  formatCents,
  formatUnixTime,
  type CommissionStatement,
  type StatementAdjustment,
  type StatementItemWithUsername,
  type StatementScope,
} from '../api'

const ITEM_PAGE_SIZE = 10

interface StatementDetailProps {
  scope: StatementScope
  /** 账单本体（来自列表行数据；self 场景同样适用，Phase 6 复用点） */
  statement: CommissionStatement
  onClose?: () => void
}

/**
 * 账单详情（A5/A6）：本体字段 + adjustments 时间线（仅 admin）+ 充值明细分页表
 * （单号/支付方式/充值金额/佣金金额/客户/比例/到账时间）。最小只读，无调账操作（A7/A8 UI 不在本期）。
 */
export function StatementDetail({ scope, statement, onClose }: StatementDetailProps) {
  const { t } = useTranslation()
  const [adjustments, setAdjustments] = useState<StatementAdjustment[]>([])
  const [items, setItems] = useState<StatementItemWithUsername[]>([])
  const [itemsTotal, setItemsTotal] = useState(0)
  const [itemsPage, setItemsPage] = useState(1)
  const [loading, setLoading] = useState(false)

  // 切换账单目标（statement.id 变化）时重置分页与旧数据：本组件为非模态内联面板，
  // 父级直接换 statement 复用同一实例，不重置会把 A 账单的旧页码/明细/调整单错挂到 B 账单标题下
  useEffect(() => {
    setItemsPage(1)
    setItems([])
    setItemsTotal(0)
    setAdjustments([])
  }, [statement.id])

  // adjustments 仅 admin 可取（A5 AdminAuth）；self 契约无对应端点，保持空数组
  useEffect(() => {
    if (scope !== 'admin') return
    let cancelled = false
    void fetchStatementDetail(statement.id).then((res) => {
      if (!cancelled && res.success && res.data) {
        setAdjustments(res.data.adjustments ?? [])
      }
    })
    return () => {
      cancelled = true
    }
  }, [scope, statement.id])

  const loadItems = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchStatementItems(
        scope,
        statement.id,
        itemsPage,
        ITEM_PAGE_SIZE
      )
      if (res.success && res.data) {
        setItems(res.data.items ?? [])
        setItemsTotal(res.data.total ?? 0)
      }
    } finally {
      setLoading(false)
    }
  }, [scope, statement.id, itemsPage])

  useEffect(() => {
    void loadItems()
  }, [loadItems])

  const itemTotalPages = Math.max(1, Math.ceil(itemsTotal / ITEM_PAGE_SIZE))

  return (
    <div className='flex flex-col gap-4 rounded-lg border p-4'>
      <div className='flex items-center justify-between'>
        <h3 className='text-base font-semibold'>
          {t('Statement Detail')} · {statement.period}
        </h3>
        {onClose && (
          <Button variant='ghost' size='sm' onClick={onClose}>
            {t('Close')}
          </Button>
        )}
      </div>

      <div className='grid grid-cols-2 gap-2 text-sm md:grid-cols-4'>
        <div>
          <span className='text-muted-foreground'>{t('Period')}: </span>
          {statement.period}
        </div>
        {scope === 'admin' && (
          <div>
            <span className='text-muted-foreground'>{t('Distributor ID')}: </span>
            {statement.distributor_id}
          </div>
        )}
        <div>
          <span className='text-muted-foreground'>{t('Total Commission')}: </span>
          {formatCents(statement.total_commission_cents)}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('Adjusted Amount')}: </span>
          {formatCents(statement.adjusted_cents)}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('Settle Amount')}: </span>
          {formatCents(statement.settle_amount_cents)}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('Status')}: </span>
          {statement.status}
        </div>
        <div>
          <span className='text-muted-foreground'>{t('Created At')}: </span>
          {formatUnixTime(statement.created_at)}
        </div>
      </div>

      {/* adjustments 时间线（admin 视角；A8 调整单由 04-03 API 产出） */}
      {scope === 'admin' && (
        <div className='flex flex-col gap-1'>
          <span className='text-sm font-medium'>{t('Adjustments')}</span>
          {adjustments.length === 0 ? (
            <span className='text-muted-foreground text-sm'>
              {t('No adjustments')}
            </span>
          ) : (
            <div className='flex flex-col gap-1 border-l-2 pl-3'>
              {adjustments.map((adj) => (
                <div key={adj.id} className='text-sm'>
                  <span className='font-mono'>
                    {adj.delta_cents >= 0 ? '+' : ''}
                    {formatCents(adj.delta_cents)}
                  </span>
                  <span className='text-muted-foreground'>
                    {' '}
                    · {adj.reason} · {formatUnixTime(adj.created_at)}
                  </span>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      {/* 充值明细分页表（A6/A11，含客户 username 冗余） */}
      <div className='flex flex-col gap-2'>
        <span className='text-sm font-medium'>{t('Statement Items')}</span>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t('Billing No')}</TableHead>
              <TableHead>{t('Payment Method')}</TableHead>
              <TableHead>{t('Topup Amount')}</TableHead>
              <TableHead>{t('Commission Amount')}</TableHead>
              <TableHead>{t('Customer')}</TableHead>
              <TableHead>{t('Rate')}</TableHead>
              <TableHead>{t('Complete Time')}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {items.length === 0 ? (
              <TableRow>
                <TableCell colSpan={7} className='h-16 text-center'>
                  {t('No statements found')}
                </TableCell>
              </TableRow>
            ) : (
              items.map((item) => (
                <TableRow key={item.id}>
                  <TableCell className='font-mono'>{item.billing_no}</TableCell>
                  <TableCell>{item.payment_method}</TableCell>
                  <TableCell>{formatCents(item.topup_money_cents)}</TableCell>
                  <TableCell>{formatCents(item.commission_cents)}</TableCell>
                  <TableCell>{item.username || item.customer_id}</TableCell>
                  {/* 比例列（SC-7）：双 scope 均显示零分支；无关联流水为 0 时展示占位符；换算走唯一函数禁内联 */}
                  <TableCell>
                    {item.rate_bp ? rateBpToPercent(item.rate_bp) : '-'}
                  </TableCell>
                  <TableCell>{formatUnixTime(item.complete_time)}</TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
        <div className='flex items-center justify-end gap-2 text-sm'>
          <span>
            {t('Page')} {itemsPage} / {itemTotalPages} · {itemsTotal}{' '}
            {t('Records')}
          </span>
          <Button
            variant='outline'
            size='sm'
            disabled={itemsPage <= 1 || loading}
            onClick={() => setItemsPage((p) => Math.max(1, p - 1))}
          >
            {t('Previous')}
          </Button>
          <Button
            variant='outline'
            size='sm'
            disabled={itemsPage >= itemTotalPages || loading}
            onClick={() => setItemsPage((p) => p + 1)}
          >
            {t('Next')}
          </Button>
        </div>
      </div>
    </div>
  )
}
