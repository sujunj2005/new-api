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
import type { PermissionCatalog } from '@/lib/admin-permissions'
import { api } from '@/lib/api'

import type { DistributorCustomerRow } from '@/features/distributor-console/api'

import type {
  User,
  GetUsersParams,
  GetUsersResponse,
  SearchUsersParams,
  UserFormData,
  ManageUserAction,
  ManageUserQuotaPayload,
  ApiResponse,
} from './types'

// ============================================================================
// User Management APIs
// ============================================================================

/**
 * Get paginated users list
 */
export async function getUsers(
  params: GetUsersParams = {}
): Promise<GetUsersResponse> {
  const { p = 1, page_size = 10 } = params
  const res = await api.get(`/api/user/?p=${p}&page_size=${page_size}`)
  return res.data
}

/**
 * Search users by keyword or group
 */
export async function searchUsers(
  params: SearchUsersParams
): Promise<GetUsersResponse> {
  const {
    keyword = '',
    group = '',
    role = '',
    status = '',
    p = 1,
    page_size = 10,
  } = params
  const queryParams = new URLSearchParams()
  queryParams.set('keyword', keyword)
  queryParams.set('group', group)
  if (role) queryParams.set('role', role)
  if (status) queryParams.set('status', status)
  queryParams.set('p', String(p))
  queryParams.set('page_size', String(page_size))
  const res = await api.get(`/api/user/search?${queryParams.toString()}`)
  return res.data
}

/**
 * Get single user by ID
 */
export async function getUser(id: number): Promise<ApiResponse<User>> {
  const res = await api.get(`/api/user/${id}`)
  return res.data
}

/**
 * Create a new user
 */
export async function createUser(
  data: UserFormData
): Promise<ApiResponse<User>> {
  const res = await api.post('/api/user/', data)
  return res.data
}

/**
 * Update an existing user
 */
export async function updateUser(
  data: UserFormData & { id: number }
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.put('/api/user/', data)
  return res.data
}

/**
 * Delete a single user (hard delete)
 */
export async function deleteUser(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/`)
  return res.data
}

/**
 * Manage user (promote, demote, enable, disable, delete)
 */
export async function manageUser(
  id: number,
  action: ManageUserAction
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', { id, action })
  return res.data
}

/**
 * Adjust user quota atomically (add/subtract/override)
 */
export async function adjustUserQuota(
  payload: ManageUserQuotaPayload
): Promise<ApiResponse<Partial<User>>> {
  const res = await api.post('/api/user/manage', payload)
  return res.data
}

/**
 * Reset user's Passkey registration
 */
export async function resetUserPasskey(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/reset_passkey`)
  return res.data
}

/**
 * Reset user's Two-Factor Authentication setup
 */
export async function resetUserTwoFA(id: number): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${id}/2fa`)
  return res.data
}

/**
 * Get all available groups
 */
export async function getGroups(): Promise<ApiResponse<string[]>> {
  const res = await api.get('/api/group/')
  return res.data
}

/**
 * Get the permission catalog (resources, actions, and role baselines).
 * Source of truth lives in the backend authz package.
 */
export async function getPermissionCatalog(): Promise<PermissionCatalog> {
  const res = await api.get('/api/authz/catalog')
  return {
    resources: res.data?.data?.resources ?? [],
    roles: res.data?.data?.roles ?? [],
  }
}

// ============================================================================
// Admin Binding Management APIs
// ============================================================================

export interface OAuthBinding {
  provider_id: string
  provider_name: string
  user_id?: number
  external_id?: string
}

/**
 * Get user's custom OAuth bindings (admin)
 */
export async function getUserOAuthBindings(
  userId: number
): Promise<ApiResponse<OAuthBinding[]>> {
  const res = await api.get(`/api/user/${userId}/oauth/bindings`)
  return res.data
}

/**
 * Clear a user's built-in binding (admin)
 */
export async function adminClearUserBinding(
  userId: number,
  bindingType: string
): Promise<ApiResponse> {
  const res = await api.delete(`/api/user/${userId}/bindings/${bindingType}`)
  return res.data
}

/**
 * Unbind custom OAuth for a user (admin)
 */
export async function adminUnbindCustomOAuth(
  userId: number,
  providerId: string
): Promise<ApiResponse> {
  const res = await api.delete(
    `/api/user/${userId}/oauth/bindings/${providerId}`
  )
  return res.data
}

// ============================================================================
// Attribution ops APIs (Phase 6 plan 6.1 gap closure G-1/G-2/G-3)
// ============================================================================

/** B7 分页信封（PageInfo 形状） */
export interface AdminDistributorCustomersPage {
  page: number
  page_size: number
  total: number
  items: DistributorCustomerRow[]
}

/**
 * B7 管理员查看分销商客户列表（AdminAuth）。
 * 端点：GET /api/attribution/distributor/:id/customers?p=&page_size=（契约附录 G，plan 6.1 G-1）。
 * 行形状与 B4 distributorCustomerItem 逐字一致（type 复用单一来源）；
 * total_topup_cents/total_commission_cents 为 int64 分，展示层 formatCents 换算。
 */
export async function fetchAdminDistributorCustomers(
  distributorId: number,
  page?: number
): Promise<ApiResponse<AdminDistributorCustomersPage>> {
  const q = new URLSearchParams()
  if (page) q.set('p', String(page))
  const res = await api.get(
    `/api/attribution/distributor/${distributorId}/customers?${q.toString()}`
  )
  return res.data
}

/**
 * B2 管理员手动改绑（RootAuth；reason 必填，服务端同事务写审计）。
 * 服务端校验是唯一业务防线，失败信封 message 原样回显。
 */
export async function adminBindAttribution(
  userId: number,
  distributorId: number,
  reason: string
): Promise<ApiResponse> {
  const res = await api.post('/api/attribution/bind', {
    user_id: userId,
    distributor_id: distributorId,
    reason,
  })
  return res.data
}

/** B3 归属审计行（model/attribution_change.go json tag 逐字） */
export interface AttributionChangeRow {
  id: number
  user_id: number
  old_inviter_id: number
  new_inviter_id: number
  source: string
  operator_id: number
  reason: string
  created_at: number
}

/**
 * B3 归属审计查询（AdminAuth；支持 user_id 过滤 + 分页）。
 */
export async function fetchAttributionChanges(params: {
  userId?: number
  page?: number
}): Promise<
  ApiResponse<{ total: number; items: AttributionChangeRow[] }>
> {
  const q = new URLSearchParams()
  if (params.userId) q.set('user_id', String(params.userId))
  if (params.page) q.set('p', String(params.page))
  const res = await api.get(`/api/attribution/changes?${q.toString()}`)
  return res.data
}
