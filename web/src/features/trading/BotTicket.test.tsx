import { render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { ApiError } from '@/api/client'
import type { DigitQuote, TradeSymbol, Wallet } from '@/types'
import { BotTicket } from './BotTicket'

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
  real_balance: '0.00',
  bonus_balance: '10000.00',
  total: '10000.00',
  updated_at: new Date().toISOString(),
}

/** Even/Odd at a $10 stake: the numbers the API actually returns for it. */
const evenOddQuote: DigitQuote = {
  contract_type: 'even_odd',
  selection: { parity: 'even' },
  stake: '10',
  payout_ratio: '0.952',
  potential_payout: '19.52',
  win_probability: '0.5',
  expected_return: '-0.024',
}

/** Matches — joint worst on the table at −4.5%, alongside Over 8 and Under 1. */
const matchesQuote: DigitQuote = {
  contract_type: 'matches_differs',
  selection: { mode: 'match', digit: 3 },
  stake: '10',
  payout_ratio: '8',
  potential_payout: '90.00',
  win_probability: '0.1',
  expected_return: '-0.045',
}

function renderTicket(overrides: Partial<React.ComponentProps<typeof BotTicket>> = {}) {
  const onSubmit = vi.fn()
  render(
    <BotTicket
      contractType="even_odd"
      symbol={symbol}
      wallet={wallet}
      quote={evenOddQuote}
      maxStake="100"
      disabled={false}
      submitting={false}
      error={null}
      onSubmit={onSubmit}
      {...overrides}
    />,
  )
  return { onSubmit }
}

describe('BotTicket', () => {
  describe('the economics it states', () => {
    it('shows what a win pays and how likely it is', () => {
      renderTicket()
      expect(screen.getByText(/\$19\.52/)).toBeInTheDocument()
      expect(screen.getByText(/50%/)).toBeInTheDocument()
    })

    // The figure this product exists to be honest about: every contract loses
    // money on average, and the ticket says so before the run starts.
    it('states the expected return per contract, negative and in full', () => {
      renderTicket()
      expect(screen.getByText(/Expected return -2\.40% per contract/)).toBeInTheDocument()
    })

    it('warns on the contract that costs the most', () => {
      renderTicket({ contractType: 'matches_differs', quote: matchesQuote })
      expect(screen.getByText(/Expected return -4\.50% per contract/)).toBeInTheDocument()
      expect(screen.getByText('This contract costs the most per play.')).toBeInTheDocument()
    })

    it('does not warn on the ordinary ones', () => {
      renderTicket()
      expect(screen.queryByText('This contract costs the most per play.')).not.toBeInTheDocument()
    })

    // The stop loss is not the worst case: the contract already in flight when it
    // trips still has to settle.
    it('states a worst case of the stop loss plus one stake', () => {
      renderTicket()
      expect(screen.getByText(/\$60\.00/)).toBeInTheDocument()
      expect(screen.getByText(/stop loss plus the contract in flight/)).toBeInTheDocument()
    })

    it('shows a placeholder rather than stale numbers while the quote loads', () => {
      renderTicket({ quote: undefined })
      expect(screen.queryByText(/Expected return/)).not.toBeInTheDocument()
    })

    it('never promises a profit', () => {
      renderTicket()
      expect(
        screen.getByText('Places your settings for you until a stop condition is met.'),
      ).toBeInTheDocument()
      expect(screen.queryByText(/\bAI\b/)).not.toBeInTheDocument()
      expect(screen.queryByText(/\bprofit\b/i)).not.toBeInTheDocument()
    })
  })

  describe('validation', () => {
    it('refuses a stake above the per-contract ceiling, naming it', async () => {
      const user = userEvent.setup()
      const { onSubmit } = renderTicket()

      const stake = screen.getByLabelText('Stake per contract')
      await user.clear(stake)
      await user.type(stake, '101')

      expect(
        screen.getByText('The most you can stake on one contract is $100.00'),
      ).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))
      expect(onSubmit).not.toHaveBeenCalled()
    })

    // A run places repeatedly, so a tenth of the balance is a different limit
    // from the one the manual ticket applies.
    it('refuses a stake above a tenth of the balance', async () => {
      const user = userEvent.setup()
      const poor: Wallet = { ...wallet, bonus_balance: '50.00', total: '50.00' }
      const { onSubmit } = renderTicket({ wallet: poor })

      const stake = screen.getByLabelText('Stake per contract')
      await user.clear(stake)
      await user.type(stake, '10')

      expect(
        screen.getByText(/at most a tenth of your balance, which is \$5\.00/),
      ).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('requires a stop loss', async () => {
      const user = userEvent.setup()
      const { onSubmit } = renderTicket()

      await user.clear(screen.getByLabelText('I have lost'))

      expect(screen.getByText('Set a stop loss so the run knows when to stop')).toBeInTheDocument()
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))
      expect(onSubmit).not.toHaveBeenCalled()
    })

    it('refuses a stop loss larger than the balance', async () => {
      const user = userEvent.setup()
      renderTicket()

      const stopLoss = screen.getByLabelText('I have lost')
      await user.clear(stopLoss)
      await user.type(stopLoss, '99999')

      expect(screen.getByText('The stop loss cannot be more than your balance')).toBeInTheDocument()
    })

    it('treats an empty take profit as not set, and a zero as a mistake', async () => {
      const user = userEvent.setup()
      const { onSubmit } = renderTicket()

      // Empty is fine: a run need not have an upper target.
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))
      expect(onSubmit).toHaveBeenCalledTimes(1)
      expect(onSubmit.mock.calls[0][0].limits.take_profit).toBeUndefined()

      const takeProfit = screen.getByLabelText('I am up')
      await user.type(takeProfit, '0')
      expect(
        screen.getByText('Enter a take profit greater than zero, or leave it empty'),
      ).toBeInTheDocument()
    })

    it('surfaces a server error against the field it names', () => {
      const err = new ApiError(422, {
        message: 'The most you can stake on one contract is $100.00',
        code: 'validation_failed',
        field: 'stake',
      })
      renderTicket({ error: err })
      expect(
        screen.getByText('The most you can stake on one contract is $100.00'),
      ).toBeInTheDocument()
    })

    it('surfaces a server error with no field above the button', () => {
      const err = new ApiError(409, {
        message: 'A run is already live on this account',
        code: 'bot_run_active',
      })
      renderTicket({ error: err })
      expect(screen.getByText('A run is already live on this account')).toBeInTheDocument()
    })
  })

  describe('what it submits', () => {
    it('sends the selection, stake, interval and limits', async () => {
      const user = userEvent.setup()
      const { onSubmit } = renderTicket()

      await user.click(screen.getByRole('button', { name: 'Odd' }))
      await user.click(screen.getByRole('button', { name: '5s' }))
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))

      expect(onSubmit).toHaveBeenCalledTimes(1)
      expect(onSubmit.mock.calls[0][0]).toMatchObject({
        contractType: 'even_odd',
        selection: { parity: 'odd' },
        stake: '10',
        intervalMS: 5000,
        limits: {
          stop_loss: '50',
          max_consecutive_losses: 5,
          max_contracts: 50,
          // Fixed in v1 rather than absent, so a run cannot outlive its session.
          max_duration_s: 1800,
        },
      })
    })

    it('carries the digit for Matches/Differs', async () => {
      const user = userEvent.setup()
      const { onSubmit } = renderTicket({ contractType: 'matches_differs', quote: matchesQuote })

      await user.click(within(screen.getByRole('group', { name: 'Digit' })).getByText('7'))
      await user.click(screen.getByRole('button', { name: 'Start auto run' }))

      expect(onSubmit.mock.calls[0][0].selection).toEqual({ mode: 'match', digit: 7 })
    })

    it('reports its configuration so the quote can follow it', async () => {
      const user = userEvent.setup()
      const onConfigChange = vi.fn()
      render(
        <BotTicket
          contractType="even_odd"
          symbol={symbol}
          wallet={wallet}
          quote={evenOddQuote}
          disabled={false}
          submitting={false}
          error={null}
          onConfigChange={onConfigChange}
          onSubmit={vi.fn()}
        />,
      )

      await user.click(screen.getByRole('button', { name: 'Odd' }))

      const last = onConfigChange.mock.calls.at(-1)?.[0]
      expect(last).toMatchObject({
        contractType: 'even_odd',
        selection: { parity: 'odd' },
        stake: '10',
      })
    })
  })

  describe('when it cannot start', () => {
    it('explains a missing price', () => {
      renderTicket({ disabled: true, disabledReason: 'Waiting for a live price…' })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeDisabled()
      expect(screen.getByText('Waiting for a live price…')).toBeInTheDocument()
    })

    it('explains a reconnecting feed', () => {
      renderTicket({ disabled: true, disabledReason: 'Market feed is reconnecting…' })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeDisabled()
      expect(screen.getByText('Market feed is reconnecting…')).toBeInTheDocument()
    })

    it('waits for the quote rather than starting without one', () => {
      renderTicket({ quote: undefined })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeDisabled()
    })

    it('is busy while a start is in flight', () => {
      renderTicket({ submitting: true })
      // The loading Button adds a visually-hidden "Working", so the accessible
      // name is no longer exactly the label.
      expect(screen.getByRole('button', { name: /Start auto run/ })).toBeDisabled()
    })

    // Real money is off, so a real account is told up front rather than at submit.
    it('refuses a real account while real-money runs are off, and says why', () => {
      renderTicket({ isReal: true, realMoneyEnabled: false })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeDisabled()
      expect(
        screen.getByText(/Automated runs are available on demo accounts for now/),
      ).toBeInTheDocument()
    })

    it('allows a real account once real-money runs are on', () => {
      renderTicket({ isReal: true, realMoneyEnabled: true })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeEnabled()
      expect(screen.queryByText(/available on demo accounts for now/)).not.toBeInTheDocument()
    })

    it('leaves a demo account alone', () => {
      renderTicket({ isReal: false, realMoneyEnabled: false })
      expect(screen.getByRole('button', { name: 'Start auto run' })).toBeEnabled()
    })
  })
})
