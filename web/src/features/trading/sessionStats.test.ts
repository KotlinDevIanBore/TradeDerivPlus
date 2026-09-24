import { describe, expect, it } from 'vitest'
import type { DigitContract, Trade } from '@/types'
import { digitSessionStats, sessionStats } from './sessionStats'

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

function digitSettled(
  status: DigitContract['status'],
  pnl: string,
  settledAt: string,
): DigitContract {
  return {
    id: crypto.randomUUID(),
    account_id: 'a',
    symbol_id: 's',
    contract_type: 'even_odd',
    selection: { parity: 'even' },
    stake: '10',
    payout_ratio: '0.952',
    potential_payout: '19.52',
    entry_price: '100',
    entry_digit: 0,
    entry_tick_epoch: 1,
    status,
    settlement_price: '101',
    settlement_digit: 2,
    settled_at: settledAt,
    pnl,
    opened_at: '2026-09-21T11:00:00Z',
    void_after: '2026-09-21T11:00:05Z',
    // Session stats count a contract the same whoever placed it, so these
    // fixtures are human contracts and the field is only here to satisfy the type.
    bot_run_id: null,
    symbol_code: 'R_75',
    symbol_name: 'Volatility 75 Index',
    pip_size: 4,
  }
}

describe('digitSessionStats', () => {
  it('sums session settled P/L and counts outcomes', () => {
    const stats = digitSessionStats(
      [
        digitSettled('won', '9.52', '2026-09-21T13:00:00Z'),
        digitSettled('lost', '-10', '2026-09-21T12:30:00Z'),
        digitSettled('void', '0', '2026-09-21T12:45:00Z'),
        digitSettled('won', '99', '2026-09-21T09:00:00Z'),
      ],
      2,
      SINCE,
    )
    expect(stats.contracts).toBe(3)
    expect(stats.wins).toBe(1)
    expect(stats.losses).toBe(1)
    expect(stats.voids).toBe(1)
    expect(stats.open).toBe(2)
    expect(stats.realised.toString()).toBe('-0.48')
  })
})
