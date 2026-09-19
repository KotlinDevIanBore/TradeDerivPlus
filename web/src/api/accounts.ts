import { api, qs } from './client'
import type { Account, AccountType, List, Wallet } from '@/types'

export const accountsApi = {
  list: () => api.get<List<Account>>('/accounts'),

  create: (account_type: AccountType) => api.post<Account>('/accounts', { account_type }),

  wallet: (accountId: string) => api.get<Wallet>(`/accounts/${accountId}/wallet`),
}

export { qs }
