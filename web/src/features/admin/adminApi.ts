import { API_BASE, ApiError, type ApiErrorBody } from '@/api/client'
import { qs } from '@/api/client'
import type { Account, BotRun, Page } from '@/types'
import { useAdminSession } from './adminSession'
import type {
  AdminBotRun,
  AdminLoginResult,
  AdminTransaction,
  AdminUserDetail,
  AdminUserRow,
  AuditEntry,
  Dashboard,
  KYCSubmission,
} from './adminTypes'

/**
 * The admin client is separate from the user one because the sessions are.
 * It reads the admin token from the admin store and refreshes against
 * /admin/refresh, whose cookie the browser only sends on admin routes.
 */

let refreshInFlight: Promise<string | null> | null = null

async function refreshAdminToken(): Promise<string | null> {
  try {
    const res = await fetch(`${API_BASE}/admin/refresh`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      useAdminSession.getState().clear()
      return null
    }
    const body = (await res.json()) as AdminLoginResult
    useAdminSession.getState().setSession(body.access_token, body.admin, body.password_change_required)
    return body.access_token
  } catch {
    return null
  }
}

/** Trades the admin refresh cookie for a token at boot, as the user app does. */
export async function bootstrapAdminSession(): Promise<boolean> {
  if (useAdminSession.getState().accessToken) return true
  return (await refreshAdminToken()) !== null
}

async function request<T>(path: string, method = 'GET', body?: unknown, anonymous = false): Promise<T> {
  const send = async (token: string | null) => {
    const headers: Record<string, string> = {}
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (token && !anonymous) headers.Authorization = `Bearer ${token}`

    return fetch(`${API_BASE}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'include',
    })
  }

  let res: Response
  try {
    res = await send(useAdminSession.getState().accessToken)
  } catch {
    throw new ApiError(0, { message: "Can't reach the server. Check your connection.", code: 'network' })
  }

  if (res.status === 401 && !anonymous) {
    refreshInFlight ??= refreshAdminToken().finally(() => {
      refreshInFlight = null
    })
    const fresh = await refreshInFlight
    if (!fresh) {
      useAdminSession.getState().clear()
      throw new ApiError(401, { message: 'Your session has expired', code: 'unauthorized' })
    }
    res = await send(fresh)
  }

  if (res.status === 204) return undefined as T

  let payload: unknown
  try {
    payload = await res.json()
  } catch {
    payload = null
  }

  if (!res.ok) {
    const err = (payload as { error?: ApiErrorBody } | null)?.error
    throw new ApiError(res.status, err ?? { message: 'Something went wrong. Try again.', code: 'unknown' })
  }
  return payload as T
}

export interface AdminListParams {
  search?: string
  page?: number
  limit?: number
}

export const adminApi = {
  // --- Auth ---
  login: (username: string, password: string) =>
    request<AdminLoginResult>('/admin/login', 'POST', { username, password }, true),

  logout: () => request<void>('/admin/logout', 'POST', undefined, true),

  changePassword: (current_password: string, new_password: string) =>
    request<{ message: string }>('/admin/change-password', 'POST', { current_password, new_password }),

  // --- Reads ---
  dashboard: () => request<Dashboard>('/admin/dashboard'),

  users: (p: AdminListParams = {}) =>
    request<Page<AdminUserRow>>(
      `/admin/users${qs({ search: p.search, page: p.page ?? 1, limit: p.limit ?? 25 })}`,
    ),

  userDetail: (id: string) => request<AdminUserDetail>(`/admin/users/${id}`),

  transactions: (p: AdminListParams & { type?: string; status?: string } = {}) =>
    request<Page<AdminTransaction>>(
      `/admin/transactions${qs({
        search: p.search, type: p.type, status: p.status,
        page: p.page ?? 1, limit: p.limit ?? 25,
      })}`,
    ),

  kyc: (status = 'pending', page = 1) =>
    request<Page<KYCSubmission>>(`/admin/kyc${qs({ status, page, limit: 25 })}`),

  /** status: 'live', 'stopped', or '' for both. Paged — history has no ceiling. */
  botRuns: (p: { status?: string; page?: number; limit?: number } = {}) =>
    request<Page<AdminBotRun>>(
      `/admin/bot/runs${qs({ status: p.status, page: p.page ?? 1, limit: p.limit ?? 25 })}`,
    ),

  auditLog: (p: { page?: number; action?: string; admin_id?: string } = {}) =>
    request<Page<AuditEntry>>(
      `/admin/audit-log${qs({ page: p.page ?? 1, limit: 50, action: p.action, admin_id: p.admin_id })}`,
    ),

  // --- Actions, all requiring a reason ---
  freeze: (accountId: string, reason: string) =>
    request<Account>(`/admin/accounts/${accountId}/freeze`, 'POST', { reason }),

  unfreeze: (accountId: string, reason: string) =>
    request<Account>(`/admin/accounts/${accountId}/unfreeze`, 'POST', { reason }),

  adjust: (accountId: string, amount: string, balance_type: 'real' | 'bonus', reason: string) =>
    request<{ transaction: AdminTransaction; wallet: unknown }>(
      `/admin/accounts/${accountId}/adjust`, 'POST', { amount, balance_type, reason },
    ),

  approveKYC: (id: string) => request<KYCSubmission>(`/admin/kyc/${id}/approve`, 'POST', {}),

  rejectKYC: (id: string, reason: string) =>
    request<KYCSubmission>(`/admin/kyc/${id}/reject`, 'POST', { reason }),

  /** Stops one run. Automated trading stays on and every other run keeps going. */
  stopBotRun: (id: string, reason: string) =>
    request<BotRun>(`/admin/bot/runs/${id}/stop`, 'POST', { reason }),

  /**
   * Stops every live run and turns automated trading off. Both, together:
   * stopping the runs alone would leave users free to start new ones.
   */
  killBots: (reason: string) =>
    request<{ runs_stopped: number; bot_enabled: boolean }>('/admin/bot/kill', 'POST', { reason }),

  /** The way back. A reason is required, as it is for unfreezing an account. */
  enableBots: (reason: string) =>
    request<{ bot_enabled: boolean }>('/admin/bot/enable', 'POST', { reason }),
}

/** Admin query keys are prefixed so they never collide with the user app's. */
export const adminKeys = {
  dashboard: () => ['admin', 'dashboard'] as const,
  users: (p: unknown) => ['admin', 'users', p] as const,
  user: (id: string) => ['admin', 'user', id] as const,
  transactions: (p: unknown) => ['admin', 'transactions', p] as const,
  kyc: (status: string, page: number) => ['admin', 'kyc', status, page] as const,
  audit: (p: unknown) => ['admin', 'audit', p] as const,
  botRuns: (p: unknown) => ['admin', 'bot-runs', p] as const,
}
