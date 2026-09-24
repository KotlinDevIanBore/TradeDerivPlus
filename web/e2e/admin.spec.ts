import { expect, test } from '@playwright/test'
import { PASSWORD, api, loginAdmin, newPhone, registerViaApi, startRunViaApi } from './helpers'


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

    // Exact, and in the badge's own casing: a loose match also finds the
    // toast that says "Account NNNNNN frozen" while it is still on screen.
    await expect(page.getByText('frozen', { exact: true })).toBeVisible()

    // The user, in their own browser, is refused and told why.
    const userContext = await browser.newContext()
    const userPage = await userContext.newPage()
    await userPage.goto('/login')
    await userPage.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
    await userPage.getByLabel('Password').fill('hunter2024')
    await userPage.getByRole('button', { name: 'Log in' }).click()
    await userPage.waitForURL('**/')

    // Logging in lands on the surface; the ticket is right there.
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

/**
 * Automated trading, from the console.
 *
 * Serial, and in this order, because these three cases are one story and each
 * depends on the state the one before it left. The kill switch does not only
 * stop runs, it turns the feature off, so a case that needs to start a run
 * cannot follow it — and the last case is the way back, which needs it off to
 * have anything to turn on. Run as a block, the suite ends with the switch as it
 * found it.
 */
test.describe.serial('Automated trading in the console', () => {
  test('stops one run from the runs table, and only that one', async ({ page }) => {
    const fixture = await registerViaApi(newPhone())
    const bystander = await registerViaApi(newPhone())
    const run = await startRunViaApi(fixture.access_token, fixture.account.id)
    await startRunViaApi(bystander.access_token, bystander.account.id)

    await loginAdmin(page)
    await page.getByRole('link', { name: 'Auto runs' }).click()
    await page.waitForURL('**/admin/bot-runs**')

    // The row is found by whose run it is, which is also what the table is for.
    const row = page.getByRole('row').filter({ hasText: fixture.user.phone_number })
    await expect(row).toBeVisible({ timeout: 30_000 })

    await row.getByRole('button', { name: 'Stop' }).click()

    // The confirmation names the run's owner rather than saying "this run": the
    // rows differ only by a phone number, and acting on the one above the one
    // you meant is the mistake this guards.
    await expect(page.getByText(fixture.user.phone_number + "'s run")).toBeVisible()
    await expect(page.getByText(/Automated trading stays on/)).toBeVisible()

    const confirm = page.getByRole('button', { name: 'Stop run' })
    await expect(confirm).toBeDisabled()
    await page.getByLabel(/Reason/).fill('Account holder asked for this run to be stopped')
    await confirm.click()

    await expect(page.getByRole('status').getByText(/Run stopp(ed|ing)/)).toBeVisible()

    // It leaves the live list — after the contract it had in flight settles, so
    // this waits rather than asserting immediately.
    await expect(row).toHaveCount(0, { timeout: 60_000 })

    // The other user's run is untouched. This is the whole difference between a
    // per-row stop and the kill switch.
    await expect(
      page.getByRole('row').filter({ hasText: bystander.user.phone_number }),
    ).toBeVisible()

    // And it is reachable as history, which is what the finished tab is for.
    await page.getByRole('button', { name: 'Finished' }).click()
    const finished = page.getByRole('row').filter({ hasText: fixture.user.phone_number })
    await expect(finished).toBeVisible({ timeout: 30_000 })
    await expect(finished.getByText('Stopped here')).toBeVisible()
    // A finished run offers no stop.
    await expect(finished.getByRole('button', { name: 'Stop' })).toHaveCount(0)

    // On the record, under its own action rather than the kill switch's.
    await page.getByRole('link', { name: 'Audit log' }).click()
    await expect(page.getByText('Stopped an auto run').first()).toBeVisible()
    await expect(page.getByText(/Account holder asked for this run/).first()).toBeVisible()

    expect(run.status).toBe('running')
  })

  test('stops all auto runs and the user is told why', async ({ page, browser }) => {
    const phone = newPhone()
    const fixture = await registerViaApi(phone)
    await startRunViaApi(fixture.access_token, fixture.account.id)

    // The user is watching their own app when it happens, which is the half of
    // this that matters to them.
    const userContext = await browser.newContext()
    const userPage = await userContext.newPage()
    await userPage.goto('/login')
    await userPage.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
    await userPage.getByLabel('Password').fill(PASSWORD)
    await userPage.getByRole('button', { name: 'Log in' }).click()
    await userPage.waitForURL('**/')

    await loginAdmin(page)

    // The dashboard says something is running before the switch is thrown.
    const tile = page.getByText('Live auto runs').locator('..')
    await expect(tile.getByText('placing contracts now')).toBeVisible({ timeout: 30_000 })

    await page.getByRole('button', { name: 'Stop all auto runs' }).click()
    await expect(page.getByText(/Automated trading will also be turned off/)).toBeVisible()

    const confirm = page.getByRole('button', { name: 'Stop all auto runs' }).last()
    await expect(confirm).toBeDisabled()
    await page.getByLabel(/Reason/).fill('Halting automated trading pending a payout table review')
    await confirm.click()

    await expect(page.getByRole('status').getByText(/auto runs? stopped/)).toBeVisible()

    // The user is told, and told why — not merely that something happened.
    await expect(userPage.getByRole('status').getByText('Auto run stopped')).toBeVisible({
      timeout: 30_000,
    })
    await expect(
      userPage.getByRole('status').getByText(/Automated trading was turned off/),
    ).toBeVisible()
    await userContext.close()

    // The tile follows, and loses its sub-line with the last run.
    await expect(tile.getByText('placing contracts now')).toHaveCount(0, { timeout: 30_000 })

    await page.getByRole('link', { name: 'Audit log' }).click()
    await expect(page.getByText('Stopped all auto runs').first()).toBeVisible()
    await expect(page.getByText(/pending a payout table review/).first()).toBeVisible()
  })

  test('turns automated trading back on, and runs can start again', async ({ page }) => {
    const fixture = await registerViaApi(newPhone())

    // The previous case left the switch off. That it stays off across a page
    // load is the point: this is a stored decision, not a session's state.
    await loginAdmin(page)
    const tile = page.getByText('Live auto runs').locator('..')
    await expect(tile.getByText('automated trading is off')).toBeVisible({ timeout: 30_000 })

    // And it is off for real, not merely drawn that way.
    const refused = await api('/bot/runs', {
      token: fixture.access_token,
      method: 'POST',
      body: {
        account_id: fixture.account.id,
        symbol: 'R_75',
        contract_type: 'even_odd',
        selection: { parity: 'even' },
        stake: '10',
        interval_ms: 2000,
        limits: { stop_loss: '500' },
      },
    })
    expect(refused.status).toBe(403)

    // The control reads the state: where the kill switch was, there is now the
    // way back.
    await expect(page.getByRole('button', { name: 'Stop all auto runs' })).toHaveCount(0)
    await page.getByRole('button', { name: 'Turn auto runs back on' }).click()

    // The one thing an operator could assume the opposite of.
    await expect(page.getByText(/Runs the kill switch stopped stay stopped/)).toBeVisible()

    const confirm = page.getByRole('button', { name: 'Turn auto runs on' })
    await expect(confirm).toBeDisabled()
    await page.getByLabel(/Reason/).fill('Payout table review closed, automated trading restored')
    await confirm.click()

    await expect(page.getByRole('status').getByText('Automated trading is on')).toBeVisible()
    await expect(tile.getByText('automated trading is off')).toHaveCount(0, { timeout: 30_000 })
    await expect(page.getByRole('button', { name: 'Stop all auto runs' })).toBeVisible()

    // The claim the whole feature rests on: a user can start a run again.
    const started = await startRunViaApi(fixture.access_token, fixture.account.id)
    expect(started.status).toBe('running')

    // Both halves of the incident are on the record, and the restart is its own
    // action rather than another bot_kill row.
    await page.getByRole('link', { name: 'Audit log' }).click()
    await expect(page.getByText('Turned auto runs back on').first()).toBeVisible()
    await expect(page.getByText(/Payout table review closed/).first()).toBeVisible()
    await expect(page.getByText('Stopped all auto runs').first()).toBeVisible()
  })
})
