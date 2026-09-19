import { api, qs } from './client'
import type { List, Signal, TradeSymbol } from '@/types'

export const marketApi = {
  symbols: () => api.get<List<TradeSymbol>>('/symbols'),

  /** Buffered tick history, so a chart draws before the next live tick. */
  ticks: (code: string, minutes = 5) =>
    api.get<{ symbol: string; items: { price: string; epoch: number }[] }>(
      `/symbols/${encodeURIComponent(code)}/ticks${qs({ minutes })}`,
    ),

  /** Omit `symbol` for the newest signals across all instruments. */
  signals: (symbol?: string, limit = 20) =>
    api.get<List<Signal>>(`/signals${qs({ symbol, limit })}`),
}
