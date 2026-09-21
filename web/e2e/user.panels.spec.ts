import { expect, test } from '@playwright/test'
import {
  closePositions,
  newPhone,
  openPositions,
  positionsPanel,
  register,
} from './helpers'

test.describe('The Positions panel', () => {
  test('arrives from an old link with its tab, and closes on back', async ({ page }) => {
    await register(page, newPhone())

    // The route Portfolio used to live at is a link somebody may have sent,
    // with the tab that made the link worth sending.
    await page.goto('/portfolio?tab=closed')
    await page.waitForURL(/\/trade\?.*panel=positions/)
    expect(page.url()).toContain('tab=closed')

    const panel = positionsPanel(page)
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Closed' })).toHaveAttribute(
      'data-state',
      'active',
    )

    await closePositions(page)
    await expect(panel).toBeHidden()

    // Opening is a history entry of its own, so back is the way out of a panel.
    await openPositions(page)
    await expect(panel).toBeVisible()
    await page.goBack()
    await expect(panel).toBeHidden()
  })

  test('leaves the feed alone however often it is opened and closed', async ({ page }) => {
    await register(page, newPhone())
    await page.goto('/trade')

    const ticket = page.getByRole('button', { name: /Buy R_75 for/ })
    await expect(ticket).toBeEnabled({ timeout: 30_000 })

    // The panel and the chart both want prices. Before the shell owned the
    // socket, the second one to let go took the feed down for both.
    for (let i = 0; i < 3; i++) {
      await openPositions(page)
      await expect(positionsPanel(page)).toBeVisible()
      await closePositions(page)
      await expect(positionsPanel(page)).toBeHidden()
    }

    await expect(page.getByText('Market feed offline')).toHaveCount(0)
    await expect(ticket).toBeEnabled()
  })

  test('keeps the close confirmation on top of the panel it was opened from', async ({
    page,
  }) => {
    await register(page, newPhone())
    await page.goto('/trade')

    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({
      timeout: 30_000,
    })
    await page.getByRole('button', { name: /Buy R_75 for/ }).click()
    await expect(page.getByText('Position opened')).toBeVisible()

    await openPositions(page)
    const panel = positionsPanel(page)

    // The chart and the panel want the same symbol priced at the same time.
    // A signed figure on the card means the panel is being fed, not just open.
    await expect(panel.getByText(/^[+-]\$\d/).first()).toBeVisible({ timeout: 15_000 })

    await panel.getByRole('button', { name: 'Close position', exact: true }).first().click()

    // A dialog over a panel: on a phone that is a dialog over a dialog, which
    // nothing in the app had done before.
    const confirm = page.getByRole('dialog', { name: 'Close position' })
    await expect(confirm).toBeVisible()
    await expect(confirm.getByText(/^Close R_75/)).toBeVisible()

    // Escape dismisses the thing on top, and only that thing.
    await page.keyboard.press('Escape')
    await expect(confirm).toBeHidden()
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Open' })).toBeVisible()

    // A second Escape then does what that breakpoint's panel is: the phone's
    // is a modal and closes, the desktop's is part of the page and stays.
    await page.keyboard.press('Escape')
    const wide = (page.viewportSize()?.width ?? 0) >= 1024
    if (wide) await expect(panel).toBeVisible()
    else await expect(panel).toBeHidden()
  })
})
