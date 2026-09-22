import { describe, expect, it } from 'vitest'
import { resolvePanel } from './usePanel'

const desktop = { desktop: true, onMainSurface: true }
const phone = { desktop: false, onMainSurface: true }

describe('resolvePanel', () => {
  it('opens Positions on a desktop with nothing in the URL', () => {
    expect(resolvePanel(null, desktop)).toBe('positions')
  })

  it('leaves a phone closed with nothing in the URL', () => {
    expect(resolvePanel(null, phone)).toBe(null)
  })

  it('opens what the URL names, at either width', () => {
    expect(resolvePanel('positions', desktop)).toBe('positions')
    expect(resolvePanel('positions', phone)).toBe('positions')
  })

  it('opens a panel that is not the default, which is why they have names', () => {
    expect(resolvePanel('history', desktop)).toBe('history')
    expect(resolvePanel('history', phone)).toBe('history')
  })

  it('treats the closed sentinel as closed, which is what it is for', () => {
    expect(resolvePanel('none', desktop)).toBe(null)
  })

  it('falls back to the default rather than an empty column on a bad value', () => {
    expect(resolvePanel('nonsense', desktop)).toBe('positions')
    expect(resolvePanel('nonsense', phone)).toBe(null)
  })

  it('stays closed away from the main surface, whatever the URL says', () => {
    expect(resolvePanel('positions', { desktop: true, onMainSurface: false })).toBe(null)
    expect(resolvePanel(null, { desktop: true, onMainSurface: false })).toBe(null)
  })

  it('hides Signals when digit contracts are on', () => {
    expect(resolvePanel('signals', { ...desktop, hideSignals: true })).toBe('positions')
    expect(resolvePanel('signals', { ...phone, hideSignals: true })).toBe(null)
  })
})
