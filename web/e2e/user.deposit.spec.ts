import { expect, test } from '@playwright/test'
import { goTo, newPhone, register } from './helpers'

test.describe('Depositing with M-Pesa', () => {
  test('walks amount, waiting and result, and settles without a reload', async ({ page }) => {
    const phone = newPhone()
    await register(page, phone)

    // Deposits are real money, so a real account is needed first.
    await goTo(page, 'Wallet')
    await page.getByRole('button', { name: 'Switch account' }).click()
    await page.getByRole('button', { name: 'Create real account' }).click()
    await expect(page.getByText('REAL').first()).toBeVisible()

    await page.getByRole('button', { name: 'Deposit' }).first().click()
    await expect(page.getByText('Deposit with M-Pesa')).toBeVisible()

    // The phone is prefilled from the profile.
    await expect(page.getByPlaceholder('712 345 678')).toHaveValue(phone.replace(/^0/, ''))

    await page.getByLabel('Amount').fill('12950')
    await expect(page.getByText('≈ $100.00 will be credited')).toBeVisible()

    await page.getByRole('button', { name: 'Send STK push' }).click()

    // The waiting step is the emotional peak: it must say what to do.
    await expect(page.getByText('Check your phone')).toBeVisible()
    await expect(page.getByText(/Enter your M-Pesa PIN to complete the KES 12,950 deposit/)).toBeVisible()

    // The simulated callback lands within a few seconds and the screen
    // resolves on its own, over the socket, with no refresh.
    await expect(
      page.getByText(/KES 12,950 deposited|didn't go through/),
    ).toBeVisible({ timeout: 45_000 })

    const succeeded = await page.getByText('KES 12,950 deposited').isVisible()
    if (succeeded) {
      await expect(page.getByText('$100.00 is now in your balance.')).toBeVisible()
      await page.getByRole('button', { name: 'Done' }).click()
      await expect(page.getByText('$100.00').first()).toBeVisible()
    } else {
      // The mock fails about one in six on purpose; the failure path must
      // explain itself and offer a way back.
      await expect(page.getByRole('button', { name: 'Try again' })).toBeVisible()
    }
  })

  test('sends a demo user to their real account rather than taking money', async ({ page }) => {
    await register(page, newPhone())
    await goTo(page, 'Wallet')

    await page.getByRole('button', { name: 'Top up demo' }).first().click()
    await expect(page.getByText(/Deposits go to a real account|Switch to your real account/)).toBeVisible()
    await expect(page.getByRole('button', { name: 'Send STK push' })).toHaveCount(0)
  })
})
