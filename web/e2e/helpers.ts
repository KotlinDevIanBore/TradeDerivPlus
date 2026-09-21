import { expect, type Page } from '@playwright/test'

export const API = process.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'

/** A fresh Safaricom-shaped number, so each run gets its own user. */
export function newPhone(): string {
  return `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
}

export const PASSWORD = 'hunter2024'

/**
 * Navigates using whichever chrome the current viewport actually shows -- the
 * bottom bar on a phone, the top bar on a desktop. These specs run in both
 * projects, and they care about arriving, not about which bar got them there.
 *
 * The two do not agree on names: the top bar already calls its destinations
 * what they are becoming (Positions, History), while the bottom bar still says
 * Portfolio and Wallet. Home has no top-bar entry at all -- the logo is the
 * way back, as it will be when Trade becomes the app's surface.
 */
const WIDE_LABELS: Record<string, string> = {
  Trade: 'Trade',
  Wallet: 'History',
  Signals: 'Signals',
}

export async function goTo(page: Page, destination: string) {
  const wide = (page.viewportSize()?.width ?? 0) >= 1024

  if (wide && destination === 'Home') {
    await page.getByRole('link', { name: 'TradeDerivPlus home' }).click()
    return
  }

  const label = wide ? (WIDE_LABELS[destination] ?? destination) : destination
  await page.locator('nav:visible').getByRole('link', { name: label, exact: true }).click()
}

function isWide(page: Page) {
  return (page.viewportSize()?.width ?? 0) >= 1024
}

/**
 * The Positions panel, whichever container it is in: a column in the page on a
 * desktop, a modal sheet on a phone. Specs care that the positions are there,
 * not which of the two is rendering them.
 */
export function positionsPanel(page: Page) {
  return page
    .getByRole('complementary', { name: 'Positions' })
    .or(page.getByRole('dialog', { name: 'Positions' }))
}

/**
 * Opens the panel however this viewport opens it, and leaves it open.
 *
 * On a desktop the control is a toggle, and a one-shot "is it open?" check
 * raced the render that follows a route change: probe too early, decide it is
 * closed, and the click that follows shuts a panel that had just opened. So
 * the whole open-and-check retries until the panel is actually there.
 */
export async function openPositions(page: Page) {
  const panel = positionsPanel(page)

  await expect(async () => {
    if (!(await panel.isVisible())) {
      if (isWide(page)) await page.getByRole('button', { name: 'Positions' }).click()
      else await page.locator('nav:visible').getByRole('link', { name: 'Positions' }).click()
    }
    await expect(panel).toBeVisible({ timeout: 2_000 })
  }).toPass({ timeout: 20_000 })
}

export async function closePositions(page: Page) {
  // Exact: "Close Positions" would otherwise also answer to a search for the
  // "Close position" button on a card, which is a different thing entirely.
  await page.getByRole('button', { name: 'Close Positions', exact: true }).click()
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
  await page.waitForURL('**/')
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
