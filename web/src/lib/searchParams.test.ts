import { describe, expect, it } from 'vitest'
import { mergeParams } from './searchParams'

const from = (query: string) => new URLSearchParams(query)

describe('mergeParams', () => {
  it('keeps parameters it was not asked about', () => {
    const next = mergeParams({ tab: 'closed' })(from('symbol=R_75&panel=positions'))

    expect(next.get('symbol')).toBe('R_75')
    expect(next.get('panel')).toBe('positions')
    expect(next.get('tab')).toBe('closed')
  })

  it('overwrites a parameter that is already set', () => {
    const next = mergeParams({ symbol: 'R_100' })(from('symbol=R_75'))

    expect(next.getAll('symbol')).toEqual(['R_100'])
  })

  it('removes a parameter given null, while setting others', () => {
    const next = mergeParams({ symbol: 'R_100', sl: null, tp: null })(
      from('symbol=R_75&sl=4200&tp=4300&tab=open'),
    )

    expect(next.get('symbol')).toBe('R_100')
    expect(next.has('sl')).toBe(false)
    expect(next.has('tp')).toBe(false)
    expect(next.get('tab')).toBe('open')
  })

  it('does not mutate the parameters it was given', () => {
    const previous = from('symbol=R_75')
    mergeParams({ symbol: 'R_100' })(previous)

    expect(previous.get('symbol')).toBe('R_75')
  })
})
