import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import type { BotRun, DigitContract } from '@/types'
import { BotRunCard } from './BotRunCard'

function makeRun(overrides: Partial<BotRun> = {}): BotRun {
  return {
    id: 'run-1',
    account_id: 'a1',
    symbol_id: 's1',
    status: 'running',
    stop_reason: null,
    last_error: null,
    contract_type: 'even_odd',
    selection: { parity: 'even' },
    stake: '10.00',
    interval_ms: 2000,
    limits: { stop_loss: '50', max_contracts: 50 },
    starting_balance: '10000.00',
    contracts_placed: 12,
    wins: 6,
    losses: 5,
    voids: 1,
    consecutive_losses: 1,
    consecutive_voids: 0,
    realised_pnl: '-2.40',
    started_at: new Date(Date.now() - 60_000).toISOString(),
    stopped_at: null,
    updated_at: new Date().toISOString(),
    symbol_code: 'R_75',
    symbol_name: 'Volatility 75 Index',
    ...overrides,
  }
}

function makeContract(overrides: Partial<DigitContract> = {}): DigitContract {
  return {
    id: 'c1',
    account_id: 'a1',
    symbol_id: 's1',
    contract_type: 'even_odd',
    selection: { parity: 'even' },
    stake: '10',
    payout_ratio: '0.952',
    potential_payout: '19.52',
    entry_price: '100.10',
    entry_digit: 0,
    entry_tick_epoch: 1,
    status: 'lost',
    settlement_price: '100.1235',
    settlement_digit: 7,
    settled_at: new Date(Date.now() - 3_000).toISOString(),
    pnl: '-10',
    opened_at: new Date(Date.now() - 5_000).toISOString(),
    void_after: new Date().toISOString(),
    bot_run_id: 'run-1',
    symbol_code: 'R_75',
    symbol_name: 'Volatility 75 Index',
    pip_size: 4,
    ...overrides,
  }
}

function renderCard(overrides: Partial<React.ComponentProps<typeof BotRunCard>> = {}) {
  const onStop = vi.fn()
  render(<BotRunCard run={makeRun()} stopping={false} onStop={onStop} {...overrides} />)
  return { onStop }
}

describe('BotRunCard', () => {
  describe('what it reports', () => {
    it('heads with the run, not with the account', () => {
      renderCard()
      expect(screen.getByText(/AUTO · R_75 · Even/)).toBeInTheDocument()
      expect(screen.getByText(/\$10\.00 every 2s/)).toBeInTheDocument()
    })

    it('shows placements against the limit the run was given', () => {
      renderCard()
      expect(screen.getByText('12')).toBeInTheDocument()
      expect(screen.getByText('/50')).toBeInTheDocument()
    })

    it('shows wins, losses and voids separately', () => {
      renderCard()
      // Voids are their own figure, because a refund is not a loss.
      expect(screen.getByText('6 / 5 / 1')).toBeInTheDocument()
    })

    it('names the selection for Over/Under', () => {
      renderCard({
        run: makeRun({ contract_type: 'over_under', selection: { side: 'over', barrier: 4 } }),
      })
      expect(screen.getByText(/AUTO · R_75 · Over 4/)).toBeInTheDocument()
    })

    it('reports the last contract that settled', () => {
      renderCard({ lastContract: makeContract() })
      expect(screen.getByText('lost')).toBeInTheDocument()
      expect(screen.getByText(/digit 7/)).toBeInTheDocument()
    })

    it('leaves the last-contract line out before anything has settled', () => {
      renderCard()
      expect(screen.queryByText(/^Last:/)).not.toBeInTheDocument()
    })
  })

  describe('the stop-loss bar', () => {
    it('measures losses against the floor', () => {
      // -2.40 of a 50 floor is a little under 5%.
      renderCard()
      const bar = screen.getByRole('progressbar', { name: 'Towards stop loss' })
      expect(bar).toHaveAttribute('aria-valuenow', '5')
    })

    // A run in profit is nowhere near its stop loss; a bar creeping backwards
    // would suggest otherwise.
    it('sits at zero while the run is up', () => {
      renderCard({ run: makeRun({ realised_pnl: '19.04' }) })
      expect(screen.getByRole('progressbar', { name: 'Towards stop loss' })).toHaveAttribute(
        'aria-valuenow',
        '0',
      )
    })

    it('does not run past full once the floor is reached', () => {
      renderCard({ run: makeRun({ realised_pnl: '-80' }) })
      expect(screen.getByRole('progressbar', { name: 'Towards stop loss' })).toHaveAttribute(
        'aria-valuenow',
        '100',
      )
    })
  })

  describe('stopping', () => {
    it('offers a stop and reports it once', async () => {
      const user = userEvent.setup()
      const { onStop } = renderCard()

      await user.click(screen.getByRole('button', { name: 'Stop run' }))
      expect(onStop).toHaveBeenCalledTimes(1)
    })

    // The server's 'stopping' state: a stop was asked for and a contract is still
    // in flight. The run has not ended, and saying it has would be a lie.
    it('shows the run winding down once the server says stopping', () => {
      renderCard({ run: makeRun({ status: 'stopping' }) })

      const button = screen.getByRole('button', { name: /Stopping/ })
      expect(button).toBeDisabled()
      expect(screen.getByText('Stopping after the current contract settles.')).toBeInTheDocument()
    })

    it('does not report a second stop while one is already in flight', async () => {
      const user = userEvent.setup()
      const { onStop } = renderCard({ stopping: true })

      const button = screen.getByRole('button', { name: /Stopping/ })
      expect(button).toBeDisabled()
      // A disabled button swallows the click; asserting the handler stayed
      // unused is what proves a second stop cannot be sent.
      await user.click(button).catch(() => {})
      expect(onStop).not.toHaveBeenCalled()
    })

    it('says the run stops on its own while it is still running', () => {
      renderCard()
      expect(screen.getByText('Stops on its own when a limit is reached.')).toBeInTheDocument()
      expect(screen.getByRole('button', { name: 'Stop run' })).toBeEnabled()
    })
  })
})
