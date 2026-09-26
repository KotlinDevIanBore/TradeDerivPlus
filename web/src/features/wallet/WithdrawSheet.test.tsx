import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { render, screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { MemoryRouter } from 'react-router-dom'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Account, Wallet } from '@/types'
import { useSessionStore } from '@/store/session'
import { WithdrawSheet } from './WithdrawSheet'

// The withdraw call is stubbed so the test can assert it is never reached: the
// point of the demo state is that the API is not asked a question it will refuse.
const withdraw = vi.fn()

vi.mock('@/api/wallet', () => ({
  walletApi: {
    withdraw: (...args: unknown[]) => withdraw(...args),
    rate: () => Promise.resolve({ kes_per_usd: 129.5 }),
  },
}))

vi.mock('@/api/kyc', () => ({
  kycApi: { state: () => Promise.resolve({ status: 'verified', unfinished_upload: false }) },
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

const demoAccount: Account = {
  ...realAccount,
  id: 'acc-demo',
  account_number: '900001',
  account_type: 'demo',
}

const wallet: Wallet = {
  account_id: 'acc-demo',
  real_balance: '250.00',
  bonus_balance: '0.00',
  total: '250.00',
  updated_at: new Date().toISOString(),
}

function renderSheet(account: Account, opts: { realAccount?: Account | null } = {}) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  const onSwitchToReal = vi.fn()
  render(
    <QueryClientProvider client={client}>
      <MemoryRouter>
        <WithdrawSheet
          open
          onOpenChange={vi.fn()}
          account={account}
          wallet={wallet}
          realAccount={opts.realAccount === undefined ? realAccount : opts.realAccount}
          onSwitchToReal={onSwitchToReal}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  )
  return { onSwitchToReal }
}

describe('WithdrawSheet on a demo account', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    // Verified, and withdrawals ON -- the configuration in which the demo gap
    // becomes reachable. With withdrawals off the platform gate answers first and
    // a missing demo state is invisible, which is exactly why this was latent.
    useSessionStore.setState({
      accessToken: 'token',
      user: {
        id: 'u1',
        phone_number: '254712345678',
        email: null,
        full_name: null,
        kyc_status: 'verified',
        withdrawals_enabled: true,
        created_at: '',
        updated_at: '',
      },
      activeAccountId: 'acc-demo',
    })
  })

  it('explains that demo money cannot be withdrawn, instead of offering the form', async () => {
    renderSheet(demoAccount)

    expect(await screen.findByText(/Demo money isn't real/)).toBeInTheDocument()
    // No form, so no way to reach the API refusal.
    expect(screen.queryByLabelText(/amount/i)).not.toBeInTheDocument()
    expect(withdraw).not.toHaveBeenCalled()
  })

  it('offers a switch to the real account rather than moving the money itself', async () => {
    const { onSwitchToReal } = renderSheet(demoAccount)

    await userEvent.click(await screen.findByRole('button', { name: 'Switch to real account' }))
    expect(onSwitchToReal).toHaveBeenCalledOnce()
    // Switching accounts is all it does. It must never submit a withdrawal
    // against an account the user was not looking at.
    expect(withdraw).not.toHaveBeenCalled()
  })

  it('offers no switch when there is no real account to switch to', async () => {
    renderSheet(demoAccount, { realAccount: null })

    expect(await screen.findByText(/Demo money isn't real/)).toBeInTheDocument()
    expect(screen.queryByRole('button', { name: 'Switch to real account' })).not.toBeInTheDocument()
  })

  it('still shows the withdrawal form on a real account', async () => {
    renderSheet(realAccount)

    expect(await screen.findByText(/Available to withdraw|Amount/i)).toBeInTheDocument()
    expect(screen.queryByText(/Demo money isn't real/)).not.toBeInTheDocument()
  })
})
