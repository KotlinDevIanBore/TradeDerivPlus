import { expect, test } from '@playwright/test'
import {
  closePositions,
  goTo,
  newPhone,
  openPositions,
  positionsPanel,
  register,
} from './helpers'

test.describe('A user trading', () => {
  test('registers, opens a position, and closes it with the balance adding up', async ({ page }) => {
    await register(page, newPhone())

    // Signup lands on a usable demo account without a second round trip.
    const balanceCard = page.locator('section[aria-label="Balance"]')
    await expect(balanceCard.getByText('Total balance')).toBeVisible()
    await expect(balanceCard.getByText('$10,000.00', { exact: true })).toBeVisible()
    await expect(balanceCard.getByText('demo', { exact: true })).toBeVisible()

    await goTo(page, 'Trade')
    await page.waitForURL('**/trade*')

    // The ticket stays disabled until a live price arrives.
    const submit = page.getByRole('button', { name: /Buy R_75 for/ })
    await expect(submit).toBeEnabled({ timeout: 30_000 })

    await page.getByLabel('Stake').fill('100')
    await expect(page.getByRole('button', { name: 'Buy R_75 for $100.00' })).toBeEnabled()
    await page.getByRole('button', { name: 'Buy R_75 for $100.00' }).click()

    await expect(page.getByText('Position opened')).toBeVisible()
    await expect(page.getByText('Your positions on R_75')).toBeVisible()

    // The stake left the balance.
    await goTo(page, 'Home')
    await expect(
      page.locator('section[aria-label="Balance"]').getByText('$9,900.00', { exact: true }),
    ).toBeVisible()

    // Positions is a panel over the chart now, not a screen to travel to.
    await goTo(page, 'Trade')
    await openPositions(page)
    await positionsPanel(page)
      .getByRole('button', { name: 'Close position', exact: true })
      .first()
      .click()

    // The confirmation restates the position before anything settles.
    await expect(page.getByText(/^Close R_75/)).toBeVisible()
    await page
      .getByRole('dialog', { name: 'Close position' })
      .getByRole('button', { name: 'Close position', exact: true })
      .click()

    await expect(page.getByText('Position closed')).toBeVisible()

    // On a phone the panel is a modal and holds the app behind it, so it has
    // to be put away before anything else is reachable.
    await closePositions(page)

    // The stake plus its result came back, so the total is near where it was.
    await goTo(page, 'Home')
    const balance = await page.locator('section[aria-label="Balance"] p').nth(1).innerText()
    const value = Number(balance.replace(/[$,]/g, ''))
    expect(value).toBeGreaterThan(9_000)
    expect(value).toBeLessThan(11_000)

    // Walking away and back must leave the feed alone. The screens visited
    // above all want R_75 priced while the position is open, and a screen
    // letting go of a symbol used to close the socket for everyone.
    await goTo(page, 'Trade')
    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({
      timeout: 30_000,
    })
    await expect(page.getByText('Market feed offline')).toHaveCount(0)
  })

  test('refuses a stake larger than the balance, naming the limit', async ({ page }) => {
    await register(page, newPhone())
    await page.goto('/trade')

    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({ timeout: 30_000 })
    await page.getByLabel('Stake').fill('999999')

    await expect(page.getByText(/You have \$10,000\.00 available/)).toBeVisible()
    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeDisabled()
  })
})
