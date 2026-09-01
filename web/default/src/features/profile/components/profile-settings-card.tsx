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
import { Link2, Settings, UserRoundPlus } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import { Skeleton } from '@/components/ui/skeleton'
import { Tabs, TabsContent, TabsList, TabsTrigger } from '@/components/ui/tabs'
import { TitledCard } from '@/components/ui/titled-card'
import { useDialog } from '@/hooks/use-dialog'

import type { UserProfile } from '../types'
import { AffRebindDialog } from './dialogs/aff-rebind-dialog'
import { AccountBindingsTab } from './tabs/account-bindings-tab'
import { NotificationTab } from './tabs/notification-tab'

// ============================================================================
// Profile Settings Card Component
// ============================================================================

interface ProfileSettingsCardProps {
  profile: UserProfile | null
  loading: boolean
  onProfileUpdate: () => void
  affRebind: (data: { aff_code: string }) => Promise<boolean>
}

export function ProfileSettingsCard({
  profile,
  loading,
  onProfileUpdate,
  affRebind,
}: ProfileSettingsCardProps) {
  const { t } = useTranslation()
  const [activeTab, setActiveTab] = useState('bindings')
  const [rebindOpen, rebindHandlers] = useDialog()

  // 归属状态（契约 §4.1 M2）：仅 InviterId=0 且 rebind_available=true 时显示补绑入口
  const attribution = profile?.attribution
  const inviterId = attribution?.inviter_id ?? 0
  const rebindAvailable = attribution?.rebind_available === true

  let attributionDesc: string | null = null
  if (attribution) {
    if (inviterId !== 0) {
      attributionDesc = t('Bound to distributor ID: {{id}}', { id: inviterId })
    } else if (rebindAvailable) {
      attributionDesc = t('Bind Distributor')
    } else {
      attributionDesc = t(
        'Rebind window has expired. Please contact the administrator.'
      )
    }
  }

  if (loading) {
    return (
      <Card data-card-hover='false' className='gap-0 overflow-hidden py-0'>
        <CardHeader className='border-b p-3 !pb-3 sm:p-5 sm:!pb-5'>
          <Skeleton className='h-6 w-32' />
          <Skeleton className='mt-2 h-4 w-48' />
        </CardHeader>
        <CardContent className='space-y-4 p-3 sm:p-5'>
          <Skeleton className='h-10 w-full' />
          {Array.from({ length: 3 }).map((_, i) => (
            <Skeleton key={i} className='h-20 w-full' />
          ))}
        </CardContent>
      </Card>
    )
  }

  return (
    <TitledCard
      title={t('Settings')}
      description={t('Configure your account preferences and integrations')}
      icon={<Settings className='h-4 w-4' />}
      disableHoverEffect
    >
      <Tabs value={activeTab} onValueChange={setActiveTab}>
        <TabsList className='grid w-full grid-cols-2 items-stretch gap-1 rounded-xl p-1 group-data-horizontal/tabs:h-10'>
          <TabsTrigger
            value='bindings'
            className='h-full gap-2 rounded-lg px-3 py-0 leading-none'
          >
            <Link2 className='h-4 w-4' />
            <span className='hidden sm:inline'>{t('Account Bindings')}</span>
            <span className='sm:hidden'>{t('Bindings')}</span>
          </TabsTrigger>
          <TabsTrigger
            value='settings'
            className='h-full gap-2 rounded-lg px-3 py-0 leading-none'
          >
            <Settings className='h-4 w-4' />
            <span className='hidden sm:inline'>
              {t('Settings & Preferences')}
            </span>
            <span className='sm:hidden'>{t('Settings')}</span>
          </TabsTrigger>
        </TabsList>

        <TabsContent value='bindings' className='mt-4 space-y-4 sm:mt-6'>
          {/* 归属绑定区块（业务关系，与 OAuth 登录绑定视觉区分）：
              仅服务端返回 attribution 字段时渲染（后端未部署/旧版本时优雅降级） */}
          {attribution && (
            <div className='flex items-center justify-between gap-2.5 rounded-lg border p-2.5 sm:gap-3 sm:p-3'>
              <div className='flex min-w-0 items-center gap-2.5 sm:gap-3'>
                <div className='bg-muted shrink-0 rounded-md p-1.5 sm:p-2'>
                  <UserRoundPlus className='h-4 w-4' />
                </div>
                <div className='min-w-0'>
                  <p className='text-sm font-medium'>{t('Attribution')}</p>
                  <p className='text-muted-foreground truncate text-xs'>
                    {attributionDesc}
                  </p>
                </div>
              </div>
              {inviterId === 0 && rebindAvailable && (
                <Button
                  variant='outline'
                  size='sm'
                  className='h-7 shrink-0 px-2.5 text-xs'
                  onClick={rebindHandlers.open}
                >
                  {t('Bind Distributor')}
                </Button>
              )}
            </div>
          )}
          <AccountBindingsTab profile={profile} onUpdate={onProfileUpdate} />
          <AffRebindDialog
            open={rebindOpen}
            onOpenChange={(open) =>
              open ? rebindHandlers.open() : rebindHandlers.close()
            }
            onSuccess={onProfileUpdate}
            onRebind={affRebind}
          />
        </TabsContent>

        <TabsContent value='settings' className='mt-4 sm:mt-6'>
          <NotificationTab profile={profile} onUpdate={onProfileUpdate} />
        </TabsContent>
      </Tabs>
    </TitledCard>
  )
}
