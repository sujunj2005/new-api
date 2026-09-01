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
import { Loader2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { Dialog } from '@/components/dialog'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'

// ============================================================================
// Aff Rebind Dialog Component
// 提交走 POST /api/user/aff_rebind（契约 §4.2 B1），经 use-profile.ts affRebind
// → api.ts postAffRebind；错误映射（400 码无效/非分销商/已归属，403 超窗口）
// 在 hook 内完成（单一 toast 责任方）。
// ============================================================================

interface AffRebindDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  onSuccess: () => void
  onRebind: (data: { aff_code: string }) => Promise<boolean>
}

export function AffRebindDialog({
  open,
  onOpenChange,
  onSuccess,
  onRebind,
}: AffRebindDialogProps) {
  const { t } = useTranslation()
  const [affCode, setAffCode] = useState('')
  const [submitting, setSubmitting] = useState(false)

  const handleRebind = async () => {
    if (!affCode.trim()) {
      toast.error(t('Please enter the invitation code'))
      return
    }

    try {
      setSubmitting(true)
      const ok = await onRebind({ aff_code: affCode.trim() })

      if (ok) {
        onSuccess() // 触发 profile 刷新，attribution.rebind_available 变 false，入口消失
        onOpenChange(false)
        setAffCode('')
      }
    } finally {
      setSubmitting(false)
    }
  }

  const handleOpenChange = (nextOpen: boolean) => {
    if (!submitting) {
      onOpenChange(nextOpen)
      if (!nextOpen) {
        // Reset form when closing
        setAffCode('')
      }
    }
  }

  return (
    <Dialog
      open={open}
      onOpenChange={handleOpenChange}
      title={t('Bind Distributor')}
      description={t(
        'Bind your account to a distributor using an invitation code. This can only be done once.'
      )}
      contentClassName='sm:max-w-md'
      contentHeight='auto'
      bodyClassName='space-y-4'
      footer={
        <>
          <Button
            type='button'
            variant='outline'
            onClick={() => handleOpenChange(false)}
            disabled={submitting}
          >
            {t('Cancel')}
          </Button>
          <Button
            type='button'
            onClick={handleRebind}
            disabled={submitting || !affCode.trim()}
          >
            {submitting && <Loader2 className='mr-2 h-4 w-4 animate-spin' />}
            {submitting ? t('Binding...') : t('Bind')}
          </Button>
        </>
      }
    >
      <div className='space-y-4 py-4'>
        <div className='space-y-2'>
          <Label htmlFor='aff-code'>{t('Invitation code')}</Label>
          <Input
            id='aff-code'
            value={affCode}
            onChange={(e) => setAffCode(e.target.value)}
            placeholder={t('Enter distributor invitation code')}
            disabled={submitting}
            maxLength={32}
          />
        </div>
      </div>
    </Dialog>
  )
}
