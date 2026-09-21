// Screenshot runner for the implementation reports. Not a test -- Playwright
// only collects *.spec.ts, so this never runs with the suite.
// Usage: node e2e/shots.mjs <out-dir> [--drawer] [--panel] [--trade-only]
import { chromium, devices } from '@playwright/test'
import { mkdirSync } from 'node:fs'

const out = process.argv[2] ?? '/tmp/shots'
const flag = (name) => process.argv.includes(`--${name}`)
mkdirSync(out, { recursive: true })

const PASSWORD = 'hunter2024'

/** A fresh user per context: the same number cannot register twice. */
async function register(page) {
  const phone = `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
  await page.goto('http://localhost:5173/register')
  await page.getByLabel('Full name').fill('Shot User')
  await page.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
  await page.getByLabel('Password', { exact: true }).fill(PASSWORD)
  await page.getByLabel('Confirm password').fill(PASSWORD)
  await page.getByRole('checkbox').check()
  // No baseURL in a standalone script, so wait on the landing content rather
  // than a glob that would be resolved against one.
  await page.getByRole('button', { name: 'Create account' }).click()
  await page.locator('section[aria-label="Balance"]').waitFor({ timeout: 30_000 })
}

const browser = await chromium.launch()

const viewports = [
  ['desktop', { viewport: { width: 1280, height: 900 } }],
  ['wide', { viewport: { width: 1920, height: 1000 } }],
  ['mobile', devices['Pixel 7']],
  ['narrow', { viewport: { width: 360, height: 780 }, isMobile: true, hasTouch: true }],
]

for (const [name, opts] of viewports) {
  const context = await browser.newContext(opts)
  const page = await context.newPage()
  await register(page)

  if (!flag('trade-only')) {
    await page.waitForTimeout(1500)
    await page.screenshot({ path: `${out}/${name}-home.png` })
  }

  await page.goto('http://localhost:5173/trade')
  await page.waitForTimeout(4000)
  await page.screenshot({ path: `${out}/${name}-trade.png` })

  if (flag('panel')) {
    await page.goto('http://localhost:5173/trade?panel=positions')
    await page.waitForTimeout(3000)
    await page.screenshot({ path: `${out}/${name}-panel.png` })
  }

  if (flag('drawer') && name !== 'desktop') {
    const menu = page.getByRole('button', { name: 'Open menu' })
    if (await menu.count()) {
      await menu.click()
      await page.waitForTimeout(600)
      await page.screenshot({ path: `${out}/${name}-drawer.png` })
    }
  }

  await context.close()
}

await browser.close()
console.log('wrote', out)
