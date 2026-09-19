import { describe, expect, it, vi } from 'vitest'
import { kes, money, percent, price, relativeTime, signedMoney, transactionTitle } from './format'

describe('money', () => {
  it('always shows two decimals with thousands separators', () => {
    expect(money('1234.5')).toBe('$1,234.50')
    expect(money('0')).toBe('$0.00')
    expect(money('1000000')).toBe('$1,000,000.00')
  })

  it('puts the minus before the currency symbol', () => {
    expect(money('-12.3')).toBe('-$12.30')
  })

  it('treats a missing value as zero rather than rendering NaN', () => {
    expect(money(null)).toBe('$0.00')
    expect(money(undefined)).toBe('$0.00')
    expect(money('not a number')).toBe('$0.00')
  })
})

describe('signedMoney', () => {
  it('always carries a sign, so profit and loss never rely on colour alone', () => {
    expect(signedMoney('4.2')).toBe('+$4.20')
    expect(signedMoney('-4.2')).toBe('-$4.20')
  })

  it('shows zero without a sign', () => {
    expect(signedMoney('0')).toBe('$0.00')
  })
})

describe('price', () => {
  it('renders at the instrument precision, not a fixed one', () => {
    expect(price('1.0854', 5)).toBe('1.08540')
    expect(price('4685.559', 4)).toBe('4,685.5590')
    expect(price('9977.7528', 2)).toBe('9,977.75')
  })
})

describe('kes and percent', () => {
  it('formats shillings as whole numbers', () => {
    expect(kes('12950')).toBe('KES 12,950')
  })

  it('formats a percentage to one decimal', () => {
    expect(percent('78.44')).toBe('78.4%')
    expect(percent('78.44', 0)).toBe('78%')
  })
})

describe('relativeTime', () => {
  it('reads as elapsed duration until it stops being useful', () => {
    vi.useFakeTimers()
    const now = new Date('2026-09-17T12:00:00Z')
    vi.setSystemTime(now)

    expect(relativeTime(new Date(now.getTime() - 5_000).toISOString())).toBe('just now')
    expect(relativeTime(new Date(now.getTime() - 3 * 60_000).toISOString())).toBe('3m ago')
    expect(relativeTime(new Date(now.getTime() - 2 * 3_600_000).toISOString())).toBe('2h ago')
    expect(relativeTime(new Date(now.getTime() - 3 * 86_400_000).toISOString())).toBe('3d ago')

    vi.useRealTimers()
  })

  it('returns empty for a missing or unparseable timestamp', () => {
    expect(relativeTime(null)).toBe('')
    expect(relativeTime('nonsense')).toBe('')
  })
})

describe('transactionTitle', () => {
  it('names an admin adjustment plainly', () => {
    expect(transactionTitle('admin_adjustment')).toBe('Balance adjustment')
  })

  it('includes the instrument on a trade row', () => {
    expect(transactionTitle('trade_open', { symbol: 'R_75', direction: 'buy' })).toBe('Opened R_75 BUY')
    expect(transactionTitle('trade_close', { symbol: 'R_75' })).toBe('Closed R_75')
  })

  it('falls back when metadata is missing', () => {
    expect(transactionTitle('trade_open', null)).toBe('Position opened')
  })
})
