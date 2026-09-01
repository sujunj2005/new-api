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
import { zodResolver } from '@hookform/resolvers/zod'
import { useForm, type Resolver } from 'react-hook-form'
import { useTranslation } from 'react-i18next'
import { z } from 'zod'

import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from '@/components/ui/form'
import { Input } from '@/components/ui/input'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'

import {
  SettingsForm,
  SettingsSwitchContent,
  SettingsSwitchItem,
} from '../../components/settings-form-layout'
import { SettingsPageFormActions } from '../../components/settings-page-context'
import { SettingsSection } from '../../components/settings-section'
import { useUpdateOption } from '../../hooks/use-update-option'

// 值域校验（契约 §2.4 + §3.4）：payout_day 8~31、days 1~365、mode ∈ {days, unlimited}。
// 前端校验非权威（仅 UX 层禁用提交），服务端 controller/option.go UpdateOption 已强制（02-03 Task 2）。
const schema = z.object({
  CommissionEnabled: z.boolean(),
  CommissionPayoutDay: z.coerce.number().int().min(8).max(31),
  AffRebindWindowMode: z.enum(['days', 'unlimited']),
  AffRebindWindowDays: z.coerce.number().int().min(1).max(365),
})

type Values = z.infer<typeof schema>

export function CommissionSection({
  defaultValues,
}: {
  defaultValues: {
    CommissionEnabled: boolean
    CommissionPayoutDay: number
    AffRebindWindowMode: 'days' | 'unlimited'
    AffRebindWindowDays: number
  }
}) {
  const { t } = useTranslation()
  const updateOption = useUpdateOption()

  const form = useForm<Values>({
    resolver: zodResolver(schema) as unknown as Resolver<Values>,
    mode: 'onChange',
    defaultValues: {
      CommissionEnabled: defaultValues.CommissionEnabled,
      CommissionPayoutDay: defaultValues.CommissionPayoutDay,
      AffRebindWindowMode: defaultValues.AffRebindWindowMode,
      AffRebindWindowDays: defaultValues.AffRebindWindowDays,
    },
  })

  const { isDirty, isSubmitting, isValid } = form.formState
  const windowMode = form.watch('AffRebindWindowMode')

  async function onSubmit(values: Values) {
    const updates: Array<{ key: string; value: string }> = []

    if (values.CommissionEnabled !== defaultValues.CommissionEnabled) {
      updates.push({
        key: 'CommissionEnabled',
        value: String(values.CommissionEnabled),
      })
    }

    if (values.CommissionPayoutDay !== defaultValues.CommissionPayoutDay) {
      updates.push({
        key: 'CommissionPayoutDay',
        value: String(values.CommissionPayoutDay),
      })
    }

    if (values.AffRebindWindowMode !== defaultValues.AffRebindWindowMode) {
      updates.push({
        key: 'AffRebindWindowMode',
        value: values.AffRebindWindowMode,
      })
    }

    if (values.AffRebindWindowDays !== defaultValues.AffRebindWindowDays) {
      updates.push({
        key: 'AffRebindWindowDays',
        value: String(values.AffRebindWindowDays),
      })
    }

    if (updates.length === 0) {
      return
    }

    // 逐键写 /api/option/（useUpdateOption 复用现有端点，M5 无新路由；
    // 02-01 已注册 updateOptionMap 热更新 case，保存即时生效）
    for (const update of updates) {
      await updateOption.mutateAsync(update)
    }

    form.reset(values)
  }

  return (
    <SettingsSection title={t('Commission')}>
      <Form {...form}>
        <SettingsForm onSubmit={form.handleSubmit(onSubmit)} autoComplete='off'>
          <SettingsPageFormActions
            onSave={form.handleSubmit(onSubmit)}
            isSaving={updateOption.isPending || isSubmitting}
            isSaveDisabled={!isDirty || !isValid}
            saveLabel='Save commission settings'
          />

          <FormField
            control={form.control}
            name='CommissionEnabled'
            render={({ field }) => (
              <SettingsSwitchItem>
                <SettingsSwitchContent>
                  <FormLabel>{t('Commission Enabled')}</FormLabel>
                  <FormDescription>
                    {t('Enable commission settlement for distributors')}
                  </FormDescription>
                </SettingsSwitchContent>
                <FormControl>
                  <Switch
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={updateOption.isPending || isSubmitting}
                  />
                </FormControl>
              </SettingsSwitchItem>
            )}
          />

          <div className='grid gap-6 sm:grid-cols-2'>
            <FormField
              control={form.control}
              name='CommissionPayoutDay'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Commission Payout Day')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={8}
                      max={31}
                      placeholder='8'
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {t(
                      'Day of the month for issuing statements, between 8 and 31'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AffRebindWindowMode'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rebind Window Mode')}</FormLabel>
                  <Select
                    items={[
                      { value: 'days', label: t('Days') },
                      { value: 'unlimited', label: t('Unlimited') },
                    ]}
                    value={field.value}
                    onValueChange={(value) =>
                      value !== null &&
                      field.onChange(value as 'days' | 'unlimited')
                    }
                  >
                    <SelectTrigger className='w-[160px]'>
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent alignItemWithTrigger={false}>
                      <SelectGroup>
                        <SelectItem value='days'>{t('Days')}</SelectItem>
                        <SelectItem value='unlimited'>
                          {t('Unlimited')}
                        </SelectItem>
                      </SelectGroup>
                    </SelectContent>
                  </Select>
                  <FormDescription>
                    {t(
                      'Whether unbound customers may self-rebind: within days after registration, or anytime'
                    )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />

            <FormField
              control={form.control}
              name='AffRebindWindowDays'
              render={({ field }) => (
                <FormItem>
                  <FormLabel>{t('Rebind Window Days')}</FormLabel>
                  <FormControl>
                    <Input
                      type='number'
                      min={1}
                      max={365}
                      placeholder='7'
                      disabled={
                        windowMode === 'unlimited' ||
                        updateOption.isPending ||
                        isSubmitting
                      }
                      {...field}
                    />
                  </FormControl>
                  <FormDescription>
                    {windowMode === 'unlimited'
                      ? t('Only applies in days mode')
                      : t(
                          'Days in which an unbound customer can self-rebind after registration, between 1 and 365'
                        )}
                  </FormDescription>
                  <FormMessage />
                </FormItem>
              )}
            />
          </div>
        </SettingsForm>
      </Form>
    </SettingsSection>
  )
}
