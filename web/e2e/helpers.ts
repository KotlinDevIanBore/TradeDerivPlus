import { expect, type Page } from '@playwright/test'

export const API = process.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'

/** A fresh Safaricom-shaped number, so each run gets its own user. */
export function newPhone(): string {
  return `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
}

export const PASSWORD = 'hunter2024'

export function isWide(page: Page) {
  return (page.viewportSize()?.width ?? 0) >= 1024
}

/**
 * The shell's account pill. It is where a balance is read now that Home is
 * gone, and its presence is what "the session is live" looks like: it renders
 * only once the account and the wallet have answered.
 */
export function accountPill(page: Page) {
  return page.getByRole('button', { name: 'Switch account' })
}

/**
 * The control that opens a panel, in whichever bar this viewport shows: the
 * top bar on a desktop, the bottom bar on a phone. Both are toggles rather
 * than links -- there is nowhere to navigate to -- so both are buttons named
 * for the panel, and the bottom bar's name comes from its aria-label because
 * the icon stands alone at that size.
 *
 * Scoped to the bar, because the other bar is in the DOM at every width, just
 * hidden.
 */
export function panelToggle(page: Page, name: string) {
  const bar = isWide(page)
    ? page.getByRole('banner')
    : page.getByRole('navigation', { name: 'Bottom' })

  return bar.getByRole('button', { name, exact: true })
}

/**
 * A panel, whichever container it is in: a column in the page on a desktop, a
 * modal sheet on a phone. Specs care that the content is there, not which of
 * the two is rendering it.
 */
export function panelOf(page: Page, name: string) {
  return page
    .getByRole('complementary', { name })
    .or(page.getByRole('dialog', { name, exact: true }))
}

/**
 * Opens a panel however this viewport opens it, and leaves it open.
 *
 * The control is a toggle at both widths, and a one-shot "is it open?" check
 * raced the render that follows: probe too early, decide it is closed, and the
 * click that follows shuts a panel that had just opened. So the whole
 * open-and-check retries until the panel is actually there.
 */
export async function openPanel(page: Page, name: string) {
  const it = panelOf(page, name)

  await expect(async () => {
    if (!(await it.isVisible())) {
      await panelToggle(page, name).click()
    }
    await expect(it).toBeVisible({ timeout: 2_000 })

    // And still open a moment later. A probe that lands between a navigation
    // and the render it causes can read "closed" for a panel that was opening,
    // and the click that follows shuts it just after this saw it open.
    await page.waitForTimeout(400)
    await expect(it).toBeVisible({ timeout: 1_000 })
  }).toPass({ timeout: 20_000 })
}

/**
 * Clicks a position card's Close, and leaves the confirmation open.
 *
 * A card redraws as its price ticks, so a click can land on a node React has
 * just replaced. Playwright retries a detached click by itself but can lose
 * the same race again, so the click and its result are retried together.
 */
export async function beginClosePosition(page: Page) {
  const confirm = page.getByRole('dialog', { name: 'Close position' })

  await expect(async () => {
    if (!(await confirm.isVisible())) {
      await panelOf(page, 'Positions')
        .getByRole('button', { name: 'Close position', exact: true })
        .first()
        .click({ timeout: 5_000 })
    }
    await expect(confirm).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 30_000 })
}

export async function closePanel(page: Page, name: string) {
  // Exact: "Close Positions" would otherwise also answer to a search for the
  // "Close position" button on a card, which is a different thing entirely.
  await page.getByRole('button', { name: `Close ${name}`, exact: true }).click()
}

/**
 * Clicks Create account, and waits its turn if the limiter says to.
 *
 * The API's auth limiter is keyed by client IP, and every user a suite invents
 * shares one — so a suite of twenty signups looks to it like one person signing
 * up twenty times, which is exactly what it exists to refuse. CI raises the
 * limits for that reason; a developer running the suite against an ordinarily
 * configured API has no such thing, and the symptom was a `waitForURL` that
 * timed out sixty seconds later with nothing to point at.
 *
 * So a refusal is treated as what it is — the suite queueing behind itself —
 * and any other failure is rethrown untouched, because a signup that breaks for
 * a real reason must still break the test.
 */
async function submitSignup(page: Page) {
  const submit = page.getByRole('button', { name: 'Create account' })
  const refused = page.getByRole('alert').filter({ hasText: 'Too many requests' })

  for (let attempt = 1; attempt <= 4; attempt++) {
    await submit.click()

    // A refusal is instant, where a signup costs a round trip and a redirect —
    // so the refusal is the one worth a short look, and everything else gets
    // the full budget.
    const wasRefused = await refused
      .waitFor({ state: 'visible', timeout: 2_000 })
      .then(() => true)
      .catch(() => false)

    if (!wasRefused) {
      await page.waitForURL('**/', { timeout: 20_000 })
      return
    }

    // One token every AUTH_RATE_EVERY, 3s by default, and a browser spends two
    // per signup — the CORS preflight being the other.
    await page.waitForTimeout(7_000)
  }

  throw new Error('Signup was rate limited four times over. Is the API set up for a test run?')
}

/**
 * Refuses to run a spec whose feature is switched off on the API.
 *
 * These suites are split across two CI jobs because the surfaces are chosen by
 * server state rather than by a per-test option, and a job pointed at the wrong
 * one does not fail in a way that says so: every test waits out its timeout for
 * an element that will never render, which is minutes of "element not found"
 * for a single misconfiguration.
 *
 * Deliberately a failure and not a skip. A suite that quietly passes by testing
 * nothing is worse than one that fails, and the whole reason these tests exist
 * is that nobody was checking the digit surface at all.
 */
export async function requireFeatures(...needed: Array<'digit' | 'bot'>) {
  const fixture = await registerViaApi(newPhone())
  const res = await api('/auth/me', { token: fixture.access_token })
  const me = (await res.json()) as {
    digit_contracts_enabled?: boolean
    bot_enabled?: boolean
  }

  if (needed.includes('digit') && !me.digit_contracts_enabled) {
    throw new Error(
      'The digit trading surface is off for this API, so none of these tests can pass. ' +
        'They belong to the "End to end (digit)" job, which starts the API with ' +
        'DIGIT_CONTRACTS_ENABLED=true — if this is the CFD job, it is selecting projects ' +
        'it should not be running.',
    )
  }
  if (needed.includes('bot') && !me.bot_enabled) {
    throw new Error(
      'Automated trading is off for this API, so none of these tests can pass. ' +
        'Start it with BOT_ENABLED=true (CI sets this on both e2e jobs), or turn it ' +
        'back on from the admin console if a kill switch left it off.',
    )
  }
}

/**
 * Refuses to run a spec that would put a real M-Pesa charge on somebody.
 *
 * The deposit suite drives the sheet for KES 12,950 against a phone number the
 * helper invented. Simulated, that is a test. Against real Daraja credentials it
 * is an STK push to a stranger for about thirteen thousand shillings, on every
 * CI run — and nothing in the spec itself can tell the difference, because the
 * UI is identical either way.
 *
 * So the API is asked. Fails closed: unreachable, or anything other than a
 * plain true, is treated as live.
 */
export async function requireMockMpesa() {
  const root = API.replace(/\/api\/v1$/, '')
  let mocked: unknown
  try {
    const res = await fetch(`${root}/health`)
    mocked = ((await res.json()) as { mock_mpesa?: unknown }).mock_mpesa
  } catch {
    mocked = undefined
  }
  if (mocked !== true) {
    throw new Error(
      'This API is not in mock M-Pesa mode, so this suite will not run: it would send a real ' +
        'STK push for KES 12,950 to a phone number it made up. Start the API with ' +
        'USE_MOCK_MPESA=true, which is what both CI e2e jobs do.',
    )
  }
}

/** Registers through the UI, which is also how the signup path gets covered. */
export async function register(page: Page, phone: string, name = 'E2E User') {
  await page.goto('/register')

  await page.getByLabel('Full name').fill(name)
  await page.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByLabel('Confirm password').fill(PASSWORD)
  await page.getByRole('checkbox').check()

  await submitSignup(page)

  // Signup lands on the surface, which is the app. There is no Home to check
  // for any more, so the pill in the top bar is the landing: a demo account,
  // funded, without a second round trip.
  await expect(accountPill(page)).toContainText('demo')
  await expect(accountPill(page)).toContainText('$10,000.00')
}

/**
 * The balance the account pill is showing, as a number.
 *
 * Money crosses the wire as a string and is rendered with separators, so a spec
 * that wants to compare balances has to come back through the formatting. Kept
 * here because more than one spec needs it and getting the regex subtly wrong is
 * the kind of thing that passes for the wrong reason.
 */
export async function pillAmount(page: Page): Promise<number> {
  const text = await accountPill(page).innerText()
  const match = text.match(/\$([\d,]+\.\d{2})/)
  if (!match) throw new Error(`no amount in the account pill: ${JSON.stringify(text)}`)
  return Number(match[1].replace(/,/g, ''))
}

/**
 * Waits for the digit trading surface, which is what the main surface renders
 * when digit_contracts_enabled is on.
 *
 * The wait is long because the flag arrives on /auth/me and the client polls it
 * every ten seconds: a client that loaded before the flag was readable shows the
 * CFD ticket until the next poll. The Even button is the tell, since the CFD
 * ticket has no such control.
 */
export async function waitForDigitSurface(page: Page) {
  await expect(page.getByRole('button', { name: 'Even' })).toBeVisible({ timeout: 45_000 })
}

/**
 * Waits for the digit ticket to be able to place a contract, which needs a live
 * price: the ticket is disabled until the first tick arrives and while the feed
 * is reconnecting.
 */
export async function waitForDigitTicket(page: Page) {
  await waitForDigitSurface(page)
  await expect(page.getByRole('button', { name: 'Even' })).toBeEnabled({ timeout: 30_000 })
}

/**
 * The seeded password is what a freshly seeded development database has. If a
 * previous run changed it, the alternative is tried rather than the suite
 * failing on an assumption about prior state.
 */
export const ADMIN_PASSWORDS = ['admin123', 'e2eConsoleAdmin1']

export async function loginAdmin(page: Page) {
  for (const password of ADMIN_PASSWORDS) {
    // Ask the API which password works before driving the form, so a failed
    // attempt is not a flaky UI interaction.
    const res = await api('/admin/login', {
      method: 'POST',
      body: { username: 'admin', password },
    })
    if (!res.ok) continue

    await page.goto('/admin/login')
    await page.getByLabel('Username').fill('admin')
    await page.getByLabel('Password').fill(password)
    await page.getByRole('button', { name: 'Sign in' }).click()

    // Wait for the login screen to be left behind. Matching /admin loosely
    // would match /admin/login too and return before the session exists.
    await page.waitForURL((url) => !url.pathname.endsWith('/admin/login'))
    return
  }
  throw new Error(
    'no known admin password works. Reset the console credential with:\n' +
      '  go run ./cmd/seed -reset-admin',
  )
}

/**
 * Drives the API directly for setup a test needs but is not itself testing,
 * so a scenario stays about the thing it is checking.
 */
export async function api(
  path: string,
  init: { method?: string; body?: unknown; token?: string } = {},
): Promise<Response> {
  return fetch(`${API}${path}`, {
    method: init.method ?? 'GET',
    headers: {
      'Content-Type': 'application/json',
      ...(init.token ? { Authorization: `Bearer ${init.token}` } : {}),
    },
    body: init.body === undefined ? undefined : JSON.stringify(init.body),
  })
}

export async function registerViaApi(phone: string) {
  const res = await api('/auth/register', {
    method: 'POST',
    body: { phone_number: phone, password: PASSWORD, full_name: 'E2E Fixture' },
  })
  return (await res.json()) as {
    // The stored form, 2547…, which is what the admin console renders — not the
    // 07… a test typed in.
    user: { id: string; phone_number: string }
    account: { id: string; account_number: string }
    access_token: string
  }
}

/**
 * Starts an automated run through the API, for specs about something other than
 * the ticket that starts one.
 *
 * The limits are deliberately out of reach, so the run is still live when the
 * test acts on it and the only thing that can end it is the test.
 */
export async function startRunViaApi(token: string, accountId: string) {
  const res = await api('/bot/runs', {
    token,
    method: 'POST',
    body: {
      account_id: accountId,
      symbol: 'R_75',
      contract_type: 'even_odd',
      selection: { parity: 'even' },
      stake: '10',
      interval_ms: 2000,
      limits: { stop_loss: '500', max_contracts: 500 },
    },
  })
  if (!res.ok) {
    const body = await res.text()
    // Almost always one thing: an earlier run of the admin suite threw the kill
    // switch and stopped before the case that turns it back on. Say so, rather
    // than leaving a 403 to be decoded.
    throw new Error(
      `Could not start a run (${res.status}): ${body}. ` +
        'If this says bot_disabled, automated trading is still off — turn it back on from the ' +
        'console (Dashboard → Turn auto runs back on), or reseed the database.',
    )
  }
  return (await res.json()) as { id: string; status: string }
}

/**
 * Switches to AUTO and starts a run with the given limits.
 *
 * Kept here because the fields are the same at both widths and getting them
 * wrong in one spec and right in another is how a suite stops meaning anything.
 */
export async function startAutoRun(
  page: Page,
  { stopLoss = '30', maxContracts = '3' }: { stopLoss?: string; maxContracts?: string } = {},
) {
  await page.getByRole('tab', { name: 'AUTO' }).click()
  await expect(page.getByRole('button', { name: 'Start auto run' })).toBeEnabled({
    timeout: 30_000,
  })

  await page.getByLabel('I have lost').fill(stopLoss)
  await page.getByLabel('Contracts placed').fill(maxContracts)
  await page.getByRole('button', { name: 'Start auto run' }).click()

  // The card replacing the ticket is what "a run is live" looks like.
  await expect(page.getByRole('button', { name: 'Stop run' })).toBeVisible({ timeout: 30_000 })
}

/** How many contracts the live run has placed, read off the card. */
export async function placedCount(page: Page): Promise<number> {
  const figure = page.locator('dt', { hasText: 'Placed' }).locator('..')
  const text = await figure.innerText()
  return Number(text.replace(/[^\d/]/g, '').split('/')[0] || 0)
}
