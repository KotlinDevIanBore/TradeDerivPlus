import { dec } from './money'

/**
 * All user-visible number and date formatting lives here, so a balance reads
 * the same on every screen.
 */

/** Money, always two decimals: `$1,234.56`, negatives as `-$12.30`. */
export function money(value: string | number | null | undefined, currency = '$'): string {
  const d = dec(value as string)
  const sign = d.isNegative() ? '-' : ''
  const body = d.abs().toFixed(2).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `${sign}${currency}${body}`
}

/** Money with an explicit sign, for profit and loss: `+$4.20`, `-$4.20`. */
export function signedMoney(value: string | number | null | undefined, currency = '$'): string {
  const d = dec(value as string)
  if (d.isZero()) return `${currency}0.00`
  return (d.isNegative() ? '' : '+') + money(value, currency)
}

/** KES amounts, whole shillings: `KES 1,000`. */
export function kes(value: string | number | null | undefined): string {
  const body = dec(value as string).toFixed(0).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  return `KES ${body}`
}

/**
 * A price at its instrument's precision. Forex shows 5 decimals, volatility
 * indices 2 to 4, so a EUR/USD tick is not rendered as 1.09.
 */
export function price(value: string | number | null | undefined, pipSize = 4): string {
  return dec(value as string)
    .toFixed(pipSize)
    .replace(/\B(?=(\d{3})+(?!\d)\.)/g, ',')
}

/** Percentage with one decimal: `78.4%`. */
export function percent(value: string | number | null | undefined, places = 1): string {
  return `${dec(value as string).toFixed(places)}%`
}

/** Signed percentage, for a PnL line: `+2.4%`. */
export function signedPercent(value: string | number | null | undefined, places = 1): string {
  const d = dec(value as string)
  return (d.isNegative() ? '' : '+') + `${d.toFixed(places)}%`
}

/**
 * Relative time in the shape the plan asks for: "just now", "3m ago", "2h ago",
 * then a date once it stops being useful as an elapsed duration.
 */
export function relativeTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''

  const seconds = Math.floor((Date.now() - then) / 1000)
  if (seconds < 0) return 'just now'
  if (seconds < 45) return 'just now'
  if (seconds < 90) return '1m ago'

  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m ago`

  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h ago`

  const days = Math.floor(hours / 24)
  if (days < 7) return `${days}d ago`

  return new Date(iso).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
}

/** Absolute timestamp for a detail sheet: `14 Sep 2026, 20:04`. */
export function absoluteTime(iso: string | null | undefined): string {
  if (!iso) return ''
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return ''
  return d.toLocaleString(undefined, {
    day: 'numeric', month: 'short', year: 'numeric',
    hour: '2-digit', minute: '2-digit',
  })
}

/** Day heading for the grouped closed-trades list: `Today`, `Yesterday`, or a date. */
export function dayLabel(iso: string): string {
  const d = new Date(iso)
  const today = new Date()
  const yesterday = new Date(today)
  yesterday.setDate(today.getDate() - 1)

  const sameDay = (a: Date, b: Date) =>
    a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

  if (sameDay(d, today)) return 'Today'
  if (sameDay(d, yesterday)) return 'Yesterday'
  return d.toLocaleDateString(undefined, { weekday: 'long', day: 'numeric', month: 'short' })
}

/** Human label for a ledger row type. */
export function transactionTitle(type: string, metadata?: Record<string, unknown> | null): string {
  switch (type) {
    case 'deposit': return 'M-Pesa deposit'
    case 'withdrawal': return 'Withdrawal to M-Pesa'
    case 'bonus_credit': return (metadata?.note as string) || 'Bonus credit'
    case 'admin_adjustment': return 'Balance adjustment'
    case 'trade_open': {
      const s = metadata?.symbol as string | undefined
      const dir = (metadata?.direction as string | undefined)?.toUpperCase()
      return s ? `Opened ${s} ${dir ?? ''}`.trim() : 'Position opened'
    }
    case 'trade_close': {
      const s = metadata?.symbol as string | undefined
      return s ? `Closed ${s}` : 'Position closed'
    }
    default: return type
  }
}

/** Friendly label for a symbol category, used as a picker section heading. */
export function categoryLabel(category: string): string {
  switch (category) {
    case 'volatility': return 'Volatility indices'
    case 'boom_crash': return 'Boom & Crash'
    case 'forex': return 'Forex'
    default: return category
  }
}
