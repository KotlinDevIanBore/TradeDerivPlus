import { describe, expect, it } from 'vitest'
import { digitEven, lastDigit } from './lastDigit'

/** Mirrors internal/models/digit_test.go TestLastDigit. */
describe('lastDigit', () => {
  it.each([
    ['9449.94', 2, 4],
    ['4185.8107', 4, 7],
    ['100', 0, 0],
    ['1.0', 1, 0],
    ['1.25', 2, 5],
  ] as const)('LastDigit(%s, %i) = %i', (price, pip, want) => {
    expect(lastDigit(price, pip)).toBe(want)
  })
})

describe('digitEven', () => {
  it('treats 0,2,4,6,8 as even', () => {
    expect(digitEven(4)).toBe(true)
    expect(digitEven(5)).toBe(false)
  })
})
