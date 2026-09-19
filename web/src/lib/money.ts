import Decimal from 'decimal.js'

/**
 * Money and prices cross the API as strings. Everything numeric in this app
 * goes through Decimal so a balance is never wrong by a floating point hair.
 */
export function dec(value: string | number | null | undefined): Decimal {
  if (value === null || value === undefined || value === '') return new Decimal(0)
  try {
    return new Decimal(value)
  } catch {
    return new Decimal(0)
  }
}

export { Decimal }
