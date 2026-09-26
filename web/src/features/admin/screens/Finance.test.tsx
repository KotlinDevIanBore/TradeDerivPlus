import { describe, expect, it } from 'vitest'
import type { EdgeCheck } from '../adminTypes'
import {
  FINANCE_SCOPE_NOTE,
  contractTypeLabel,
  edgeNoisePP,
  edgePercent,
  edgeReadout,
  rangeLabel,
} from './Finance'

function edge(over: Partial<EdgeCheck> = {}): EdgeCheck {
  return {
    sample_size: 5000,
    realised: '0.0231',
    theoretical: '0.024',
    too_few_to_read: false,
    min_sample: 1000,
    unpriced: 0,
    ...over,
  }
}

describe('edgeReadout', () => {
  it('withholds the comparison when the sample is too small', () => {
    // Two numbers printed side by side invite subtraction, and at n=15 the
    // difference is noise. The readout names the shortfall instead.
    const out = edgeReadout(edge({ sample_size: 15, too_few_to_read: true }))
    expect(out).toBe('Too few contracts to read — 15 of 1000')
    expect(out).not.toContain('%')
  })

  it('compares realised against theoretical once the sample is large enough', () => {
    const out = edgeReadout(edge())
    expect(out).toContain('+2.31%')
    expect(out).toContain('+2.40%')
    expect(out).toContain('theoretical')
  })
})

describe('edgeNoisePP', () => {
  it('is the standard error in percentage points', () => {
    // 100/sqrt(1000) = 3.16 -> 3.2
    expect(edgeNoisePP(1000)).toBe('±3.2 pp')
    expect(edgeNoisePP(10000)).toBe('±1.0 pp')
  })

  it('has nothing to say about no contracts', () => {
    expect(edgeNoisePP(0)).toBe('—')
    expect(edgeNoisePP(-1)).toBe('—')
  })
})

describe('edgePercent', () => {
  it('turns a fraction into a signed percentage', () => {
    expect(edgePercent('0.024')).toBe('+2.40%')
    expect(edgePercent('-0.1712')).toBe('-17.12%')
    expect(edgePercent('0')).toBe('+0.00%')
  })

  it('renders an absent figure rather than NaN', () => {
    expect(edgePercent(null)).toBe('—')
    expect(edgePercent(undefined)).toBe('—')
    expect(edgePercent('')).toBe('—')
  })
})

describe('the scope note', () => {
  it('says the report covers digit contracts only', () => {
    // The omission of CFD trades has to be visible on the screen, not implied.
    expect(FINANCE_SCOPE_NOTE).toMatch(/Digit contracts only/)
    expect(FINANCE_SCOPE_NOTE).toMatch(/CFD/)
  })
})

describe('rangeLabel', () => {
  it('names every range the API accepts', () => {
    expect(rangeLabel('24h')).toBe('24 hours')
    expect(rangeLabel('7d')).toBe('7 days')
    expect(rangeLabel('30d')).toBe('30 days')
    expect(rangeLabel('all')).toBe('All time')
  })
})

describe('contractTypeLabel', () => {
  it('reads the stored types back as words', () => {
    expect(contractTypeLabel('even_odd')).toBe('Even / Odd')
    expect(contractTypeLabel('matches_differs')).toBe('Matches / Differs')
    expect(contractTypeLabel('over_under')).toBe('Over / Under')
  })

  it('passes an unknown type through rather than blanking it', () => {
    expect(contractTypeLabel('something_new')).toBe('something_new')
  })
})
