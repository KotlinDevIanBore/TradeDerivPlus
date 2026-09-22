import { api, qs } from './client'
import type { DigitContract, DigitContractStatus, DigitContractType, DigitSelection, Page } from '@/types'

export interface OpenDigitInput {
  account_id: string
  symbol: string
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
}

export const digitApi = {
  open: (input: OpenDigitInput) => api.post<DigitContract>('/digit-contracts', input),

  list: (
    accountId: string,
    status?: DigitContractStatus | '',
    opts: { limit?: number; page?: number } = {},
  ) =>
    api.get<Page<DigitContract>>(
      `/digit-contracts${qs({
        account_id: accountId,
        status: status || undefined,
        limit: opts.limit ?? 25,
        page: opts.page ?? 1,
      })}`,
    ),
}
