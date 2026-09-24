import { expect, test } from '@playwright/test'
import {
  closePanel,
  isWide,
  newPhone,
  openPanel,
  panelOf,
  register,
  waitForDigitSurface,
} from './helpers'

/**
 * The chrome in digit mode. Signals is hidden here — its entry/stop-loss/
 * take-profit shape belongs to CFD positions and says nothing about a contract
 * that settles on the next tick — so the panel set is three, not four. The CFD
 * spec asserts the four, which is exactly why the two modes need separate runs.
 */
test.describe('The chrome in digit mode', () => {
  test('carries three panels on a phone, without Signals', async ({ page }) => {
    test.skip(isWide(page), 'the bottom bar is the phone’s chrome')

    await register(page, newPhone())
    await waitForDigitSurface(page)

    const bar = page.getByRole('navigation', { name: 'Bottom' })
    await expect(bar.getByRole('link')).toHaveCount(0)

    for (const name of ['Positions', 'History', 'Account']) {
      await expect(bar.getByRole('button', { name, exact: true })).toHaveAttribute(
        'aria-pressed',
        'false',
      )
    }
    await expect(bar.getByRole('button', { name: 'Signals', exact: true })).toHaveCount(0)
  })

  test('offers Positions and History but not Signals on a desktop', async ({ page }) => {
    test.skip(!isWide(page), 'the top bar row is the desktop’s chrome')

    await register(page, newPhone())
    await waitForDigitSurface(page)

    const bar = page.getByRole('banner')
    await expect(bar.getByRole('button', { name: 'Positions', exact: true })).toBeVisible()
    await expect(bar.getByRole('button', { name: 'History', exact: true })).toBeVisible()
    await expect(bar.getByRole('button', { name: 'Signals', exact: true })).toHaveCount(0)
  })
})

test.describe('The Positions panel in digit mode', () => {
  test('shows contracts settling on the next tick rather than positions to close', async ({
    page,
  }) => {
    await register(page, newPhone())
    await waitForDigitSurface(page)
    await expect(page.getByRole('button', { name: 'Even' })).toBeEnabled({ timeout: 30_000 })

    await page.getByLabel('Stake').fill('5')
    await page.getByRole('button', { name: 'Even' }).click()
    await expect(page.getByText('Even opened')).toBeVisible()

    await openPanel(page, 'Positions')
    const panel = panelOf(page, 'Positions')

    // The digit branch explains that settlement is automatic, and offers no way
    // to close: there is nothing to close, which is the product difference.
    await expect(panel.getByText('Settles next tick').first()).toBeVisible()
    await expect(panel.getByRole('button', { name: 'Close position', exact: true })).toHaveCount(0)

    await closePanel(page, 'Positions')
  })
})

test.describe('The History panel in digit mode', () => {
  test('lists a settled contract with its outcome', async ({ page }) => {
    await register(page, newPhone())
    await waitForDigitSurface(page)
    await expect(page.getByRole('button', { name: 'Even' })).toBeEnabled({ timeout: 30_000 })

    await page.getByLabel('Stake').fill('5')
    await page.getByRole('button', { name: 'Even' }).click()
    await expect(page.getByText('Even opened')).toBeVisible()

    await openPanel(page, 'History')
    const panel = panelOf(page, 'History')

    // The row carries a status chip once the contract has settled, which is the
    // one assertion that proves the whole open-settle-render path ran. The chip
    // reads WON on screen but the text in the DOM is "Won": the capitals are a
    // CSS transform, and getByText matches what the markup says.
    await expect(panel.getByText(/^(Won|Lost|Void)$/).first()).toBeVisible({ timeout: 30_000 })

    await closePanel(page, 'History')
  })
})
