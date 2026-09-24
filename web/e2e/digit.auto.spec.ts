import { expect, test } from '@playwright/test'
import {
  accountPill,
  closePanel,
  newPhone,
  openPanel,
  panelOf,
  pillAmount,
  placedCount,
  register,
  startAutoRun,
  waitForDigitTicket,
} from './helpers'

/**
 * Automated runs, end to end against the real stack.
 *
 * Every contract on this platform has negative expected value, so the thing being
 * checked is not that a run makes money — it will not — but that it does exactly
 * what it was told, says so honestly while doing it, and that the money adds up
 * when it stops.
 */
test.describe('Automated trading', () => {
  test('carries the mode on the URL, and leaves the instrument alone', async ({ page }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)

    const tablist = page.getByRole('tablist', { name: 'Trading mode' })
    await expect(tablist.getByRole('tab', { name: 'MANUAL' })).toHaveAttribute(
      'aria-selected',
      'true',
    )

    // The symbol goes on first, so the merge can be observed rather than assumed.
    await page.goto('/?symbol=R_100')
    await waitForDigitTicket(page)
    await tablist.getByRole('tab', { name: 'AUTO' }).click()

    await expect(page).toHaveURL(/mode=auto/)
    // The bug this guards: a mode switch that replaced the query string instead
    // of merging into it would silently reset the chart's instrument.
    await expect(page).toHaveURL(/symbol=R_100/)

    // A reload keeps it, which is the point of putting mode on the URL at all.
    //
    // The wait is on the AUTO tab rather than on the ticket helper: both tickets
    // carry a button called Even, so waiting for one would be satisfied by
    // either, and this test would pass whichever mode came back.
    await page.reload()
    await expect(tablist.getByRole('tab', { name: 'AUTO' })).toHaveAttribute(
      'aria-selected',
      'true',
      { timeout: 45_000 },
    )
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeVisible()

    // Going back drops the parameter rather than writing mode=manual, so the
    // default state has one representation — and the manual ticket comes back.
    await tablist.getByRole('tab', { name: 'MANUAL' }).click()
    await waitForDigitTicket(page)
    await expect(page).not.toHaveURL(/mode=/)
    await expect(page).toHaveURL(/symbol=R_100/)
  })

  test('states what a run costs before it starts', async ({ page }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)
    await page.getByRole('tab', { name: 'AUTO' }).click()
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeEnabled({
      timeout: 30_000,
    })

    // Even/Odd is -2.40%: served by the API, not computed here, so it cannot
    // drift from the table that prices the contract.
    await expect(page.getByText(/Expected return -2\.40% per contract/)).toBeVisible()
    // The stop loss is not the worst case — the contract in flight when it trips
    // still has to settle.
    await expect(page.getByText(/Worst case you lose/)).toBeVisible()

    // Matches is the expensive one and says so.
    await page.getByRole('tab', { name: 'Matches/Differs' }).click()
    await expect(page.getByText(/Expected return -10\.00% per contract/)).toBeVisible()
    await expect(page.getByText('This contract costs the most per play.')).toBeVisible()

    await page.getByRole('tab', { name: 'Even/Odd' }).click()

    // The server's ceiling, refused before a request goes out.
    await page.getByLabel('Stake per contract').fill('101')
    await expect(page.getByText('The most you can stake on one contract is $100.00')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeDisabled()

    // And a run will not start without a floor.
    await page.getByLabel('Stake per contract').fill('10')
    await page.getByLabel('I have lost').fill('')
    await expect(page.getByText('Set a stop loss so the run knows when to stop')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeDisabled()
  })

  // The exit criterion for this phase, in one test: start, watch it work, let it
  // stop on its own limit, and check the money.
  test('places, reports itself, stops on its limit, and the balance reconciles', async ({
    page,
  }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)

    const start = await pillAmount(page)
    expect(start).toBe(10_000)

    await startAutoRun(page, { stopLoss: '30', maxContracts: '3' })

    // While it runs: the card is the ticket, and manual trading is locked rather
    // than quietly available behind a run that is still placing.
    await expect(page.getByRole('button', { name: 'Start auto run' })).toHaveCount(0)
    await expect(page.getByRole('tab', { name: 'MANUAL' })).toBeDisabled()
    await expect(page.getByText('Stop the run to trade manually.')).toBeVisible()
    await expect(page.getByRole('progressbar', { name: 'Towards stop loss' })).toBeVisible()

    // Placements climb.
    const first = await placedCount(page)
    await expect(async () => {
      expect(await placedCount(page)).toBeGreaterThan(first)
    }).toPass({ timeout: 30_000 })

    // The session strip says a run is going, without leaving the chart.
    await expect(
      page.getByRole('region', { name: 'Session activity' }).getByText(/Running/),
    ).toBeVisible()

    // It stops itself: three contracts was the limit it was given.
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeVisible({
      timeout: 90_000,
    })
    await expect(page.getByRole('tab', { name: 'MANUAL' })).toBeEnabled()

    // Stopping is announced, and the announcement carries what happened rather
    // than only that something did.
    await expect(page.getByRole('status').getByText(/Auto run/)).toBeVisible()
    await expect(page.getByRole('status').getByText(/over \d+ contracts?/)).toBeVisible()

    // The strip reports how it ended.
    await expect(
      page.getByRole('region', { name: 'Session activity' }).getByText(/Stopped/),
    ).toBeVisible()

    // Every contract the run placed is attributable on the surfaces that show
    // contracts, which is what the provenance column is for.
    await openPanel(page, 'Positions')
    const positions = panelOf(page, 'Positions')
    await positions.getByRole('tab', { name: 'Closed' }).click()
    await expect(positions.getByText('Bot', { exact: true }).first()).toBeVisible({
      timeout: 30_000,
    })
    await closePanel(page, 'Positions')

    await openPanel(page, 'History')
    const history = panelOf(page, 'History')
    await expect(history.getByText('Bot', { exact: true }).first()).toBeVisible({ timeout: 30_000 })
    await closePanel(page, 'History')

    // And the money. Three $10 contracts can only end in a handful of places, so
    // the balance is checked against the arithmetic rather than a range: each is
    // a win (+9.52), a loss (-10) or a void (0).
    const end = await pillAmount(page)
    const moved = Number((end - start).toFixed(2))
    const outcomes: number[] = []
    for (let wins = 0; wins <= 3; wins++) {
      for (let losses = 0; losses + wins <= 3; losses++) {
        outcomes.push(Number((wins * 9.52 - losses * 10).toFixed(2)))
      }
    }
    expect(outcomes).toContain(moved)
  })

  // The other way a run ends: someone decides it should. The limits here are
  // deliberately out of reach, so the only thing that can stop this run is the
  // button — and so the ending the toast reports can only be `user`.
  test('stops when told to, and says so', async ({ page }) => {
    await register(page, newPhone())
    await waitForDigitTicket(page)

    await startAutoRun(page, { stopLoss: '500', maxContracts: '50' })

    // Stopping before anything has been placed would not exercise the part that
    // matters, which is a stop arriving while the run is mid-cycle.
    await expect(async () => {
      expect(await placedCount(page)).toBeGreaterThan(0)
    }).toPass({ timeout: 30_000 })

    await page.getByRole('button', { name: 'Stop run' }).click()

    // The run may have a contract in flight, and the server holds it in
    // 'stopping' until that settles — so what is waited for is the end of it,
    // not the intermediate state, which the component test covers without a
    // race. The ticket coming back is what "the run is over" looks like.
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeVisible({
      timeout: 60_000,
    })
    await expect(page.getByRole('tab', { name: 'MANUAL' })).toBeEnabled()

    const announcement = page.getByRole('status')
    await expect(announcement.getByText('Auto run stopped')).toBeVisible()
    // Every ending carries what happened, not just that something did.
    await expect(announcement.getByText(/over \d+ contracts?/)).toBeVisible()
  })

  // The card is fed by a socket event with a poll behind it. Both look identical
  // when working, so the fallback is checked with the socket taken away.
  test('keeps reporting a run when the account socket drops', async ({ page }) => {
    let refused = 0
    await page.routeWebSocket(/\/ws\/account/, (ws) => {
      refused++
      ws.close()
    })

    await register(page, newPhone())
    await waitForDigitTicket(page)
    await startAutoRun(page, { stopLoss: '40', maxContracts: '4' })

    expect(refused).toBeGreaterThan(0)

    // Nothing can arrive on the socket, so anything that moves is the poll.
    const first = await placedCount(page)
    await expect(async () => {
      expect(await placedCount(page)).toBeGreaterThan(first)
    }).toPass({ timeout: 30_000 })

    // And the run still resolves back to the ticket without one.
    await expect(page.getByRole('button', { name: 'Start auto run' })).toBeVisible({
      timeout: 90_000,
    })
    await expect(accountPill(page)).toBeVisible()
  })
})
