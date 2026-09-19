import { create } from 'zustand'
import { persist } from 'zustand/middleware'
import type { AccessGrant, User } from '@/types'

/**
 * Session and account context. This is the only client state that outlives a
 * render; everything server-derived belongs to React Query.
 *
 * The access token is held in memory and deliberately NOT persisted. The
 * refresh token is not here at all: it lives in an httpOnly cookie the browser
 * sends on its own, so no long-lived credential is reachable from JavaScript
 * and an XSS bug cannot walk off with a session.
 *
 * Surviving a reload is handled by a silent refresh on boot, not by
 * persistence. See bootstrapSession in api/client.ts.
 */
interface SessionState {
  accessToken: string | null
  user: User | null
  activeAccountId: string | null

  setSession: (tokens: AccessGrant, user: User) => void
  setTokens: (tokens: AccessGrant) => void
  setUser: (user: User) => void
  setActiveAccount: (accountId: string | null) => void
  clear: () => void
}

export const useSessionStore = create<SessionState>()(
  persist(
    (set) => ({
      accessToken: null,
      user: null,
      activeAccountId: null,

      setSession: (tokens, user) => set({ accessToken: tokens.access_token, user }),

      setTokens: (tokens) => set({ accessToken: tokens.access_token }),

      setUser: (user) => set({ user }),

      setActiveAccount: (activeAccountId) => set({ activeAccountId }),

      clear: () => set({ accessToken: null, user: null, activeAccountId: null }),
    }),
    {
      name: 'tdp.session',
      // Only non-sensitive preferences are persisted. The access token is
      // absent on purpose: it is re-obtained by a silent refresh on boot.
      partialize: (s) => ({
        user: s.user,
        activeAccountId: s.activeAccountId,
      }),
    },
  ),
)

export const useIsAuthenticated = () => useSessionStore((s) => s.accessToken !== null)
