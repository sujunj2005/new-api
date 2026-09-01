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
import i18next from 'i18next'
import { useState, useEffect, useCallback } from 'react'
import { toast } from 'sonner'

import { getUserProfile, postAffRebind, updateUserProfile, updateUserSettings } from '../api'
import type {
  UserProfile,
  UpdateUserRequest,
  UpdateUserSettingsRequest,
} from '../types'

// ============================================================================
// Profile Hook
// ============================================================================

export function useProfile() {
  const [profile, setProfile] = useState<UserProfile | null>(null)
  const [loading, setLoading] = useState(true)
  const [updating, setUpdating] = useState(false)

  // Fetch user profile (with optional silent mode)
  const fetchProfile = useCallback(async (silent = false) => {
    try {
      if (!silent) {
        setLoading(true)
      }
      const response = await getUserProfile()

      if (response.success && response.data) {
        setProfile(response.data)
      }
    } catch (error) {
      // eslint-disable-next-line no-console
      console.error('Failed to fetch profile:', error)
      if (!silent) {
        toast.error(i18next.t('Failed to load profile'))
      }
    } finally {
      if (!silent) {
        setLoading(false)
      }
    }
  }, [])

  // Refresh profile silently (without loading state)
  const refreshProfile = useCallback(async () => {
    await fetchProfile(true)
  }, [fetchProfile])

  // Update user profile
  const updateProfile = useCallback(
    async (data: UpdateUserRequest): Promise<boolean> => {
      try {
        setUpdating(true)
        const response = await updateUserProfile(data)

        if (response.success) {
          toast.success(i18next.t('Profile updated successfully'))
          await refreshProfile() // Refresh profile silently
          return true
        }

        toast.error(response.message || i18next.t('Failed to update profile'))
        return false
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to update profile:', error)
        toast.error(i18next.t('Failed to update profile'))
        return false
      } finally {
        setUpdating(false)
      }
    },
    [refreshProfile]
  )

  // Update user settings
  const updateSettings = useCallback(
    async (data: UpdateUserSettingsRequest): Promise<boolean> => {
      try {
        setUpdating(true)
        const response = await updateUserSettings(data)

        if (response.success) {
          toast.success(i18next.t('Settings updated successfully'))
          await refreshProfile() // Refresh profile silently
          return true
        }

        toast.error(response.message || i18next.t('Failed to update settings'))
        return false
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to update settings:', error)
        toast.error(i18next.t('Failed to update settings'))
        return false
      } finally {
        setUpdating(false)
      }
    },
    [refreshProfile]
  )

  // Self-rebind attribution to a distributor (契约 §4.2 B1：POST /api/user/aff_rebind)
  // 错误映射（单一 toast 责任方，postAffRebind 已 skip 全局拦截器提示）：
  //   403 → 超窗口；400 → 码无效/非分销商/已归属；其余 → 通用失败
  const affRebind = useCallback(
    async (data: { aff_code: string }): Promise<boolean> => {
      try {
        setUpdating(true)
        const response = await postAffRebind(data)

        if (response.success) {
          toast.success(i18next.t('Rebind successful'))
          await refreshProfile() // 刷新 profile，attribution.rebind_available 更新
          return true
        }

        toast.error(response.message || i18next.t('Rebind failed'))
        return false
      } catch (error) {
        // eslint-disable-next-line no-console
        console.error('Failed to rebind aff:', error)
        const status = (
          error as { response?: { status?: number } } | undefined
        )?.response?.status
        if (status === 403) {
          toast.error(i18next.t('Rebind window has expired. Please contact the administrator.'))
        } else if (status === 400) {
          toast.error(
            i18next.t('Invalid invitation code or the code is not from a distributor')
          )
        } else {
          toast.error(i18next.t('Rebind failed'))
        }
        return false
      } finally {
        setUpdating(false)
      }
    },
    [refreshProfile]
  )

  // Initial fetch
  useEffect(() => {
    fetchProfile()
  }, [fetchProfile])

  return {
    profile,
    loading,
    updating,
    fetchProfile,
    refreshProfile,
    updateProfile,
    updateSettings,
    affRebind,
  }
}
