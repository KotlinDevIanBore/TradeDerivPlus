import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { accountsApi } from '@/api/accounts'
import { authApi } from '@/api/auth'
import { digitApi, type OpenDigitInput } from '@/api/digit'
import { marketApi } from '@/api/market'
import { tradesApi, type OpenTradeInput } from '@/api/trades'
import { walletApi } from '@/api/wallet'
import { queryKeys } from '@/lib/queryKeys'
import { useSessionStore } from '@/store/session'
import type { AccountType, DigitContractStatus, DigitContractType, DigitSelection, TradeStatus, TxType } from '@/types'

/**
 * Server state lives here. Components receive data and callbacks as props from
 * feature containers that use these hooks, so components stay pure.
 */

export function useAccounts() {
  const enabled = useSessionStore((s) => s.accessToken !== null)
  return useQuery({
    queryKey: queryKeys.accounts(),
    queryFn: () => accountsApi.list(),
    enabled,
    staleTime: 60_000,
  })
}

export function useWallet(accountId: string | null) {
  return useQuery({
    queryKey: queryKeys.wallet(accountId ?? ''),
    queryFn: () => accountsApi.wallet(accountId!),
    enabled: Boolean(accountId),
    // The account socket pushes changes; this is the safety net.
    refetchOnWindowFocus: true,
    staleTime: 10_000,
  })
}

export function useCreateAccount() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (accountType: AccountType) => accountsApi.create(accountType),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: queryKeys.accounts() }),
  })
}

export function useMe() {
  const enabled = useSessionStore((s) => s.accessToken !== null)
  const setUser = useSessionStore((s) => s.setUser)
  return useQuery({
    queryKey: queryKeys.me(),
    queryFn: async () => {
      const user = await authApi.me()
      setUser(user)
      return user
    },
    enabled,
    // Short enough that a SQL flag flip shows up without a long wait; matches
    // the API's settings cache TTL so we do not hammer the DB.
    staleTime: 10_000,
    refetchInterval: 10_000,
  })
}

export function useSymbols() {
  const enabled = useSessionStore((s) => s.accessToken !== null)
  return useQuery({
    queryKey: queryKeys.symbols(),
    queryFn: () => marketApi.symbols(),
    enabled,
    staleTime: 300_000,
  })
}

export function useSignals(symbol?: string, limit = 20) {
  const enabled = useSessionStore((s) => s.accessToken !== null)
  return useQuery({
    queryKey: queryKeys.signals(symbol),
    queryFn: () => marketApi.signals(symbol, limit),
    enabled,
    staleTime: 30_000,
  })
}

export function useTrades(accountId: string | null, status?: TradeStatus, limit = 25) {
  return useQuery({
    queryKey: queryKeys.trades(accountId ?? '', status),
    queryFn: () => tradesApi.list(accountId!, status, { limit }),
    enabled: Boolean(accountId),
    // Open positions also update over the socket; poll slowly as a fallback.
    refetchInterval: status === 'open' ? 15_000 : false,
  })
}

export function useTradeSummary(accountId: string | null) {
  return useQuery({
    queryKey: queryKeys.tradeSummary(accountId ?? ''),
    queryFn: () => tradesApi.summary(accountId!),
    enabled: Boolean(accountId),
    refetchInterval: 15_000,
  })
}

export function useOpenTrade() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: OpenTradeInput) => tradesApi.open(input),
    onSuccess: (trade) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.trades(trade.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.wallet(trade.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.tradeSummary(trade.account_id) })
    },
  })
}

export function useCloseTrade() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (tradeId: string) => tradesApi.close(tradeId),
    onSuccess: (trade) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.trades(trade.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.wallet(trade.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.tradeSummary(trade.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.transactions(trade.account_id) })
    },
  })
}

export function useOpenDigits(accountId: string | null, status: DigitContractStatus | 'settled' = 'open') {
  return useQuery({
    queryKey: queryKeys.digitContracts(accountId ?? '', status),
    queryFn: () => digitApi.list(accountId!, status, { limit: 50 }),
    enabled: Boolean(accountId),
    refetchInterval: status === 'open' ? 5_000 : false,
  })
}

export function useDigitQuote(
  contractType: DigitContractType,
  selection: DigitSelection,
  stake: string,
  enabled = true,
) {
  const stakeOk = Boolean(stake) && Number(stake) > 0
  return useQuery({
    queryKey: queryKeys.digitQuote({ contractType, selection, stake }),
    queryFn: () =>
      digitApi.quote({
        contract_type: contractType,
        selection,
        stake,
      }),
    enabled: enabled && stakeOk,
    staleTime: 60_000,
    // Quotes are static-table reads; keep previous numbers while stake edits settle.
    placeholderData: (prev) => prev,
  })
}

export function useOpenDigit() {
  const queryClient = useQueryClient()
  return useMutation({
    mutationFn: (input: OpenDigitInput) => digitApi.open(input),
    onSuccess: (contract) => {
      queryClient.invalidateQueries({ queryKey: queryKeys.digitContracts(contract.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.wallet(contract.account_id) })
      queryClient.invalidateQueries({ queryKey: queryKeys.transactions(contract.account_id) })
    },
  })
}

export function useTransactions(accountId: string | null, types?: TxType[], limit = 25) {
  return useQuery({
    queryKey: queryKeys.transactions(accountId ?? '', { types, limit }),
    queryFn: () => walletApi.transactions(accountId!, { types, limit }),
    enabled: Boolean(accountId),
  })
}

export function useRate() {
  const enabled = useSessionStore((s) => s.accessToken !== null)
  return useQuery({
    queryKey: queryKeys.rate(),
    queryFn: () => walletApi.rate(),
    enabled,
    staleTime: 600_000,
  })
}
