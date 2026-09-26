import { expect, test, type Page } from '@playwright/test'
import { isWide, newPhone, register, requireFeatures, waitForDigitTicket } from './helpers'

test.beforeAll(() => requireFeatures('digit'))

/**
 * The phone layout of the trading screen, measured rather than eyeballed.
 *
 * Every assertion here is a number from
 * docs/investigation-reports/mobile-trading-screen-layout.md, and every one of
 * them passed at 412px (Pixel 7, the only phone width the suite used to run at)
 * while failing on every iPhone width. These tests exist so the fixes have
 * something to fail against first, and so the defects cannot come back at a
 * width nobody happens to be looking at.
 */

/** Pixels of the price chart a user can actually see, sampled by hit-testing
 *  down the chart's centre line. Counts what is on top of the chart, whatever it
 *  is -- a sticky ticket, a toast, a header -- rather than trusting z-index. */
async function visibleChartPx(page: Page): Promise<number> {
  return page.evaluate(() => {
    const chart = document.querySelector('.recharts-responsive-container')
    if (!chart) return -1
    const box = chart.getBoundingClientRect()
    const x = Math.round(box.left + box.width / 2)
    let visible = 0
    for (let y = Math.max(0, Math.ceil(box.top)); y < Math.min(window.innerHeight, box.bottom); y += 2) {
      const hit = document.elementFromPoint(x, y)
      if (hit && chart.contains(hit)) visible += 2
    }
    return visible
  })
}

test.describe('The trading screen on a phone', () => {
  test.beforeEach(async ({ page }) => {
    test.skip(isWide(page), 'these are the phone layout; the desktop has a column for the ticket')
    await register(page, newPhone())
    await waitForDigitTicket(page)
  })

  test('the contract-type tab row fits the screen without scrolling', async ({ page }) => {
    const tablist = page.getByRole('tablist', { name: 'Contract type' })
    const geometry = await tablist.evaluate((el) => ({
      scrollWidth: el.scrollWidth,
      clientWidth: el.clientWidth,
      lastTabRight: Math.round(
        Array.from(el.querySelectorAll('[role=tab]')).at(-1)!.getBoundingClientRect().right,
      ),
      viewport: window.innerWidth,
    }))
    // Was 403px of content in a 375px row: the third tab scrolled off an edge
    // that gave no sign it scrolled. The source asks for 12px text; a
    // tailwind-merge misclassification rendered it at 16px.
    expect(geometry.scrollWidth, 'tab row content wider than its box').toBeLessThanOrEqual(
      geometry.clientWidth,
    )
    expect(geometry.lastTabRight, 'Over/Under extends past the screen edge').toBeLessThanOrEqual(
      geometry.viewport,
    )
  })

  test('the ten digit buttons sit on one row', async ({ page }) => {
    await page.getByRole('tab', { name: 'Over/Under' }).click()
    const group = page.getByRole('group', { name: 'Barrier' })
    const tops = await group.evaluate((el) =>
      Array.from(el.querySelectorAll('button')).map((b) => Math.round(b.getBoundingClientRect().top)),
    )
    expect(tops, 'expected ten digit buttons').toHaveLength(10)
    // Was "0 1 2 3 4 5 6 7" on one row and "8 9" orphaned on the next at 375px.
    expect(new Set(tops).size, `digits wrapped onto ${new Set(tops).size} rows`).toBe(1)
  })

  test('with Over/Under selected the chart keeps at least 160px on screen', async ({ page }) => {
    await page.getByRole('tab', { name: 'Over/Under' }).click()
    await expect(page.getByRole('group', { name: 'Barrier' })).toBeVisible()
    await page.evaluate(() => window.scrollTo(0, 0))

    // 160 is two thirds of the 240px chart, set as the floor for the smallest
    // common phone (375x667), where the sticky ticket used to leave 0px.
    const visible = await visibleChartPx(page)
    expect(visible, 'chart pixels visible at the top of the page').toBeGreaterThanOrEqual(160)
  })

  test('the two outcome buttons are reachable without scrolling', async ({ page }) => {
    await page.getByRole('tab', { name: 'Over/Under' }).click()
    await page.evaluate(() => window.scrollTo(0, 0))

    // Whatever un-covers the chart must not push the primary action below the
    // fold: the point of the screen is to see the price and act on it at once.
    for (const name of [/^Over/, /^Under/]) {
      const button = page.getByRole('button', { name }).first()
      const reachable = await button.evaluate((el) => {
        const b = el.getBoundingClientRect()
        if (b.bottom > window.innerHeight || b.top < 0) return false
        const hit = document.elementFromPoint(b.left + b.width / 2, b.top + b.height / 2)
        return !!hit && (el === hit || el.contains(hit))
      })
      expect(reachable, `${name} is on screen and not covered, at scroll position 0`).toBe(true)
    }
  })
})
