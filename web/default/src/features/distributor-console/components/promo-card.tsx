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
 * G-4 分销商推广卡（B6 GET /api/distributor/profile，CUST-02 展示面）。
 * aff_code/aff_link/customer_count 直用服务端值零换算（aff_link 服务端已拼好）；
 * 一键复制走 useCopyToClipboard 既有入口（禁手写剪贴板逻辑）。
 */
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import {
  Card,
  CardContent,
  CardHeader,
  CardTitle,
} from '@/components/ui/card'
import { useCopyToClipboard } from '@/hooks/use-copy-to-clipboard'

import { fetchDistributorProfile, type DistributorProfile } from '../api'

interface PromoCardProps {
  /** 页级联动刷新键（Pitfall 5，与四卡一致） */
  refreshKey?: number
}

export function PromoCard({ refreshKey }: PromoCardProps) {
  const { t } = useTranslation()
  const [profile, setProfile] = useState<DistributorProfile | null>(null)
  const { copyToClipboard, copiedText } = useCopyToClipboard({ notify: false })

  const load = useCallback(async () => {
    try {
      const res = await fetchDistributorProfile()
      if (res.success && res.data) {
        setProfile(res.data) // 服务端值直用，零前端换算
      }
    } catch {
      // 静默失败：推广卡为辅助信息，不阻塞 overview 其余卡片
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load, refreshKey])

  return (
    <Card>
      <CardHeader>
        <CardTitle className='text-sm font-medium'>
          {t('Promotion')}
        </CardTitle>
      </CardHeader>
      <CardContent className='flex flex-col gap-2'>
        <div className='flex items-center justify-between gap-2'>
          <div className='min-w-0'>
            <div className='text-muted-foreground text-xs'>
              {t('Promo Code')}
            </div>
            <div className='truncate font-mono text-sm font-semibold'>
              {profile?.aff_code ?? '-'}
            </div>
          </div>
          <Button
            variant='outline'
            size='sm'
            disabled={!profile?.aff_code}
            onClick={() => copyToClipboard(profile?.aff_code ?? '')}
          >
            {copiedText === profile?.aff_code ? t('Copied') : t('Copy')}
          </Button>
        </div>
        <div className='flex items-center justify-between gap-2'>
          <div className='min-w-0'>
            <div className='text-muted-foreground text-xs'>
              {t('Promotion Link')}
            </div>
            <div className='text-muted-foreground truncate text-xs'>
              {profile?.aff_link ?? '-'}
            </div>
          </div>
          <Button
            variant='outline'
            size='sm'
            disabled={!profile?.aff_link}
            onClick={() => copyToClipboard(profile?.aff_link ?? '')}
          >
            {copiedText === profile?.aff_link ? t('Copied') : t('Copy Link')}
          </Button>
        </div>
        <div className='text-muted-foreground text-xs'>
          {t('Total Customers')}: {profile?.customer_count ?? '-'}
        </div>
      </CardContent>
    </Card>
  )
}
