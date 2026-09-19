import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { TradeSymbol, Wallet } from '@/types'
import { OrderTicket } from './OrderTicket'

const symbol: TradeSymbol = {
  id: 's1',
  code: 'R_75',
  display_name: 'Volatility 75 Index',
  category: 'volatility',
  pip_size: 4,
  is_active: true,
}

const wallet: Wallet = {
  account_id: 'a1',
  real_balance: '100.00',
  bonus_balance: '50.00',
  total: '150.00',
  updated_at: new Date().toISOString(),
}

function renderTicket(overrides: Partial<React.ComponentProps<typeof OrderTicket>> = {}) {
  const onSubmit = vi.fn()
  render(
    <OrderTicket
      symbol={symbol}
      currentPrice="100.0000"
      wallet={wallet}
      disabled={false}
      submitting={false}
      error={null}
      onSubmit={onSubmit}
      isDemo={false}
      {...overrides}
    />,
  )
  return { onSubmit }
}

describe('OrderTicket', () => {
  it('labels the action with the direction, symbol and stake', () => {
    renderTicket()
    expect(screen.getByRole('button', { name: /buy r_75 for \$10\.00/i })).toBeInTheDocument()
  })

  it('follows the direction toggle into the label', async () => {
    const user = userEvent.setup()
    renderTicket()

    await user.click(screen.getByRole('button', { name: /^sell$/i }))
    expect(screen.getByRole('button', { name: /sell r_75 for/i })).toBeInTheDocument()
  })

  it('refuses a stake larger than the available balance', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderTicket()

    const stake = screen.getByLabelText(/stake/i)
    await user.clear(stake)
    await user.type(stake, '500')

    expect(await screen.findByText(/you have \$150\.00 available/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /buy r_75/i })).toBeDisabled()
    expect(onSubmit).not.toHaveBeenCalled()
  })

  it('sums real and bonus into the available balance', () => {
    renderTicket()
    expect(screen.getByText(/\$150\.00/)).toBeInTheDocument()
  })

  it('refuses a stop loss above the price on a buy, naming the field', async () => {
    const user = userEvent.setup()
    renderTicket()

    // Stop loss starts off; turn it on before it can be set.
    await user.click(screen.getByRole('button', { name: /set stop loss/i }))
    await user.type(screen.getByLabelText('Stop loss'), '150')

    expect(
      await screen.findByText(/stop loss must be below the current price for a buy/i),
    ).toBeInTheDocument()
  })

  it('accepts a stop loss below the price on a buy', async () => {
    const user = userEvent.setup()
    const { onSubmit } = renderTicket()

    await user.click(screen.getByRole('button', { name: /set stop loss/i }))
    await user.type(screen.getByLabelText('Stop loss'), '95')

    expect(
      screen.queryByText(/stop loss must be below the current price/i),
    ).not.toBeInTheDocument()

    await user.click(screen.getByRole('button', { name: /buy r_75/i }))
    expect(onSubmit).toHaveBeenCalledWith(
      expect.objectContaining({ direction: 'buy', stopLoss: '95' }),
    )
  })

  it('disables trading and says why when the price feed is stale', () => {
    renderTicket({ disabled: true, disabledReason: 'Waiting for a live price before you can trade' })

    expect(screen.getByText(/waiting for a live price/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /buy r_75/i })).toBeDisabled()
  })

  it('warns when the levels came from a signal that has aged out', () => {
    renderTicket({ expiredSignal: true })
    expect(screen.getByText(/that signal has aged/i)).toBeInTheDocument()
  })

  it('marks demo funds as practice money', () => {
    renderTicket({ isDemo: true })
    expect(screen.getByText(/practice funds/i)).toBeInTheDocument()
  })

  it('names each level field for a screen reader', async () => {
    const user = userEvent.setup()
    renderTicket()

    // The visible labels sit beside the toggles rather than wrapping the
    // inputs, so this asserts they are still associated with them.
    await user.click(screen.getByRole('button', { name: /set stop loss/i }))
    await user.click(screen.getByRole('button', { name: /set take profit/i }))

    expect(screen.getByLabelText('Stop loss')).toBeInTheDocument()
    expect(screen.getByLabelText('Take profit')).toBeInTheDocument()
  })
})
