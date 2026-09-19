import type { Page } from '@playwright/test'

export const API = process.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'

/** A fresh Safaricom-shaped number, so each run gets its own user. */
export function newPhone(): string {
  return `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
}

export const PASSWORD = 'hunter2024'

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
