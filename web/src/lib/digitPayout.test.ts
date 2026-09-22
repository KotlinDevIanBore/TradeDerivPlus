import { describe, expect, it } from 'vitest'
import { dec } from './money'
import { payoutPercentLabel, payoutRatioFor, winCredit } from './digitPayout'

describe('digitPayout', () => {
  it('matches the benchmark Even/Odd credit on a $10 stake', () => {
    const ratio = payoutRatioFor('even_odd', { parity: 'even' })!
    expect(ratio.toString()).toBe('0.952')
    expect(winCredit(dec(10), ratio).toFixed(2)).toBe('19.52')
    expect(payoutPercentLabel(ratio)).toBe('95.20%')
  })

  it('rejects an Over barrier with no winning digits', () => {
    expect(payoutRatioFor('over_under', { side: 'over', barrier: 9 })).toBeNull()
  })
})
