// Throwaway probe: the "N new signals" pill depends on the panel's scroll
// position and on a push arriving. Run the API with SIGNAL_INTERVAL=3s so the
// wait is seconds rather than minutes. Not part of the suite.
import { chromium, devices } from '@playwright/test'

const BASE = process.env.BASE ?? 'http://localhost:5173'
const phone = () => `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`

for (const [label, ctx] of [
  ['desktop', { viewport: { width: 1440, height: 900 } }],
  ['mobile', devices['Pixel 7']],
]) {
  const browser = await chromium.launch()
  const page = await browser.newPage(ctx)

  await page.goto(`${BASE}/register`)
  await page.getByLabel('Full name').fill('Pill Probe')
  await page.getByLabel('Phone number').fill(phone().replace(/^0/, ''))
  await page.getByLabel('Password', { exact: true }).fill('hunter2024')
  await page.getByLabel('Confirm password').fill('hunter2024')
  await page.getByRole('checkbox').check()
  await page.getByRole('button', { name: 'Create account' }).click()
  await page.locator('section[aria-label="Balance"]').waitFor({ timeout: 30_000 })

  await page.goto(`${BASE}/trade?panel=signals`)
  const panel = page
    .getByRole('complementary', { name: 'Signals' })
    .or(page.getByRole('dialog', { name: 'Signals' }))
  await panel.waitFor()
  await page.waitForTimeout(2000)

  // The panel's own scroll container, found the way the component finds it.
  const scroller = panel.locator('div').filter({ hasText: /All/ }).first()
  const box = await panel.evaluate((el) => {
    const node = [...el.querySelectorAll('div')].find((d) => d.scrollHeight > d.clientHeight + 40)
    if (!node) return null
    node.scrollTop = 500
    return { scrollTop: node.scrollTop, height: node.scrollHeight }
  })
  console.log(label, 'scrolled to', box)
  void scroller

  const pill = panel.getByRole('button', { name: /new signal/ })
  try {
    await pill.waitFor({ timeout: 40_000 })
    console.log(label, 'pill:', await pill.textContent())

    await pill.click()
    await page.waitForTimeout(1200)
    const after = await panel.evaluate(
      (el) =>
        [...el.querySelectorAll('div')].find((d) => d.scrollHeight > d.clientHeight + 40)
          ?.scrollTop,
    )
    console.log(label, 'scrollTop after clicking the pill:', after)
    console.log(label, 'pill still there:', await pill.isVisible())
  } catch {
    console.log(label, 'NO PILL within 40s')
  }

  await browser.close()
}
