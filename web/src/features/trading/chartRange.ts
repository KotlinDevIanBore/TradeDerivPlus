import { dec, type Decimal } from '@/lib/money'
import type { Tick } from '@/types'

/** Minutes of tick history the chart shows. Its own module so the toggle and
 * the screen can import it without pulling the charting library in. */
export type ChartRange = 1 | 5 | 15

/**
 * The ticks the chart is currently drawing. The header's change figure and the
 * chart itself both read the window through here, so the percentage can never
 * describe a slice the chart is not showing.
 */
export function windowTicks(ticks: Tick[], range: ChartRange): Tick[] {
  const cutoff = Date.now() / 1000 - range * 60
  return ticks.filter((t) => t.epoch >= cutoff)
}

export interface WindowChange {
  /** Price movement across the window, in the instrument's own units. */
  absolute: Decimal
  percent: Decimal
}

/**
 * How far price has travelled across the visible window: first tick against
 * last. This is deliberately not a daily change -- the server keeps only a
 * short rolling tick buffer, so a session or daily open price does not exist.
 */
export function windowChange(ticks: Tick[], range: ChartRange): WindowChange | null {
  const visible = windowTicks(ticks, range)
  if (visible.length < 2) return null

  const first = dec(visible[0].price)
  const last = dec(visible[visible.length - 1].price)
  if (first.isZero()) return null

  return {
    absolute: last.minus(first),
    percent: last.minus(first).dividedBy(first).times(100),
  }
}
