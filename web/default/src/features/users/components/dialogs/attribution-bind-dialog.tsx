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
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'

import { adminBindAttribution } from '../../api'

interface AttributionBindDialogProps {
  open: boolean
  /** 目标客户（B2 user_id，对任意用户可用） */
  userId: number | null
  username: string
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/**
 * G-2 指定归属弹窗（B2 POST /api/attribution/bind，CUST-04 管理员手动改绑）。
 * 分销商 ID + reason 必填（前端仅空值拦截）；服务端校验链（F5 无条件覆盖 /
 * distributor role=5 精确匹配 / reason 审计）是唯一业务防线，
 * 失败信封 message 原样回显、零改写；成功后刷新列表。
 */
export function AttributionBindDialog({
  open,
  userId,
  username,
  onOpenChange,
  onSuccess,
}: AttributionBindDialogProps) {
  const { t } = useTranslation()
  const [distributorId, setDistributorId] = useState('')
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    if (!open) return
    setError('')
    setDistributorId('')
    setReason('')
  }, [open, userId])

  const handleConfirm = async () => {
    const trimmedId = distributorId.trim()
    const trimmedReason = reason.trim()
    if (!trimmedId) {
      setError(t('Distributor ID cannot be empty'))
      return
    }
    if (!trimmedReason) {
      setError(t('Reason cannot be empty'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const res = await adminBindAttribution(
        userId ?? 0,
        Number(trimmedId),
        trimmedReason
      )
      if (res.success) {
        toast.success(t('Attribution updated'))
        onOpenChange(false)
        onSuccess?.()
      } else {
        setError(res.message || t('Request failed'))
      }
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={onOpenChange}
      title={t('Assign Attribution')}
      description={`${t('Customer')} · ${username || '-'}`}
      footer={
        <>
          <Button
            variant='outline'
            onClick={() => onOpenChange(false)}
            disabled={submitting}
          >
            {t('Cancel')}
          </Button>
          <Button onClick={handleConfirm} disabled={submitting}>
            {t('Confirm')}
          </Button>
        </>
      }
    >
      <div className='flex flex-col gap-3'>
        <div className='flex flex-col gap-1.5'>
          <span className='text-sm font-medium'>{t('Distributor ID')}</span>
          <Input
            placeholder={t('Enter distributor ID')}
            value={distributorId}
            onChange={(e) => setDistributorId(e.target.value)}
          />
        </div>
        <div className='flex flex-col gap-1.5'>
          <span className='text-sm font-medium'>{t('Reason')}</span>
          <Input
            placeholder={t('Enter reason')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
          />
        </div>
        {error && <div className='text-destructive text-sm'>{error}</div>}
      </div>
    </Dialog>
  )
}
