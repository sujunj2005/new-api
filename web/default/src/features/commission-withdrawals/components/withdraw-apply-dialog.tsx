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
 * 提现申请/操作弹窗组（Phase 5 首个 features 层写操作表单，容器一律 Dialog 封装）。
 * - 申请确认：按单全额（D-08），正文展示 period + settle_amount_cents，无金额输入面
 * - 驳回弹窗：reason 必填（前端空值拦截 + 服务端 400 信封 message 回显双保险）
 * - 批准确认：无表单确认（展示单号与金额，Q1=B reviewing→approved）
 * - 打款登记弹窗：voucher_no 必填（展示 settle_amount 全额提示，Q1=B approved→paid）
 * 错误回显：信封 success=false message 展示（全局拦截器同步 toast 为既有现状语义）。
 */
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'

import {
  applyWithdraw,
  approveWithdrawal,
  formatCents,
  markWithdrawalPaid,
  rejectWithdrawal,
  type Withdrawal,
} from '../api'
import type { CommissionStatement } from '@/features/commission-statements/api'

interface WithdrawApplyDialogProps {
  /** 目标账单（null=关闭）；正文展示 period + 全额，无金额输入（D-08） */
  statement: CommissionStatement | null
  onOpenChange: (open: boolean) => void
  /** 申请成功后回调（调用方刷新账单表 + 提现记录表） */
  onSuccess?: () => void
}

/** 申请全额确认对话框（A15）：确认即 POST /api/commission/withdrawals */
export function WithdrawApplyDialog({
  statement,
  onOpenChange,
  onSuccess,
}: WithdrawApplyDialogProps) {
  const { t } = useTranslation()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleConfirm = async () => {
    if (!statement) return
    setSubmitting(true)
    setError('')
    try {
      const res = await applyWithdraw(statement.id)
      if (res.success) {
        toast.success(t('Withdrawal application submitted'))
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
      onOpenChange={onOpenChange}
      title={t('Apply Withdrawal')}
      description={t(
        'This application withdraws the full statement amount.'
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
      {statement && (
        <div className='flex flex-col gap-2 text-sm'>
          <div>
            <span className='text-muted-foreground'>{t('Period')}: </span>
            {statement.period}
          </div>
          <div>
            <span className='text-muted-foreground'>
              {t('Settle Amount')}:{' '}
            </span>
            {formatCents(statement.settle_amount_cents)}
          </div>
          {error && <div className='text-destructive text-sm'>{error}</div>}
        </div>
      )}
    </Dialog>
  )
}

interface WithdrawRejectDialogProps {
  /** 目标提现单（null=关闭）；仅 reviewing 可达（Q2=B 门控由按钮状态保证） */
  withdrawal: Withdrawal | null
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/** 驳回弹窗（A19）：reason 必填，服务端信封 message 回显双保险 */
export function WithdrawRejectDialog({
  withdrawal,
  onOpenChange,
  onSuccess,
}: WithdrawRejectDialogProps) {
  const { t } = useTranslation()
  const [reason, setReason] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleConfirm = async () => {
    if (!withdrawal) return
    const trimmed = reason.trim()
    if (!trimmed) {
      setError(t('Reject reason cannot be empty'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const res = await rejectWithdrawal(withdrawal.id, trimmed)
      if (res.success) {
        toast.success(t('Withdrawal rejected'))
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
      open={!!withdrawal}
      onOpenChange={(open) => {
        if (!open) setReason('')
        onOpenChange(open)
      }}
      title={t('Reject Withdrawal')}
      description={withdrawal ? `${t('Withdrawal No')}: ${withdrawal.withdrawal_no}` : undefined}
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
      <div className='flex flex-col gap-2'>
        <Textarea
          placeholder={t('Enter reject reason')}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          rows={4}
        />
        {error && <div className='text-destructive text-sm'>{error}</div>}
      </div>
    </Dialog>
  )
}

interface WithdrawApproveDialogProps {
  /** 目标提现单（null=关闭）；仅 reviewing 可达（Q1=B） */
  withdrawal: Withdrawal | null
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/** 批准确认弹窗（A20）：无表单，展示单号与金额；账单保持 withdrawing */
export function WithdrawApproveDialog({
  withdrawal,
  onOpenChange,
  onSuccess,
}: WithdrawApproveDialogProps) {
  const { t } = useTranslation()
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleConfirm = async () => {
    if (!withdrawal) return
    setSubmitting(true)
    setError('')
    try {
      const res = await approveWithdrawal(withdrawal.id)
      if (res.success) {
        toast.success(t('Withdrawal approved'))
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
      open={!!withdrawal}
      onOpenChange={onOpenChange}
      title={t('Approve Withdrawal')}
      description={t('Confirm approval of this withdrawal?')}
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
      {withdrawal && (
        <div className='flex flex-col gap-2 text-sm'>
          <div>
            <span className='text-muted-foreground'>{t('Withdrawal No')}: </span>
            <span className='font-mono'>{withdrawal.withdrawal_no}</span>
          </div>
          <div>
            <span className='text-muted-foreground'>
              {t('Settle Amount')}:{' '}
            </span>
            {formatCents(withdrawal.settle_amount_cents)}
          </div>
          {error && <div className='text-destructive text-sm'>{error}</div>}
        </div>
      )}
    </Dialog>
  )
}

interface WithdrawMarkPaidDialogProps {
  /** 目标提现单（null=关闭）；仅 approved 可达（Q1=B） */
  withdrawal: Withdrawal | null
  onOpenChange: (open: boolean) => void
  onSuccess?: () => void
}

/** 打款登记弹窗（A21）：voucher_no 必填，展示 settle_amount 全额提示 */
export function WithdrawMarkPaidDialog({
  withdrawal,
  onOpenChange,
  onSuccess,
}: WithdrawMarkPaidDialogProps) {
  const { t } = useTranslation()
  const [voucherNo, setVoucherNo] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const handleConfirm = async () => {
    if (!withdrawal) return
    const trimmed = voucherNo.trim()
    if (!trimmed) {
      setError(t('Voucher no cannot be empty'))
      return
    }
    setSubmitting(true)
    setError('')
    try {
      const res = await markWithdrawalPaid(withdrawal.id, trimmed)
      if (res.success) {
        toast.success(t('Payment registered successfully'))
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
      open={!!withdrawal}
      onOpenChange={(open) => {
        if (!open) setVoucherNo('')
        onOpenChange(open)
      }}
      title={t('Mark Paid')}
      description={
        withdrawal
          ? `${t('Payment Amount')}: ${formatCents(withdrawal.settle_amount_cents)}`
          : undefined
      }
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
      <div className='flex flex-col gap-2'>
        <Input
          placeholder={t('Enter voucher no')}
          value={voucherNo}
          onChange={(e) => setVoucherNo(e.target.value)}
        />
        {error && <div className='text-destructive text-sm'>{error}</div>}
      </div>
    </Dialog>
  )
}
