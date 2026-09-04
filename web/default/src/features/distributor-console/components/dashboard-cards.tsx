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
 * 分销商控制台 overview 四卡（CUST-03/D-04）。
 * 数据面唯一事实源 = A9 服务端聚合（会话派生），前端直用 cents/count 渲染，
 * 零金额二次计算（CONTEXT specifics：任何汇总/换算只允许展示层 formatCents）。
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
  fetchDashboard,
  formatCents,
  type CommissionDashboard,
} from '../api'

interface DashboardCardsProps {
  /** 页级联动刷新键（提现申请/驳回/打款后四卡重取，Pitfall 5） */
  refreshKey?: number
}

/** overview 四卡：当期未出账 / 历史已出账 / 已提现 / 待提现（各配笔数副文本） */
export function DashboardCards({ refreshKey }: DashboardCardsProps) {
  const { t } = useTranslation()
  const [dash, setDash] = useState<CommissionDashboard | null>(null)
  const [loading, setLoading] = useState(false)

  const load = useCallback(async () => {
    setLoading(true)
    try {
      const res = await fetchDashboard()
      if (res.success && res.data) {
        setDash(res.data) // 服务端聚合值直用，禁前端再算
      }
    } finally {
      setLoading(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load, refreshKey])

  const cards = [
    {
      titleKey: 'Current Pending',
      cents: dash?.current_pending_cents,
      count: dash?.current_pending_count,
    },
    {
      titleKey: 'Total Statemented',
      cents: dash?.total_statement_cents,
      count: dash?.total_statement_count,
    },
    {
      titleKey: 'Total Withdrawn',
      cents: dash?.total_withdrawn_cents,
      count: dash?.total_withdrawn_count,
    },
    {
      titleKey: 'Pending Withdraw',
      cents: dash?.pending_withdraw_cents,
      count: dash?.pending_withdraw_count,
    },
  ]

  return (
    <div className='flex flex-col gap-3'>
      <div className='grid grid-cols-1 gap-3 md:grid-cols-2 xl:grid-cols-4'>
        {cards.map((card) => (
          <Card key={card.titleKey}>
            <CardHeader>
              <CardTitle className='text-sm font-medium'>
                {t(card.titleKey)}
              </CardTitle>
            </CardHeader>
            <CardContent className='text-xl font-semibold'>
              {card.cents === undefined ? '-' : formatCents(card.cents)}
              <div className='text-muted-foreground mt-1 text-xs font-normal'>
                {card.count === undefined ? '-' : `${card.count} ${t('Records')}`}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>
      {loading && (
        <span className='text-muted-foreground text-sm'>{t('Loading...')}</span>
      )}
    </div>
  )
}
