import { dec } from '@/lib/money'

/**
 * Last decimal digit of a price at the instrument's pip size — must match
 * `models.LastDigit` in Go exactly, or the digit row and settlement disagree.
 *
 * Rule (from internal/models/digit.go):
 *  1. Format the price with exactly pipSize digits after the decimal point
 *     (fixed-point), e.g. pipSize 2 → "9449.94".
 *  2. Walk the string from the end and take the first character that is a
 *     digit 0–9. That character, as an integer, is the last digit.
 *
 * Examples: 9449.94 @ 2 → 4; 4185.8107 @ 4 → 7; 100 @ 0 → 0.
 */
export function lastDigit(price: string | number, pipSize: number): number {
  const places = pipSize < 0 ? 0 : pipSize
  const s = dec(price).toFixed(places)
  for (let i = s.length - 1; i >= 0; i--) {
    const c = s.charCodeAt(i)
    if (c >= 48 && c <= 57) return c - 48
  }
  return 0
}

export function digitEven(d: number): boolean {
  return d % 2 === 0
}
