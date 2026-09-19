import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { ApiError } from '@/api/client'
import { AccountPill } from '@/components/AccountPill'
import { DirectionBadge } from '@/components/DirectionBadge'
import { ErrorState } from '@/components/ErrorState'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/Button'
import { Card, CardTitle } from '@/components/ui/Card'
import { Input } from '@/components/ui/Input'
import { Skeleton } from '@/components/ui/Skeleton'
import { toast } from '@/components/ui/Toast'
import { cn } from '@/lib/cn'
import { money, price as fmtPrice, relativeTime, transactionTitle } from '@/lib/format'
import { dec } from '@/lib/money'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { AccountStatusBadge, ActionBadge, KYCBadge } from '../components/badges'
import { ReasonDialog } from '../components/ReasonDialog'
import type { AdminAccountDetail } from '../adminTypes'

type PendingAction =
  | { kind: 'freeze'; account: AdminAccountDetail }
  | { kind: 'unfreeze'; account: AdminAccountDetail }
  | { kind: 'adjust'; account: AdminAccountDetail }
  | null

export function AdminUserDetailScreen() {
  const { id = '' } = useParams()
  const queryClient = useQueryClient()
  const [action, setAction] = useState<PendingAction>(null)

  // Adjustment inputs live here so the confirmation can render from the same
  // values that get submitted, rather than from a template that could drift.
  const [amount, setAmount] = useState('')
  const [balanceType, setBalanceType] = useState<'real' | 'bonus'>('real')

  const query = useQuery({
    queryKey: adminKeys.user(id),
    queryFn: () => adminApi.userDetail(id),
    enabled: Boolean(id),
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: adminKeys.user(id) })
    queryClient.invalidateQueries({ queryKey: ['admin', 'audit'] })
    queryClient.invalidateQueries({ queryKey: adminKeys.dashboard() })
  }

  const freeze = useMutation({
    mutationFn: ({ accountId, reason }: { accountId: string; reason: string }) =>
      adminApi.freeze(accountId, reason),
    onSuccess: (a) => {
      toast.success(`Account ${a.account_number} frozen`)
      setAction(null)
      invalidate()
    },
  })

  const unfreeze = useMutation({
    mutationFn: ({ accountId, reason }: { accountId: string; reason: string }) =>
      adminApi.unfreeze(accountId, reason),
    onSuccess: (a) => {
      toast.success(`Account ${a.account_number} unfrozen`)
      setAction(null)
      invalidate()
    },
  })

  const adjust = useMutation({
    mutationFn: ({ accountId, reason }: { accountId: string; reason: string }) =>
      adminApi.adjust(accountId, dec(amount).toFixed(2), balanceType, reason),
    onSuccess: () => {
      toast.success('Balance adjusted', `${money(amount)} on ${balanceType} balance`)
      setAction(null)
      setAmount('')
      invalidate()
    },
  })

  if (query.isError) {
    return (
      <>
        <AdminPageHeader title="User" />
        <ErrorState error={query.error} onRetry={() => query.refetch()} />
      </>
    )
  }

  const d = query.data
  const amountValue = dec(amount)
  const pendingKYC = d?.kyc_submissions.find((k) => k.status === 'pending')

  return (
    <>
      <AdminPageHeader
        title={d?.user.full_name ?? (d ? `+${d.user.phone_number}` : 'User')}
        breadcrumb={
          <>
            <Link to="/admin/users" className="hover:text-text">
              Users
            </Link>
            {d ? ` / +${d.user.phone_number}` : ''}
          </>
        }
      />

      {query.isLoading ? (
        <div className="space-y-3">
          <Skeleton className="h-[96px] rounded-card" />
          <Skeleton className="h-[180px] rounded-card" />
        </div>
      ) : !d ? null : (
        <div className="space-y-3">
          <Card>
            <div className="flex flex-wrap items-start justify-between gap-4">
              <div className="divide-y divide-hairline">
                <Row label="Phone" value={<span className="tnum">+{d.user.phone_number}</span>} />
                <Row label="Email" value={d.user.email ?? '—'} />
                <Row label="Joined" value={relativeTime(d.user.created_at)} />
              </div>
              <div className="text-right">
                <KYCBadge status={d.user.kyc_status} />
                {pendingKYC ? (
                  <Link
                    to="/admin/kyc?status=pending"
                    className="mt-2 block text-meta font-medium text-accent hover:underline"
                  >
                    Review submission
                  </Link>
                ) : null}
              </div>
            </div>
          </Card>

          <section className="grid gap-3 md:grid-cols-2">
            {d.accounts.map((a) => (
              <Card key={a.id} className={cn(a.status === 'frozen' && 'border-l-2 border-l-accent')}>
                <div className="flex items-start justify-between gap-3">
                  <div>
                    <span className="flex items-center gap-2">
                      <AccountPill account={a} />
                      <AccountStatusBadge status={a.status} />
                    </span>
                    <p className="mt-2 text-title font-bold">
                      <Money value={a.wallet?.total} />
                    </p>
                    <p className="mt-0.5 text-meta text-muted tnum">
                      Real {money(a.wallet?.real_balance)} · Bonus {money(a.wallet?.bonus_balance)}
                    </p>
                  </div>
                </div>

                {a.frozen_reason ? (
                  <p className="mt-3 rounded-lg border border-warning/30 bg-warning/[0.06] px-3 py-2 text-meta text-warning">
                    {a.frozen_reason}
                  </p>
                ) : null}

                <div className="mt-4 flex gap-2">
                  {a.status === 'frozen' ? (
                    <Button
                      variant="outline"
                      size="sm"
                      className="flex-1 border-profit/50 text-profit hover:bg-profit/10"
                      onClick={() => setAction({ kind: 'unfreeze', account: a })}
                    >
                      Unfreeze
                    </Button>
                  ) : (
                    <Button
                      variant="danger"
                      size="sm"
                      className="flex-1"
                      onClick={() => setAction({ kind: 'freeze', account: a })}
                    >
                      Freeze
                    </Button>
                  )}
                  <Button
                    variant="danger"
                    size="sm"
                    className="flex-1"
                    onClick={() => {
                      setAmount('')
                      setBalanceType('real')
                      setAction({ kind: 'adjust', account: a })
                    }}
                  >
                    Adjust balance
                  </Button>
                </div>
              </Card>
            ))}
          </section>

          {d.open_trades.length > 0 ? (
            <Card>
              <CardTitle>Open positions</CardTitle>
              <p className="mt-1 text-meta text-faint">
                Read-only. Admins cannot close a user's position.
              </p>
              <ul className="mt-3 divide-y divide-hairline">
                {d.open_trades.map((t) => (
                  <li key={t.id} className="flex items-center gap-3 py-2">
                    <DirectionBadge direction={t.direction} size="sm" />
                    <span className="flex-1 truncate text-body">{t.symbol_code}</span>
                    <span className="text-meta text-muted tnum">{money(t.stake)}</span>
                    <span className="text-meta text-faint tnum">
                      @ {fmtPrice(t.entry_price, t.pip_size)}
                    </span>
                  </li>
                ))}
              </ul>
            </Card>
          ) : null}

          <Card>
            <CardTitle>Recent transactions</CardTitle>
            {d.transactions.length === 0 ? (
              <p className="mt-3 text-meta text-muted">No transactions yet.</p>
            ) : (
              <ul className="mt-3 divide-y divide-hairline">
                {d.transactions.slice(0, 25).map((t) => (
                  <li key={t.id} className="flex items-center gap-3 py-2">
                    <span className="min-w-0 flex-1">
                      <span className="block truncate text-body">
                        {transactionTitle(t.type, t.metadata)}
                      </span>
                      <span className="text-meta text-muted">{relativeTime(t.created_at)}</span>
                    </span>
                    {t.status !== 'completed' ? <StatusBadge status={t.status} /> : null}
                    <Money value={t.amount} signed className="shrink-0 text-body" />
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card>
            <CardTitle>Admin actions on this user</CardTitle>
            {d.audit.length === 0 ? (
              <p className="mt-3 text-meta text-muted">Nothing has been done to this account.</p>
            ) : (
              <ul className="mt-3 divide-y divide-hairline">
                {d.audit.map((e) => (
                  <li key={e.id} className="py-2.5">
                    <div className="flex items-center gap-2">
                      <ActionBadge action={e.action} />
                      <span className="text-meta text-muted">{e.admin_username}</span>
                      <span className="ml-auto text-meta text-faint">
                        {relativeTime(e.created_at)}
                      </span>
                    </div>
                    {e.reason ? <p className="mt-1 text-meta text-muted">{e.reason}</p> : null}
                  </li>
                ))}
              </ul>
            )}
          </Card>
        </div>
      )}

      {/* --- Freeze --- */}
      <ReasonDialog
        open={action?.kind === 'freeze'}
        onOpenChange={(open) => !open && setAction(null)}
        title={`Freeze account ${action?.account.account_number ?? ''}?`}
        consequence="The user will not be able to trade, deposit, or withdraw from this account until it is unfrozen. Open positions stay open and can still be closed."
        confirmLabel="Freeze account"
        pending={freeze.isPending}
        error={freeze.error}
        onConfirm={(reason) =>
          action && freeze.mutate({ accountId: action.account.id, reason })
        }
      />

      {/* --- Unfreeze --- */}
      <ReasonDialog
        open={action?.kind === 'unfreeze'}
        onOpenChange={(open) => !open && setAction(null)}
        title={`Unfreeze account ${action?.account.account_number ?? ''}?`}
        consequence="The user will be able to trade, deposit and withdraw again. Say why it is safe to reopen."
        confirmLabel="Unfreeze account"
        tone="safe"
        pending={unfreeze.isPending}
        error={unfreeze.error}
        onConfirm={(reason) =>
          action && unfreeze.mutate({ accountId: action.account.id, reason })
        }
      />

      {/* --- Adjust --- */}
      <ReasonDialog
        open={action?.kind === 'adjust'}
        onOpenChange={(open) => !open && setAction(null)}
        title="Adjust balance"
        consequence={
          amountValue.isZero() ? (
            'Enter an amount. A leading minus debits the account.'
          ) : action ? (
            // Rendered from the values that will be submitted, so the sentence
            // cannot drift from what actually happens.
            <>
              {amountValue.isNegative() ? 'Debit' : 'Credit'}{' '}
              <strong className="text-text">{money(amountValue.abs().toString())}</strong>{' '}
              {amountValue.isNegative() ? 'from' : 'to'}{' '}
              <strong className="text-text">{balanceType}</strong> balance on{' '}
              <strong className="text-text">
                {action.account.account_type.toUpperCase()} {action.account.account_number}
              </strong>
              . This writes a ledger row that cannot be undone; a mistake is corrected with a
              second adjustment.
            </>
          ) : null
        }
        confirmLabel={
          amountValue.isZero()
            ? 'Adjust balance'
            : `${amountValue.isNegative() ? 'Debit' : 'Credit'} ${money(amountValue.abs().toString())}`
        }
        disabled={amountValue.isZero()}
        pending={adjust.isPending}
        error={adjust.error}
        onConfirm={(reason) => action && adjust.mutate({ accountId: action.account.id, reason })}
      >
        <Input
          label="Amount"
          inputMode="decimal"
          prefix="$"
          placeholder="0.00"
          value={amount}
          onChange={(e) => setAmount(e.target.value.replace(/[^\d.-]/g, ''))}
          hint="A leading minus debits the account"
          error={
            adjust.error instanceof ApiError && adjust.error.field === 'amount'
              ? adjust.error.message
              : undefined
          }
        />
        <div>
          <span className="mb-1.5 block text-meta font-medium text-muted">Balance</span>
          <div className="grid grid-cols-2 gap-2">
            {(['real', 'bonus'] as const).map((b) => (
              <button
                key={b}
                type="button"
                onClick={() => setBalanceType(b)}
                className={cn(
                  'rounded-xl border py-2 text-body capitalize transition-colors',
                  balanceType === b
                    ? 'border-accent bg-accent-subtle text-text'
                    : 'border-border text-muted hover:text-text',
                )}
              >
                {b}
              </button>
            ))}
          </div>
        </div>
      </ReasonDialog>
    </>
  )
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline gap-6 py-1.5">
      <span className="w-16 shrink-0 text-meta text-muted">{label}</span>
      <span className="text-body text-text">{value}</span>
    </div>
  )
}
