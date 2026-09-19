import { api, qs } from './client'
import type { DepositResponse, Page, Transaction, TxType } from '@/types'

export interface DepositInput {
  account_id: string
  amount_kes: string
  phone_number: string
}

export interface WithdrawInput {
  account_id: string
  amount_usd: string
  phone_number: string
}

export const walletApi = {
  deposit: (input: DepositInput) => api.post<DepositResponse>('/wallet/deposit', input),

  withdraw: (input: WithdrawInput) => api.post<Transaction>('/wallet/withdraw', input),

  transactions: (accountId: string, opts: { limit?: number; page?: number; types?: TxType[] } = {}) =>
    api.get<Page<Transaction>>(
      `/wallet/transactions${qs({
        account_id: accountId,
        limit: opts.limit ?? 25,
        page: opts.page ?? 1,
        type: opts.types,
      })}`,
    ),

  /** Polling fallback for the deposit screen when the account socket is down. */
  transaction: (id: string) => api.get<Transaction>(`/wallet/transactions/${id}`),

  rate: () => api.get<{ kes_per_usd: number }>('/wallet/rate'),
}
