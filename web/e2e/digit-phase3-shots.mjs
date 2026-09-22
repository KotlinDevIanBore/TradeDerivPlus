// Digit Phase 3: toast + panel walkthrough screenshots.
// Usage: node e2e/digit-phase3-shots.mjs <out-dir>
import { chromium } from '@playwright/test'
import { mkdirSync } from 'node:fs'

const out = process.argv[2] ?? 'docs/implementation-reports/assets/digit-phase-3'
mkdirSync(out, { recursive: true })

const PASSWORD = 'hunter2024'
const BASE = 'http://localhost:5173'
const API = 'http://localhost:8080/api/v1'

async function register(page) {
  const phone = `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
  await page.goto(`${BASE}/register`)
  await page.getByLabel('Full name').fill('Digit Phase3')
  await page.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByLabel('Confirm password').fill(PASSWORD)
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: 'Create account' }).click()
  await page.getByRole('button', { name: 'Switch account' }).waitFor({ timeout: 30_000 })
  return phone
}

async function waitForDigitSurface(page) {
  // Flag polls every 10s; wait for Even button (ticket) which CFD ticket lacks.
  await page.getByRole('button', { name: 'Even' }).waitFor({ timeout: 45_000 })
}

async function shotToast(page, name) {
  const toast = page.getByRole('status')
  await toast.waitFor({ state: 'visible', timeout: 15_000 })
  await page.waitForTimeout(300)
  await page.screenshot({ path: `${out}/${name}.png`, fullPage: false })
  const text = await toast.innerText()
  console.log(name, text.replace(/\n/g, ' | '))
  return text
}

const browser = await chromium.launch()
const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })

await register(page)
await waitForDigitSurface(page)
await page.screenshot({ path: `${out}/digit-surface.png`, fullPage: false })
console.log('digit-surface ok')

// Even — settle toast (win or loss)
await page.getByRole('button', { name: 'Even' }).click()
const openToast = await shotToast(page, 'toast-opened')
// Wait for settlement toast (second toast after open)
await page.waitForTimeout(500)
await page.getByRole('status').waitFor({ state: 'visible', timeout: 10_000 }).catch(() => {})
// Dismissal of open toast then settle toast
for (let i = 0; i < 20; i++) {
  const t = await page.getByRole('status').innerText().catch(() => '')
  if (/\bwonn?\b|\blost\b/i.test(t)) {
    await page.screenshot({ path: `${out}/toast-settled.png`, fullPage: false })
    console.log('toast-settled', t.replace(/\n/g, ' | '))
    break
  }
  await page.waitForTimeout(400)
}

// Positions while we open Match (brief window) — open Odd then immediately open Positions
await page.getByRole('button', { name: 'Odd' }).click()
await page.waitForTimeout(200)
await page.getByRole('banner').getByRole('button', { name: 'Positions', exact: true }).click()
await page.waitForTimeout(600)
await page.screenshot({ path: `${out}/positions-pending-or-empty.png`, fullPage: false })
console.log('positions shot')

// Wait for settle, then History
await page.waitForTimeout(2500)
await page.getByRole('banner').getByRole('button', { name: 'History', exact: true }).click()
await page.waitForTimeout(800)
await page.screenshot({ path: `${out}/history-settled.png`, fullPage: false })
console.log('history shot')

// Session strip on surface
await page.goto(BASE)
await waitForDigitSurface(page)
await page.waitForTimeout(500)
await page.screenshot({ path: `${out}/session-strip.png`, fullPage: false })
console.log('session-strip shot')

// Open Match and Over for lifecycle coverage
await page.getByRole('tab', { name: 'Matches/Differs' }).click()
await page.getByRole('button', { name: 'Match' }).click()
await page.waitForTimeout(2800)
await page.getByRole('tab', { name: 'Over/Under' }).click()
await page.getByRole('button', { name: 'Over' }).click()
await page.waitForTimeout(2800)

await page.getByRole('banner').getByRole('button', { name: 'History', exact: true }).click()
await page.waitForTimeout(800)
await page.screenshot({ path: `${out}/history-three-types.png`, fullPage: false })

console.log('done', out)
await browser.close()
