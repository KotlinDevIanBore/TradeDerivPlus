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

/** Registers through the UI, which is also how the signup path gets covered. */
export async function register(page: Page, phone: string, name = 'E2E User') {
  await page.goto('/register')

  await page.getByLabel('Full name').fill(name)
  await page.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByLabel('Confirm password').fill(PASSWORD)
  await page.getByRole('checkbox').check()

  await page.getByRole('button', { name: 'Create account' }).click()

  // Signup lands on the surface, which is the app. There is no Home to check
  // for any more, so the pill in the top bar is the landing: a demo account,
  // funded, without a second round trip.
  await page.waitForURL('**/')
  await expect(accountPill(page)).toContainText('demo')
  await expect(accountPill(page)).toContainText('$10,000.00')
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
    user: { id: string }
    account: { id: string; account_number: string }
    access_token: string
  }
}
