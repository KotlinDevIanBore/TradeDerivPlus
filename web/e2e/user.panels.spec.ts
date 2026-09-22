import { expect, test } from '@playwright/test'
import {
  beginClosePosition,
  closePanel,
  isWide,
  newPhone,
  openPanel,
  panelOf,
  register,
} from './helpers'

test.describe('The Positions panel', () => {
  test('arrives from an old link with its tab, and closes on back', async ({ page }) => {
    await register(page, newPhone())

    // The route Portfolio used to live at is a link somebody may have sent,
    // with the tab that made the link worth sending.
    await page.goto('/portfolio?tab=closed')
    await page.waitForURL(/\/\?.*panel=positions/)
    expect(page.url()).toContain('tab=closed')

    const panel = panelOf(page, 'Positions')
    await expect(panel).toBeVisible()
    await expect(panel.getByRole('tab', { name: 'Closed' })).toHaveAttribute(
      'data-state',
      'active',
    )

    await closePanel(page, 'Positions')
    await expect(panel).toBeHidden()

    // Opening is a history entry of its own, so back is the way out of a panel.
    await openPanel(page, 'Positions')
    await expect(panel).toBeVisible()
    await page.goBack()
    await expect(panel).toBeHidden()
  })

  test('leaves the feed alone however often it is opened and closed', async ({ page }) => {
    await register(page, newPhone())

    const ticket = page.getByRole('button', { name: /Buy R_75 for/ })
    await expect(ticket).toBeEnabled({ timeout: 30_000 })

    // The panel and the chart both want prices. Before the shell owned the
    // socket, the second one to let go took the feed down for both.
    for (let i = 0; i < 3; i++) {
      await openPanel(page, 'Positions')
      await expect(panelOf(page, 'Positions')).toBeVisible()
      await closePanel(page, 'Positions')
      await expect(panelOf(page, 'Positions')).toBeHidden()
    }

    await expect(page.getByText('Market feed offline')).toHaveCount(0)
    await expect(ticket).toBeEnabled()
  })

  test('keeps the close confirmation on top of the panel it was opened from', async ({
    page,
  }) => {
    await register(page, newPhone())

    await expect(page.getByRole('button', { name: /Buy R_75 for/ })).toBeEnabled({
      timeout: 30_000,
    })
    await page.getByRole('button', { name: /Buy R_75 for/ }).click()
    await expect(page.getByText('Position opened')).toBeVisible()

    await openPanel(page, 'Positions')
    const panel = panelOf(page, 'Positions')

    // The chart and the panel want the same symbol priced at the same time.
    // A signed figure on the card means the panel is being fed, not just open.
    await expect(panel.getByText(/^[+-]\$\d/).first()).toBeVisible({ timeout: 15_000 })

    // A dialog over a panel: on a phone that is a dialog over a dialog, which
    // nothing in the app had done before.
    await beginClosePosition(page)
    const confirm = page.getByRole('dialog', { name: 'Close position' })
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

test.describe('The routes that used to be screens', () => {
  test('all land on the surface with what they were carrying', async ({ page }) => {
    await register(page, newPhone())

    // The surface's own old route, with everything a signal link puts on it.
    // These parameters are the reason the redirect forwards the query string
    // whole rather than by name.
    await page.goto('/trade?symbol=R_100&direction=sell&sl=1.5&tp=2.5&expired=1')
    await page.waitForURL(/\/\?symbol=R_100/)

    const params = new URL(page.url()).searchParams
    expect(params.get('direction')).toBe('sell')
    expect(params.get('sl')).toBe('1.5')
    expect(params.get('tp')).toBe('2.5')
    expect(params.get('expired')).toBe('1')

    // And the surface read them rather than merely keeping them in the bar:
    // the ticket is priced on the symbol the link named, in the direction it
    // named.
    await expect(page.getByRole('button', { name: /Sell R_100 for/ })).toBeVisible({
      timeout: 30_000,
    })

    for (const [path, name] of [
      ['/portfolio', 'Positions'],
      ['/wallet', 'History'],
      ['/signals', 'Signals'],
      ['/profile', 'Account'],
    ]) {
      // A link somebody sent, or a bookmark from before the move.
      await page.goto(path)
      await page.waitForURL(new RegExp(`/\\?.*panel=${name.toLowerCase()}`))
      await expect(panelOf(page, name)).toBeVisible()
      await closePanel(page, name)
      await expect(panelOf(page, name)).toBeHidden()

      // And the way a user without the link gets there: the top bar on a
      // desktop, the bottom bar on a phone. Both are toggles now.
      await openPanel(page, name)
      await expect(panelOf(page, name)).toBeVisible()
      await closePanel(page, name)
    }
  })

  test('anything else falls back to the surface', async ({ page }) => {
    await register(page, newPhone())

    await page.goto('/home')
    await page.waitForURL(/\/$/)
    await expect(page.getByRole('button', { name: /(Buy|Sell) R_75 for/ })).toBeVisible({
      timeout: 30_000,
    })
  })
})

test.describe('Getting around on a phone', () => {
  test('the bottom bar carries the panels and the drawer carries the rest', async ({ page }) => {
    test.skip(isWide(page), 'the bottom bar and the drawer are the phone’s chrome')

    await register(page, newPhone())

    // Four toggles, no destinations: there is nowhere to go.
    const bar = page.getByRole('navigation', { name: 'Bottom' })
    await expect(bar.getByRole('link')).toHaveCount(0)
    for (const name of ['Positions', 'History', 'Signals', 'Account']) {
      await expect(bar.getByRole('button', { name, exact: true })).toHaveAttribute(
        'aria-pressed',
        'false',
      )
    }

    // Tapping one opens it, and tapping it again is the way back to the chart.
    await openPanel(page, 'Signals')
    await closePanel(page, 'Signals')
    await expect(panelOf(page, 'Signals')).toBeHidden()

    // The drawer is now the three things that are not panels, and offers no
    // second route to the four that are.
    await page.getByRole('button', { name: 'Open menu' }).click()
    const menu = page.getByRole('dialog', { name: 'Menu' })
    await expect(menu.getByRole('button', { name: 'Deposit' })).toBeVisible()
    await expect(menu.getByRole('button', { name: 'Withdraw' })).toBeVisible()
    await expect(menu.getByRole('button', { name: 'Log out' })).toBeVisible()
    await expect(menu.getByRole('link')).toHaveCount(0)
  })
})

test.describe('The History panel', () => {
  test('arrives from the old Wallet link and opens a transaction over itself', async ({
    page,
  }) => {
    await register(page, newPhone())

    await page.goto('/wallet')
    await page.waitForURL(/\/\?.*panel=history/)

    const history = panelOf(page, 'History')
    await expect(history).toBeVisible()
    await expect(history.getByRole('region', { name: 'Balance' })).toBeVisible()

    // A fresh demo account has exactly one movement: the money it started with.
    const row = history.getByRole('listitem').first().getByRole('button')
    await expect(row).toContainText('Demo account starting balance')
    await row.click()

    // The same shape as the close confirmation over Positions: a sheet over a
    // panel, which on a phone is a dialog over a dialog.
    const detail = page.getByRole('dialog', { name: 'Transaction' })
    await expect(detail).toBeVisible()
    await expect(detail.getByText('Reference', { exact: true })).toBeVisible()

    // Escape takes the top thing only, and hands focus back to the row.
    await page.keyboard.press('Escape')
    await expect(detail).toBeHidden()
    await expect(history).toBeVisible()
    await expect(row).toBeFocused()

    // A second Escape then does what that breakpoint's panel is: the phone's
    // is a modal and closes, the desktop's is part of the page and stays.
    await page.keyboard.press('Escape')
    const wide = (page.viewportSize()?.width ?? 0) >= 1024
    if (wide) await expect(history).toBeVisible()
    else await expect(history).toBeHidden()
  })

  test('closing it on a desktop leaves Positions where it was', async ({ page }) => {
    test.skip((page.viewportSize()?.width ?? 0) < 1024, 'desktop has a default panel; a phone does not')

    await register(page, newPhone())
    await expect(panelOf(page, 'Positions')).toBeVisible()

    await openPanel(page, 'History')
    await expect(panelOf(page, 'Positions')).toBeHidden()

    // Closing a panel returns to the default rather than to an empty column.
    await closePanel(page, 'History')
    await expect(panelOf(page, 'History')).toBeHidden()
    await expect(panelOf(page, 'Positions')).toBeVisible()
  })
})
