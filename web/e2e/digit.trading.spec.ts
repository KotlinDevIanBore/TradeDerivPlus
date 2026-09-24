import { expect, test } from '@playwright/test'
import {
  accountPill,
  newPhone,
  pillAmount,
  register,
  requireFeatures,
  waitForDigitTicket,
} from './helpers'

// Fails in seconds if this job's API has the digit surface off.
test.beforeAll(() => requireFeatures('digit'))

/**
 * The digit surface, which is what the app is when digit_contracts_enabled is on
 * — and which until now no browser test touched at all. These specs run in their
 * own Playwright projects against an API started with the flag on, because the
 * CFD specs assert a ticket and a panel set that digit mode replaces.
 */
test.describe('A user trading digit contracts', () => {
  test('opens an Even contract and the balance lands on one of three outcomes', async ({
    page,
  }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)

    expect(await pillAmount(page)).toBe(10_000)

    await page.getByLabel('Stake').fill('10')

    // The button carries its own payout, so it is named for the outcome plus the
    // quote the server returned for this stake.
    const even = page.getByRole('button', { name: 'Even' })
    await expect(even).toContainText('$19.52')
    await even.click()

    await expect(page.getByText('Even opened')).toBeVisible()

    // A digit contract settles on the next tick with no action from anyone, so
    // the only three ends it can have are a win at the quoted payout, a loss of
    // the stake, or a void that refunds it. Anything else is a settlement bug.
    //
    // 10,009.52 = stake back plus 95.2% profit · 9,990.00 = stake lost
    // 10,000.00 = voided and refunded
    await expect(async () => {
      expect([10_009.52, 9_990, 10_000]).toContain(await pillAmount(page))
    }).toPass({ timeout: 30_000 })

    // Whatever it settled to, it is in the ledger with an outcome on it.
    await expect(
      page.getByText(/^(Even won|Even lost|R_75 voided|R_75 won|R_75 lost)/).first(),
    ).toBeVisible({ timeout: 30_000 })
  })

  test('refuses a stake above the per-contract ceiling, naming it', async ({ page }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)

    // Well inside a $10,000 balance, so only the server-side ceiling can refuse
    // it. The client does not pre-empt this one: the limit lives in one place and
    // the message names the figure.
    await page.getByLabel('Stake').fill('101')
    await page.getByRole('button', { name: 'Even' }).click()

    await expect(
      page.getByText('The most you can stake on one contract is $100.00'),
    ).toBeVisible()

    // Nothing was placed, so nothing left the balance.
    await expect(accountPill(page)).toContainText('$10,000.00')
  })

  // The per-account open-contract cap is not asserted here on purpose. Over HTTP
  // the placement rate bucket refuses a rapid burst before five contracts can be
  // in flight at once, so a browser test would pass on the limiter rather than on
  // the cap. Both are covered where they can be forced deterministically: the cap
  // by TestDigitOpenCapIsRaceFree, the bucket by the burst in scripts/smoke.py.
})
