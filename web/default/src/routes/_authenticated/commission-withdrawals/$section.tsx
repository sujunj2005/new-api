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
import { createFileRoute, redirect } from '@tanstack/react-router'
import { useCallback, useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { toast } from 'sonner'

import { SectionPageLayout } from '@/components/layout'
import {
  fetchStatementDetail,
  type CommissionStatement,
} from '@/features/commission-statements/api'
import { StatementDetail } from '@/features/commission-statements/components/statement-detail'
import { acceptWithdrawal } from '@/features/commission-withdrawals/api'
import {
  WithdrawApproveDialog,
  WithdrawMarkPaidDialog,
  WithdrawRejectDialog,
} from '@/features/commission-withdrawals/components/withdraw-apply-dialog'
import { WithdrawalTable } from '@/features/commission-withdrawals/components/withdrawal-table'
import type { Withdrawal } from '@/features/commission-withdrawals/api'
import {
  COMMISSION_WITHDRAWALS_DEFAULT_SECTION,
  isCommissionWithdrawalsSectionId,
} from '@/features/commission-withdrawals/section-registry'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

/**
 * 管理员提现审核工作台（WDRAW-03/SC3）。
 * 组装纪律（D4 禁复制派生）：列表/弹窗/快照明细全部来自 feature 域共享组件，
 * 本文件只做状态接线（受理直调 + 驳回/批准/打款弹窗 + StatementDetail 快照明细），
 * 各写操作成功后 refreshKey++ 同步重取列表。
 */
function CommissionWithdrawals() {
  const { t } = useTranslation()
  const [refreshKey, setRefreshKey] = useState(0)
  const [rejectTarget, setRejectTarget] = useState<Withdrawal | null>(null)
  const [approveTarget, setApproveTarget] = useState<Withdrawal | null>(null)
  const [paidTarget, setPaidTarget] = useState<Withdrawal | null>(null)
  const [stmtDetailId, setStmtDetailId] = useState<number | null>(null)
  const [stmtDetail, setStmtDetail] = useState<CommissionStatement | null>(null)

  const bumpRefresh = useCallback(() => setRefreshKey((k) => k + 1), [])

  // WDRAW-03 快照明细：onOpenStatement → A5 拉取账单本体 → StatementDetail(scope=admin)
  // 消费既有 A5/A6 端点零后端改动（明细/adjustments 由组件内既定路径自取）
  useEffect(() => {
    if (stmtDetailId == null) return
    let cancelled = false
    setStmtDetail(null)
    void fetchStatementDetail(stmtDetailId).then((res) => {
      if (!cancelled && res.success && res.data) {
        setStmtDetail(res.data.statement)
      }
    })
    return () => {
      cancelled = true
    }
  }, [stmtDetailId])

  const handleAccept = async (w: Withdrawal) => {
    const res = await acceptWithdrawal(w.id)
    if (res.success) {
      toast.success(t('Withdrawal accepted'))
      bumpRefresh()
    }
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('Withdrawal Review')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Content>
        <div className='flex flex-col gap-4'>
          <WithdrawalTable
            scope='admin'
            refreshKey={refreshKey}
            onAccept={handleAccept}
            onReject={setRejectTarget}
            onApprove={setApproveTarget}
            onMarkPaid={setPaidTarget}
            onOpenStatement={(w) => setStmtDetailId(w.statement_id)}
          />
          {stmtDetail && (
            <StatementDetail
              scope='admin'
              statement={stmtDetail}
              onClose={() => {
                setStmtDetailId(null)
                setStmtDetail(null)
              }}
            />
          )}
          {/* 弹窗置于 Content 内：SectionPageLayout 仅渲染命名插槽，外部子树不挂载 */}
          <WithdrawRejectDialog
            withdrawal={rejectTarget}
            onOpenChange={(open) => {
              if (!open) setRejectTarget(null)
            }}
            onSuccess={bumpRefresh}
          />
          <WithdrawApproveDialog
            withdrawal={approveTarget}
            onOpenChange={(open) => {
              if (!open) setApproveTarget(null)
            }}
            onSuccess={bumpRefresh}
          />
          <WithdrawMarkPaidDialog
            withdrawal={paidTarget}
            onOpenChange={(open) => {
              if (!open) setPaidTarget(null)
            }}
            onSuccess={bumpRefresh}
          />
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}

/**
 * 管理员提现工作台路由。
 * beforeLoad 阈值式守卫（role < ROLE.ADMIN → /403，commission-statements 页惯例）：
 * 前端守卫仅体验层，服务端 AdminAuth（D-10）才是真防线。
 */
export const Route = createFileRoute(
  '/_authenticated/commission-withdrawals/$section'
)({
  beforeLoad: ({ params }) => {
    const { auth } = useAuthStore.getState()
    if (!auth.user || auth.user.role < ROLE.ADMIN) {
      throw redirect({
        to: '/403',
      })
    }
    if (!isCommissionWithdrawalsSectionId(params.section)) {
      throw redirect({
        to: '/commission-withdrawals/$section',
        params: { section: COMMISSION_WITHDRAWALS_DEFAULT_SECTION },
      })
    }
  },
  component: CommissionWithdrawals,
})
