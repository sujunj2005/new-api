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
 * A7 超管人工补录/冲销流水弹窗（RootAuth，v1.1 Round-2 Obs-1 补 UI 消费者）。
 * 前端仅做必填空值拦截与整数校验（体验层）；服务端校验链
 * （reason 必填 / flow_type 白名单 / 目标角色精确判定 / amount 恒正）是唯一业务防线，
 * 失败信封 message 原样回显、零改写（rate-set-dialog 母本）。
 * 金额输入为 cents 直输（域纪律：全链路 int64 cents，无浮点运算——api.ts 头注）。
 */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'

import { createManualFlow, type ManualFlowType } from '../api'

interface ManualFlowDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/** A7 弹窗：分销商 ID + 类型（补录/冲销）+ 金额（cents 恒正）+ reason 必填 */
export function ManualFlowDialog({
  open,
  onOpenChange,
  onSuccess,
}: ManualFlowDialogProps) {
  const { t } = useTranslation()
  const [distributorId, setDistributorId] = useState('')
  const [flowType, setFlowType] = useState<ManualFlowType>('manual_credit')
  const [amountCents, setAmountCents] = useState('')
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const resetForm = () => {
    setDistributorId('')
    setFlowType('manual_credit')
    setAmountCents('')
    setReason('')
    setError('')
  }

  // 打开时清空（每次提交独立一笔，无预填场景）
  useEffect(() => {
    if (!open) return
    setDistributorId('')
    setFlowType('manual_credit')
    setAmountCents('')
    setReason('')
    setError('')
  }, [open])

  const handleConfirm = async () => {
    const trimmedId = distributorId.trim()
    const trimmedAmount = amountCents.trim()
    const trimmedReason = reason.trim()
    if (!trimmedId) {
      setError(t('Distributor ID cannot be empty'))
      return
    }
    const idNum = Number(trimmedId)
    if (!Number.isInteger(idNum) || idNum <= 0) {
      setError(t('Distributor ID must be a positive integer'))
      return
    }
    if (!trimmedAmount) {
      setError(t('Amount cannot be empty'))
      return
    }
    const amountNum = Number(trimmedAmount)
    if (!Number.isInteger(amountNum) || amountNum <= 0) {
      setError(t('Amount must be a positive integer'))
      return
    }
    if (!trimmedReason) {
      setError(t('Reason cannot be empty'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      // A7 冻结形状四字段逐字：amount_cents 恒正（debit 落库取负由服务端处理）
      const res = await createManualFlow(idNum, flowType, amountNum, trimmedReason)
      if (res.success) {
        toast.success(t('Manual flow created'))
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
      onOpenChange={(o) => {
        if (!o) resetForm()
        onOpenChange(o)
      }}
      title={t('Manual Flow')}
      description={t(
        'Create a manual credit or debit commission flow for a distributor.'
      )}
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
          <Label htmlFor='manual-flow-distributor'>{t('Distributor ID')}</Label>
          <Input
            id='manual-flow-distributor'
            placeholder={t('Enter distributor ID')}
            value={distributorId}
            onChange={(e) => setDistributorId(e.target.value)}
          />
        </div>
        <div className='flex flex-col gap-1.5'>
          <Label>{t('Flow Type')}</Label>
          <Select
            items={[
              { value: 'manual_credit', label: t('Manual Credit') },
              { value: 'manual_debit', label: t('Manual Debit') },
            ]}
            value={flowType}
            onValueChange={(value) =>
              value !== null && setFlowType(value as ManualFlowType)
            }
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent alignItemWithTrigger={false}>
              <SelectGroup>
                <SelectItem value='manual_credit'>
                  {t('Manual Credit')}
                </SelectItem>
                <SelectItem value='manual_debit'>{t('Manual Debit')}</SelectItem>
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
        <div className='flex flex-col gap-1.5'>
          <Label htmlFor='manual-flow-amount'>{t('Amount (cents)')}</Label>
          <Input
            id='manual-flow-amount'
            placeholder={t('Enter amount in cents')}
            value={amountCents}
            onChange={(e) => setAmountCents(e.target.value)}
          />
        </div>
        <div className='flex flex-col gap-1.5'>
          <Label htmlFor='manual-flow-reason'>{t('Reason')}</Label>
          <Textarea
            id='manual-flow-reason'
            placeholder={t('Enter reason')}
            value={reason}
            onChange={(e) => setReason(e.target.value)}
            rows={4}
          />
        </div>
        {error && <div className='text-destructive text-sm'>{error}</div>}
      </div>
    </Dialog>
  )
}
