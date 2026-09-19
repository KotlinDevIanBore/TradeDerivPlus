import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, Transaction } from '@/types'
import { useSessionStore } from '@/store/session'
import { DepositSheet } from './DepositSheet'

// The sheet is driven by the wallet API and the rate lookup; both are stubbed
// so the step transitions can be tested without a server.
const deposit = vi.fn()
const transaction = vi.fn()

vi.mock('@/api/wallet', () => ({
  walletApi: {
    deposit: (...args: unknown[]) => deposit(...args),
    transaction: (...args: unknown[]) => transaction(...args),
    rate: () => Promise.resolve({ kes_per_usd: 129.5 }),
  },
}))

const realAccount: Account = {
  id: 'acc-real',
  user_id: 'u1',
  account_number: '900002',
  account_type: 'real',
  currency: 'USD',
  status: 'active',
  frozen_reason: null,
  created_at: new Date().toISOString(),
}

const demoAccount: Account = { ...realAccount, id: 'acc-demo', account_number: '900001', account_type: 'demo' }

function renderSheet(account: Account = realAccount) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onOpenChange = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <DepositSheet
          open
          onOpenChange={onOpenChange}
          account={account}
          realAccount={realAccount}
          onSwitchToReal={vi.fn()}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onOpenChange }
}

describe('DepositSheet', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useSessionStore.setState({
      accessToken: 'token',
      user: {
        id: 'u1',
        phone_number: '254712345678',
        email: null,
        full_name: null,
        kyc_status: 'pending',
        created_at: '',
        updated_at: '',
      },
      activeAccountId: 'acc-real',
    })
  })

  it('prefills the phone number from the profile', async () => {
    renderSheet()
    await waitFor(() => {
      expect(screen.getByPlaceholderText('712 345 678')).toHaveValue('712345678')
    })
  })

  it('shows what the KES amount converts to before sending', async () => {
    const user = userEvent.setup()
    renderSheet()

    await user.type(screen.getByLabelText(/amount/i), '12950')
    expect(await screen.findByText(/≈ \$100\.00 will be credited/i)).toBeInTheDocument()
  })

  it('refuses an amount below the minimum', async () => {
    const user = userEvent.setup()
    renderSheet()

    await user.type(screen.getByLabelText(/amount/i), '10')
    expect(await screen.findByText(/minimum deposit is kes 50/i)).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /send stk push/i })).toBeDisabled()
  })

  it('moves to the waiting step once M-Pesa accepts the push', async () => {
    const user = userEvent.setup()
    deposit.mockResolvedValue({
      transaction_id: 'tx-1',
      checkout_request_id: 'ws_CO_1',
      amount_kes: '1000',
      amount_usd: '7.72',
      status: 'pending',
    })
    transaction.mockResolvedValue({ id: 'tx-1', status: 'pending' } as Partial<Transaction>)

    renderSheet()
    await user.type(screen.getByLabelText(/amount/i), '1000')
    await user.click(screen.getByRole('button', { name: /send stk push/i }))

    expect(await screen.findByText(/check your phone/i)).toBeInTheDocument()
    expect(screen.getByText(/enter your m-pesa pin/i)).toBeInTheDocument()
  })

  it('reports success with the credited amount once the callback lands', async () => {
    const user = userEvent.setup()
    deposit.mockResolvedValue({
      transaction_id: 'tx-2',
      checkout_request_id: 'ws_CO_2',
      amount_kes: '12950',
      amount_usd: '100',
      status: 'pending',
    })
    transaction.mockResolvedValue({
      id: 'tx-2',
      account_id: 'acc-real',
      status: 'completed',
      amount: '100',
      type: 'deposit',
    })

    renderSheet()
    await user.type(screen.getByLabelText(/amount/i), '12950')
    await user.click(screen.getByRole('button', { name: /send stk push/i }))

    expect(await screen.findByText(/kes 12,950 deposited/i, undefined, { timeout: 6000 })).toBeInTheDocument()
    expect(screen.getByRole('button', { name: /start trading/i })).toBeInTheDocument()
  }, 10_000)

  it('shows the human reason when the deposit fails', async () => {
    const user = userEvent.setup()
    deposit.mockResolvedValue({
      transaction_id: 'tx-3',
      checkout_request_id: 'ws_CO_3',
      amount_kes: '1000',
      amount_usd: '7.72',
      status: 'pending',
    })
    transaction.mockResolvedValue({
      id: 'tx-3',
      account_id: 'acc-real',
      status: 'failed',
      amount: '7.72',
      type: 'deposit',
      metadata: { reason: 'You cancelled the request' },
    })

    renderSheet()
    await user.type(screen.getByLabelText(/amount/i), '1000')
    await user.click(screen.getByRole('button', { name: /send stk push/i }))

    expect(await screen.findByText(/didn't go through/i, undefined, { timeout: 6000 })).toBeInTheDocument()
    expect(screen.getByText(/you cancelled the request/i)).toBeInTheDocument()
  }, 10_000)

  it('sends a demo user to their real account instead of taking money', () => {
    renderSheet(demoAccount)
    expect(screen.getByText(/switch to your real account to deposit/i)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: /send stk push/i })).not.toBeInTheDocument()
  })
})
