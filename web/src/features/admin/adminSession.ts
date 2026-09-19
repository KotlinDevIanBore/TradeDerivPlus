import { create } from 'zustand'
import type { AdminUser } from './adminTypes'

/**
 * The admin session is deliberately separate from the user session: a
 * different store, a different cookie path, a different token scope. Signing
 * into one does not sign into the other.
 *
 * As with the user session the access token is memory-only; the refresh token
 * is an httpOnly cookie scoped to /api/v1/admin.
 */
interface AdminSessionState {
  accessToken: string | null
  admin: AdminUser | null
  /**
   * True when the backend is BLOCKING this session until the password is
   * changed, which it does outside development. Distinct from
   * `admin.must_change_password`, which says the default password is still in
   * place: in development that is a banner, not a barrier, so the two must not
   * be conflated or the dev-mode nudge never appears.
   */
  passwordChangeRequired: boolean

  setSession: (accessToken: string, admin: AdminUser, required: boolean) => void
  setToken: (accessToken: string) => void
  clear: () => void
}

export const useAdminSession = create<AdminSessionState>((set) => ({
  accessToken: null,
  admin: null,
  passwordChangeRequired: false,

  setSession: (accessToken, admin, passwordChangeRequired) =>
    set({ accessToken, admin, passwordChangeRequired }),

  setToken: (accessToken) => set({ accessToken }),

  clear: () => set({ accessToken: null, admin: null, passwordChangeRequired: false }),
}))

export const useIsAdminAuthenticated = () => useAdminSession((s) => s.accessToken !== null)
