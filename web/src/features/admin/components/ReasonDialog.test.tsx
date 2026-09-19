import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import { ReasonDialog } from './ReasonDialog'

function renderDialog(overrides: Partial<React.ComponentProps<typeof ReasonDialog>> = {}) {
  const onConfirm = vi.fn()
  const onOpenChange = vi.fn()
  render(
    <ReasonDialog
      open
      onOpenChange={onOpenChange}
      title="Freeze account 900002?"
      consequence="The user will not be able to trade, deposit, or withdraw."
      confirmLabel="Freeze account"
      onConfirm={onConfirm}
      {...overrides}
    />,
  )
  return { onConfirm, onOpenChange }
}

describe('ReasonDialog', () => {
  it('states the consequence in the dialog itself', () => {
    renderDialog()
    expect(screen.getByText(/will not be able to trade/i)).toBeInTheDocument()
  })

  it('blocks confirmation until a reason worth logging is written', async () => {
    const user = userEvent.setup()
    const { onConfirm } = renderDialog()

    const confirm = screen.getByRole('button', { name: /freeze account/i })
    expect(confirm).toBeDisabled()

    await user.type(screen.getByLabelText(/reason/i), 'too short')
    expect(confirm).toBeDisabled()
    expect(onConfirm).not.toHaveBeenCalled()

    await user.type(screen.getByLabelText(/reason/i), ' but now it is long enough')
    expect(confirm).toBeEnabled()
  })

  it('counts down the characters still needed', async () => {
    const user = userEvent.setup()
    renderDialog()

    await user.type(screen.getByLabelText(/reason/i), 'abc')
    expect(screen.getByText(/7 more characters/i)).toBeInTheDocument()
  })

  it('passes the trimmed reason to the caller', async () => {
    const user = userEvent.setup()
    const { onConfirm } = renderDialog()

    await user.type(screen.getByLabelText(/reason/i), '  Suspected duplicate signup  ')
    await user.click(screen.getByRole('button', { name: /freeze account/i }))

    expect(onConfirm).toHaveBeenCalledWith('Suspected duplicate signup')
  })

  it('allows confirming without a reason when one is not required', async () => {
    const user = userEvent.setup()
    const { onConfirm } = renderDialog({ reasonRequired: false, confirmLabel: 'Approve' })

    await user.click(screen.getByRole('button', { name: /approve/i }))
    expect(onConfirm).toHaveBeenCalledWith('')
  })

  it('shows a server-side reason error under the field', () => {
    renderDialog({
      error: new ApiError(422, {
        message: 'Give a reason of at least 10 characters',
        code: 'validation_failed',
        field: 'reason',
      }),
    })
    expect(screen.getByText(/give a reason of at least 10 characters/i)).toBeInTheDocument()
  })

  it('shows a non-field error as a banner instead', () => {
    renderDialog({
      error: new ApiError(400, { message: 'Insufficient balance', code: 'insufficient_funds' }),
    })
    expect(screen.getByRole('alert')).toHaveTextContent(/insufficient balance/i)
  })

  it('can be dismissed without acting', async () => {
    const user = userEvent.setup()
    const { onConfirm, onOpenChange } = renderDialog()

    await user.click(screen.getByRole('button', { name: /cancel/i }))
    expect(onOpenChange).toHaveBeenCalledWith(false)
    expect(onConfirm).not.toHaveBeenCalled()
  })
})
