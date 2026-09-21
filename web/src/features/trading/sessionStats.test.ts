import { describe, expect, it } from 'vitest'
import type { Trade } from '@/types'
import { sessionStats } from './sessionStats'

const SINCE = new Date('2026-09-21T12:00:00Z').getTime()

function closed(pnl: string, closedAt: string): Trade {
  return {
    id: crypto.randomUUID(),
    account_id: 'a',
    symbol_id: 's',
    direction: 'buy',
    entry_price: '100',
    exit_price: '101',
    stake: '10',
    stop_loss: null,
    take_profit: null,
    status: 'closed',
    pnl,
    opened_at: '2026-09-21T11:00:00Z',
    closed_at: closedAt,
    symbol_code: 'R_75',
    symbol_name: 'Volatility 75 Index',
    pip_size: 4,
  }
}

describe('sessionStats', () => {
  it('counts only trades closed since the session began', () => {
    const stats = sessionStats(
      [
        closed('4.00', '2026-09-21T13:00:00Z'),
        closed('-1.50', '2026-09-21T12:30:00Z'),
        closed('99.00', '2026-09-21T09:00:00Z'), // before the session
      ],
      SINCE,
    )

    expect(stats.trades).toBe(2)
    expect(stats.wins).toBe(1)
    expect(stats.losses).toBe(1)
    expect(stats.realised.toString()).toBe('2.5')
  })

  it('treats a break-even trade as neither a win nor a loss', () => {
    const stats = sessionStats([closed('0', '2026-09-21T13:00:00Z')], SINCE)

    expect(stats.trades).toBe(1)
    expect(stats.wins).toBe(0)
    expect(stats.losses).toBe(0)
  })

  it('is empty before anything has been closed', () => {
    const stats = sessionStats([], SINCE)

    expect(stats.trades).toBe(0)
    expect(stats.realised.toString()).toBe('0')
  })

  it('ignores an open trade that carries no close time', () => {
    const open = { ...closed('0', '2026-09-21T13:00:00Z'), closed_at: null, pnl: null }

    expect(sessionStats([open], SINCE).trades).toBe(0)
  })
})
