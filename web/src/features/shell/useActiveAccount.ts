import { useEffect, useMemo } from 'react'
import { useAccounts } from '@/hooks/useQueries'
import { useSessionStore } from '@/store/session'
import type { Account } from '@/types'

/**
 * Resolves the active account, falling back to the demo account when nothing is
 * chosen or the stored choice no longer exists. Every account-scoped screen
 * reads the account from here rather than from the store directly.
 */
export function useActiveAccount() {
  const accountsQuery = useAccounts()
  const activeAccountId = useSessionStore((s) => s.activeAccountId)
  const setActiveAccount = useSessionStore((s) => s.setActiveAccount)

  const accounts = useMemo(() => accountsQuery.data?.items ?? [], [accountsQuery.data])

  const account: Account | null = useMemo(() => {
    if (accounts.length === 0) return null
    return accounts.find((a) => a.id === activeAccountId) ?? accounts[0]
  }, [accounts, activeAccountId])

  // Repair a stale or missing selection.
  useEffect(() => {
    if (account && account.id !== activeAccountId) setActiveAccount(account.id)
  }, [account, activeAccountId, setActiveAccount])

  return {
    account,
    accountId: account?.id ?? null,
    accounts,
    isDemo: account?.account_type === 'demo',
    isReal: account?.account_type === 'real',
    isLoading: accountsQuery.isLoading,
    error: accountsQuery.error,
    refetch: accountsQuery.refetch,
  }
}
