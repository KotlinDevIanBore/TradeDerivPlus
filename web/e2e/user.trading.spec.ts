import { expect, test } from '@playwright/test'
import {
  accountPill,
  beginClosePosition,
  closePanel,
  newPhone,
  openPanel,
  register,
} from './helpers'

test.describe('A user trading', () => {
  test('registers, opens a position, and closes it with the balance adding up', async ({ page }) => {
    // Signup lands on the surface with a funded demo account; register()
    // asserts the pill it lands on.
    await register(page, newPhone())

    // The ticket stays disabled until a live price arrives.
    const submit = page.getByRole('button', { name: /Buy R_75 for/ })
    await expect(submit).toBeEnabled({ timeout: 30_000 })

    await page.getByLabel('Stake').fill('100')
    await expect(page.getByRole('button', { name: 'Buy R_75 for $100.00' })).toBeEnabled()
    await page.getByRole('button', { name: 'Buy R_75 for $100.00' }).click()

    await expect(page.getByText('Position opened')).toBeVisible()
    await expect(page.getByText('Your positions on R_75')).toBeVisible()

    // The stake left the balance. The pill is in the bar above the chart, so
    // checking it no longer means leaving the page the trade was made on.
    await expect(accountPill(page)).toContainText('$9,900.00')

    await openPanel(page, 'Positions')
    await beginClosePosition(page)

    // The confirmation restates the position before anything settles.
    await expect(page.getByText(/^Close R_75/)).toBeVisible()
    await page
      .getByRole('dialog', { name: 'Close position' })
      .getByRole('button', { name: 'Close position', exact: true })
      .click()

    await expect(page.getByText('Position closed')).toBeVisible()

    // On a phone the panel is a modal and holds the app behind it, so it has
    // to be put away before anything else is reachable.
    await closePanel(page, 'Positions')

    // The stake plus its result came back, so the total is near where it was.
    // The pill also carries the account type and, on a desktop, its number.
    const pill = await accountPill(page).innerText()
    const value = Number(pill.match(/\$([\d,]+\.\d{2})/)![1].replace(/,/g, ''))
    expect(value).toBeGreaterThan(9_000)
    expect(value).toBeLessThan(11_000)

    // Opening and closing panels must leave the feed alone. The chart and the
    // panel both want R_75 priced while the position is open, and a screen
    // letting go of a symbol used to close the socket for everyone.
    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({
      timeout: 30_000,
    })
    await expect(page.getByText('Market feed offline')).toHaveCount(0)
  })

  test('refuses a stake larger than the balance, naming the limit', async ({ page }) => {
    await register(page, newPhone())

    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({ timeout: 30_000 })
    await page.getByLabel('Stake').fill('999999')

    await expect(page.getByText(/You have \$10,000\.00 available/)).toBeVisible()
    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeDisabled()
  })
})
