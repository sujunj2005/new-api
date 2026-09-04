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
 * A8 超管账单调整单弹窗（RootAuth，v1.1 Round-2 Obs-1 补 UI 消费者）。
 * 仅 status=payable 账单可达（行内按钮门控 + 服务端白名单双保险）；
 * delta_cents 可负（正补负减，cents 直输无浮点）；服务端按账单当前累计
 * 重算 settle 金额（不信任客户端），失败信封 message 原样回显（rate-set-dialog 母本）。
 */
import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'

import { createStatementAdjustment, formatCents, type CommissionStatement } from '../api'

interface StatementAdjustDialogProps {
  /** 目标账单（null=关闭）；仅 payable 行可达 */
  statement: CommissionStatement | null
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/** A8 弹窗：展示 period + 当前结算额 + delta_cents（可负）+ reason 必填 */
export function StatementAdjustDialog({
  statement,
  onOpenChange,
  onSuccess,
}: StatementAdjustDialogProps) {
  const { t } = useTranslation()
  const [deltaCents, setDeltaCents] = useState('')
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const resetForm = () => {
    setDeltaCents('')
    setReason('')
    setError('')
  }

  // 切换目标账单或重新打开时清空（复用同一实例，不重置会把 A 账单输入错挂到 B）
  useEffect(() => {
    setDeltaCents('')
    setReason('')
    setError('')
  }, [statement?.id, statement === null])

  const handleConfirm = async () => {
    if (!statement) return
    const trimmedDelta = deltaCents.trim()
    const trimmedReason = reason.trim()
    if (!trimmedDelta) {
      setError(t('Delta cannot be empty'))
      return
    }
    const deltaNum = Number(trimmedDelta)
    if (!Number.isInteger(deltaNum) || deltaNum === 0) {
      setError(t('Delta must be a non-zero integer'))
      return
    }
    if (!trimmedReason) {
      setError(t('Reason cannot be empty'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      // A8 冻结形状两字段逐字：delta_cents 可负，服务端重算 settle
      const res = await createStatementAdjustment(
        statement.id,
        deltaNum,
        trimmedReason
      )
      if (res.success) {
        toast.success(t('Statement adjusted'))
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
      open={!!statement}
      onOpenChange={(open) => {
        if (!open) resetForm()
        onOpenChange(open)
      }}
      title={t('Adjust Statement')}
      description={statement ? `${t('Period')}: ${statement.period} · ${t('Statement ID')}: ${statement.id}` : undefined}
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
      {statement && (
        <div className='flex flex-col gap-3'>
          <div className='text-muted-foreground text-sm'>
            {t('Current Settle Amount')}:{' '}
            <span className='text-foreground font-medium'>
              {formatCents(statement.settle_amount_cents)}
            </span>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='adjust-delta'>{t('Delta (cents)')}</Label>
            <Input
              id='adjust-delta'
              placeholder={t('Enter delta in cents')}
              value={deltaCents}
              onChange={(e) => setDeltaCents(e.target.value)}
            />
            <span className='text-muted-foreground text-xs'>
              {t('Positive increases, negative decreases')}
            </span>
          </div>
          <div className='flex flex-col gap-1.5'>
            <Label htmlFor='adjust-reason'>{t('Reason')}</Label>
            <Textarea
              id='adjust-reason'
              placeholder={t('Enter reason')}
              value={reason}
              onChange={(e) => setReason(e.target.value)}
              rows={4}
            />
          </div>
          {error && <div className='text-destructive text-sm'>{error}</div>}
        </div>
      )}
    </Dialog>
  )
}
