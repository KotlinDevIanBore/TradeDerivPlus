// Measures the rendered chart width at a few desktop widths, which is the
// number the layout decisions in the migration plan turn on. Not a test.
// Usage: node e2e/measure.mjs [--panel]
import { chromium } from '@playwright/test'

const WIDTHS = [1280, 1440, 1920, 2560]
const withPanel = process.argv.includes('--panel')

const browser = await chromium.launch()
const context = await browser.newContext({ viewport: { width: WIDTHS[0], height: 900 } })
const page = await context.newPage()

const phone = `07${Math.floor(10_000_000 + Math.random() * 89_999_999)}`
await page.goto('http://localhost:5173/register')
await page.getByLabel('Full name').fill('Measure User')
await page.getByLabel('Phone number').fill(phone.replace(/^0/, ''))
await page.getByLabel('Password', { exact: true }).fill('hunter2024')
await page.getByLabel('Confirm password').fill('hunter2024')
await page.getByRole('checkbox').check()
await page.getByRole('button', { name: 'Create account' }).click()
await page.getByRole('button', { name: 'Switch account' }).waitFor({ timeout: 30_000 })

for (const width of WIDTHS) {
  await page.setViewportSize({ width, height: 900 })
  await page.goto(`http://localhost:5173/${withPanel ? '?panel=positions' : '?panel=none'}`)
  await page.locator('.recharts-wrapper').waitFor({ timeout: 30_000 })
  await page.waitForTimeout(1200)

  const measured = await page.evaluate(() => {
    const round = (n) => Math.round(n)
    const chart = document.querySelector('.recharts-wrapper')
    const panel = document.querySelector('aside[aria-label="Positions"]')
    const ticket = [...document.querySelectorAll('div')].find((d) => String(d.className).includes('rounded-t-3xl'))
    return {
      chart: chart ? round(chart.getBoundingClientRect().width) : null,
      panel: panel ? round(panel.getBoundingClientRect().width) : 0,
      ticket: ticket ? round(ticket.getBoundingClientRect().width) : null,
    }
  })

  console.log(
    `${width}px viewport  panel ${String(measured.panel).padStart(4)}  ` +
      `chart ${String(measured.chart).padStart(4)}  ticket ${measured.ticket}`,
  )
}

// And the phone, vertically: with Home gone the surface is the landing, so
// what matters is whether the chart and the ticket fit in one screenful.
await page.setViewportSize({ width: 412, height: 915 })
await page.goto('http://localhost:5173/')
await page.locator('.recharts-wrapper').waitFor({ timeout: 30_000 })
await page.waitForTimeout(1200)

const tall = await page.evaluate(() => {
  const round = (n) => Math.round(n)
  const box = (el) => (el ? round(el.getBoundingClientRect().height) : null)
  const chart = document.querySelector('.recharts-wrapper')
  const ticket = [...document.querySelectorAll('div')].find((d) => String(d.className).includes('rounded-t-3xl'))
  return {
    viewport: window.innerHeight,
    document: round(document.documentElement.scrollHeight),
    topbar: box(document.querySelector('header')),
    chart: box(chart),
    chartBottom: chart ? round(chart.getBoundingClientRect().bottom) : null,
    ticketTop: ticket ? round(ticket.getBoundingClientRect().top) : null,
    tabbar: box(document.querySelector('nav[aria-label="Bottom"]')),
  }
})

console.log(
  `\n412x915 phone  viewport ${tall.viewport}  document ${tall.document}  ` +
    `topbar ${tall.topbar}  chart ${tall.chart}  tabbar ${tall.tabbar}\n` +
    `               chart ends at ${tall.chartBottom}, ticket starts at ${tall.ticketTop}, ` +
    `so ${tall.ticketTop - tall.chartBottom}px between them`,
)

await browser.close()
