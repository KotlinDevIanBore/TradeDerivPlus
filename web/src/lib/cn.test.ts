import { describe, expect, it } from 'vitest'
// Vite's ?raw import: the config as text, with no Node types needed under the
// app tsconfig. The point is only which keys the fontSize block declares.
import tailwindConfig from '../../tailwind.config.js?raw'
import { cn, CUSTOM_FONT_SIZES } from './cn'

describe('cn', () => {
  it('keeps a custom text size alongside a text colour', () => {
    // The regression this file exists to stop: stock tailwind-merge treated
    // text-meta as a colour and let text-muted replace it.
    const out = cn('text-meta font-semibold', 'text-muted')
    expect(out).toContain('text-meta')
    expect(out).toContain('text-muted')
  })

  it('still lets a later size beat an earlier one', () => {
    expect(cn('text-meta', 'text-body')).toBe('text-body')
    expect(cn('text-xs', 'text-title')).toBe('text-title')
  })

  it('still lets a later colour beat an earlier one', () => {
    expect(cn('text-muted', 'text-accent')).toBe('text-accent')
  })

  it.each(CUSTOM_FONT_SIZES)('treats text-%s as a size, not a colour', (size) => {
    const out = cn(`text-${size}`, 'text-muted')
    expect(out).toContain(`text-${size}`)
    expect(out).toContain('text-muted')
  })

  it('registers every custom fontSize the Tailwind config defines', () => {
    // Read the config as text rather than importing it: it is CommonJS with a
    // plugin import, and the point is only which keys exist.
    const config = tailwindConfig
    const start = config.indexOf('fontSize: {')
    // The block ends at the first line that is only a closing brace at the
    // block's own indentation; a greedy search for '},' overran into the next
    // section and picked up its keys.
    const rest = config.slice(start)
    const end = rest.search(/\n {6}\},?\n/)
    const body = rest.slice(0, end)
    const keys = [...body.matchAll(/^\s+([a-z]+):\s*\[/gm)].map((m) => m[1])
    expect(keys.length).toBeGreaterThan(0)
    expect([...CUSTOM_FONT_SIZES].sort()).toEqual([...keys].sort())
  })
})
