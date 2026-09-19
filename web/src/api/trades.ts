import { api, qs } from './client'
import type { Direction, Page, Trade, TradeStatus, TradeSummary } from '@/types'

export interface OpenTradeInput {
  account_id: string
  symbol: string
  direction: Direction
  stake: string
  stop_loss?: string
  take_profit?: string
}

export const tradesApi = {
  list: (accountId: string, status?: TradeStatus, opts: { limit?: number; page?: number } = {}) =>
    api.get<Page<Trade>>(
      `/trades${qs({
        account_id: accountId,
        status,
        limit: opts.limit ?? 25,
        page: opts.page ?? 1,
      })}`,
    ),

  summary: (accountId: string) =>
    api.get<TradeSummary>(`/trades/summary${qs({ account_id: accountId })}`),

  open: (input: OpenTradeInput) => api.post<Trade>('/trades', input),

  close: (tradeId: string) => api.post<Trade>(`/trades/${tradeId}/close`),
}
