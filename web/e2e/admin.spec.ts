import { expect, test } from '@playwright/test'
import { api, loginAdmin, newPhone, registerViaApi } from './helpers'


test.describe('The admin console', () => {
  test('freezes an account and the user is refused with the reason', async ({ page, browser }) => {
    const phone = newPhone()
    const fixture = await registerViaApi(phone)

    await loginAdmin(page)

    // Go straight to the row rather than searching, so this test is about
    // the freeze rather than about search.
    await page.goto(`/admin/users/${fixture.user.id}`)

    // Wait for the account card, which is what this test acts on.
    await expect(page.getByRole('button', { name: 'Freeze' }).first()).toBeVisible()

    await page.getByRole('button', { name: 'Freeze' }).first().click()
    await expect(page.getByText(/Open positions stay open and can still be closed/)).toBeVisible()

    // Nothing that touches an account happens without a written reason.
    const confirm = page.getByRole('button', { name: 'Freeze account' })
    await expect(confirm).toBeDisabled()
    await page.getByLabel(/Reason/).fill('Suspected duplicate signup, under investigation')
    await expect(confirm).toBeEnabled()
    await confirm.click()

    await expect(page.getByText('FROZEN')).toBeVisible()

    // The user, in their own browser, is refused and told why.
    const userContext = await browser.newContext()
    const userPage = await userContext.newPage()
    await userPage.goto('/login')
    await userPage.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
    await userPage.getByLabel('Password').fill('hunter2024')
    await userPage.getByRole('button', { name: 'Log in' }).click()
    await userPage.waitForURL('**/')

    await userPage.goto('/trade')
    await expect(userPage.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({ timeout: 30_000 })
    await userPage.getByRole('button', { name: /Buy R_75 for/ }).click()
    await expect(userPage.getByText(/This account is frozen/)).toBeVisible()

    await userContext.close()

    // And the action is on the audit log with the reason attached.
    await page.getByRole('link', { name: 'Audit log' }).click()
    await expect(page.getByText('Froze account').first()).toBeVisible()
    await expect(page.getByText(/Suspected duplicate signup/).first()).toBeVisible()
  })

  test('reviews a KYC submission and the user becomes verified', async ({ page }) => {
    await loginAdmin(page)
    await page.getByRole('link', { name: 'KYC queue' }).click()
    await page.waitForURL('**/admin/kyc**')

    const pending = page.getByRole('button', { name: 'Approve' })
    const count = await pending.count()
    test.skip(count === 0, 'no pending submissions seeded; run: go run ./cmd/seed')

    // The placeholder document must read as one, not as a broken link.
    await expect(page.getByText(/PLACEHOLDER/).first()).toBeVisible()

    await pending.first().click()
    await expect(page.getByText(/This enables withdrawals on/)).toBeVisible()
    await page.getByRole('button', { name: 'Approve', exact: true }).last().click()

    await expect(page.getByText('Verification approved')).toBeVisible()

    // And it lands on the audit log attributed to this admin.
    await page.getByRole('link', { name: 'Audit log' }).click()
    await expect(page.getByText('Approved KYC').first()).toBeVisible()
  })

  test('shows the ledger reconciling on the dashboard', async ({ page }) => {
    await loginAdmin(page)
    await expect(page.getByText('Ledger reconciles.')).toBeVisible()
  })

  test('refuses a user token on an admin route', async ({ page }) => {
    const fixture = await registerViaApi(newPhone())

    const res = await api('/admin/dashboard', { token: fixture.access_token })
    expect(res.status).toBe(401)
  })
})
