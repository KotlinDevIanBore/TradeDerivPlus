import { api, qs } from './client'
import type {
  DigitContract,
  DigitContractStatus,
  DigitContractType,
  DigitQuote,
  DigitSelection,
  Page,
} from '@/types'

export interface OpenDigitInput {
  account_id: string
  symbol: string
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
}

export interface DigitQuoteParams {
  contract_type: DigitContractType
  selection: DigitSelection
  stake: string
}

function selectionQuery(selection: DigitSelection): Record<string, string | number | undefined> {
  return {
    mode: selection.mode,
    digit: selection.digit,
    parity: selection.parity,
    side: selection.side,
    barrier: selection.barrier,
  }
}

export const digitApi = {
  open: (input: OpenDigitInput) => api.post<DigitContract>('/digit-contracts', input),

  quote: ({ contract_type, selection, stake }: DigitQuoteParams) =>
    api.get<DigitQuote>(
      `/digit-contracts/quote${qs({
        contract_type,
        stake,
        ...selectionQuery(selection),
      })}`,
    ),

  list: (
    accountId: string,
    status?: DigitContractStatus | 'settled' | '',
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
