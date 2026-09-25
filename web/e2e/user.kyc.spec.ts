import { expect, test } from '@playwright/test'
import { adminToken, api, newPhone, openPanel, register } from './helpers'

/**
 * Self-serve identity verification, end to end.
 *
 * This is the flow that did not exist: before it, no user could ever reach
 * `verified`, so the withdrawal gate refused everyone and the feature flag was
 * not what was blocking withdrawals.
 *
 * The upload goes to the object store, not through the API. With no bucket
 * configured the server signs URLs at its own development upload route, which is
 * what makes this runnable in CI with no credentials — and what makes the test
 * exercise the real three-step flow rather than a shortcut.
 */

/** A minimal but genuine JPEG header, so the server's content sniff accepts it. */
function jpeg(): Buffer {
  const head = Buffer.from([0xff, 0xd8, 0xff, 0xe0, 0x00, 0x10, 0x4a, 0x46, 0x49, 0x46])
  return Buffer.concat([head, Buffer.alloc(512)])
}

/** An HTML file, for the case where the label lies about the bytes. */
function htmlPretendingToBeAJpeg(): Buffer {
  return Buffer.from('<html><body>not an identity document</body></html>')
}

test.describe('Verifying your identity', () => {
  test('uploads a document, an admin approves it, and withdrawals unlock', async ({ page }) => {
    const phone = newPhone()
    await register(page, phone, 'KYC E2E')

    await openPanel(page, 'Account')
    await expect(page.getByText('Verify your identity to enable withdrawals.')).toBeVisible()
    await expect(page.getByRole('button', { name: 'Start verification' })).toBeVisible()

    await page.getByRole('button', { name: 'Start verification' }).click()
    // The field, not the title: "Verify your identity" also appears in the
    // account panel's own copy behind the sheet.
    await expect(page.getByLabel('ID number')).toBeVisible()

    await page.getByLabel('ID number').fill('21234567')
    await page.setInputFiles('#kyc-document', {
      name: 'id.jpg',
      mimeType: 'image/jpeg',
      buffer: jpeg(),
    })
    // The picked file is named back to the user before they commit to sending it.
    await expect(page.getByText('id.jpg', { exact: false })).toBeVisible()

    await page.getByRole('button', { name: 'Send for review' }).click()

    // The copy says what happens, not when: nothing here can promise when a
    // person will look.
    await expect(page.getByText('We have your document')).toBeVisible({ timeout: 15_000 })
    await expect(page.getByText(/Someone will review it/)).toBeVisible()
    await page.getByRole('button', { name: 'Done' }).click()

    // Under review there is nothing to press, so no control is offered.
    await expect(page.getByText('Your documents are with us. Someone will review them.')).toBeVisible()
    await expect(page.getByRole('button', { name: /verification/ })).toHaveCount(0)

    // An operator approves it, through the console's own endpoint.
    const token = await adminToken()
    const queue = await api('/admin/kyc?status=pending&limit=50', { token })
    const items = ((await queue.json()) as { items: Array<{ id: string; user_phone: string }> }).items
    // newPhone() gives the 07… form a human types; the API stores and returns the
    // normalised 2547… one.
    const normalised = `254${phone.replace(/^0/, '')}`
    const mine = items.find((k) => k.user_phone === normalised)
    expect(mine, 'the submission is not in the review queue').toBeTruthy()

    // The document is viewable, and viewing it is audited.
    const view = await api(`/admin/kyc/${mine!.id}/document-url`, { method: 'POST', token, body: {} })
    expect(view.ok, `document url failed: ${view.status}`).toBeTruthy()
    expect(((await view.json()) as { url: string }).url).toBeTruthy()

    const approved = await api(`/admin/kyc/${mine!.id}/approve`, { method: 'POST', token, body: {} })
    expect(approved.ok, `approve failed: ${approved.status}`).toBeTruthy()

    // The decision reaches the user over the account socket, so the screen
    // catches up without a reload.
    await expect(page.getByText('Your identity is verified. Withdrawals are enabled.')).toBeVisible({
      timeout: 15_000,
    })

    // And the audit log records the view alongside the approval.
    const log = await api('/admin/audit-log?action=kyc_document_view&limit=50', { token })
    const entries = ((await log.json()) as { items: Array<{ action: string }> }).items
    expect(entries.length, 'the document view was not audited').toBeGreaterThan(0)
  })

  test('refuses a file that is not what it says it is', async ({ page }) => {
    const phone = newPhone()
    await register(page, phone, 'KYC Sniff')

    await openPanel(page, 'Account')
    await page.getByRole('button', { name: 'Start verification' }).click()
    await page.getByLabel('ID number').fill('21234567')

    // The browser declares image/jpeg, so the signed upload succeeds — the
    // signature covers the header, not the bytes. Only the server's content
    // sniff can catch this, and it is the reason the sniff exists.
    await page.setInputFiles('#kyc-document', {
      name: 'id.jpg',
      mimeType: 'image/jpeg',
      buffer: htmlPretendingToBeAJpeg(),
    })
    await page.getByRole('button', { name: 'Send for review' }).click()

    await expect(page.getByText(/does not look like a JPEG, PNG or PDF/)).toBeVisible({
      timeout: 15_000,
    })
    await expect(page.getByText('Nothing was submitted. You can try again.')).toBeVisible()

    // The attempt stays open, so the user is invited to continue rather than
    // being silently reset to the beginning.
    // .last(): the sheet has its own dismiss control as well as this one.
    await page.getByRole('button', { name: 'Close' }).last().click()
    await expect(page.getByRole('button', { name: 'Continue verification' })).toBeVisible()
    await expect(page.getByText(/didn't finish verification/)).toBeVisible()
  })
})
