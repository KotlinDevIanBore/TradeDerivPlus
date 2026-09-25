import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { absoluteTime, relativeTime } from '@/lib/format'
import { cn } from '@/lib/cn'
import { Sheet } from '@/components/ui/Sheet'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { DataTable, type Column } from '../components/DataTable'
import { ReasonDialog } from '../components/ReasonDialog'
import { FilterChips } from '../components/SearchInput'
import type { AdminPayout } from '../adminTypes'

type Outcome = 'paid' | 'failed' | 'never_received'

/**
 * What each payout state means, in the words an operator needs rather than the
 * database's. The two middle ones are why this screen exists: they are the
 * difference between "nobody knows" and "we know it never happened", and only the
 * second makes a refund safe without M-Pesa having spoken.
 */
const STATUS: Record<AdminPayout['status'], { label: string; tone: string; meaning: string }> = {
  pending: {
    label: 'Not sent',
    tone: 'text-muted',
    meaning: 'Reserved alongside the debit, not yet sent to M-Pesa.',
  },
  sent: {
    label: 'Sent',
    tone: 'text-muted',
    meaning: 'M-Pesa accepted the request and has not reported back yet.',
  },
  unknown: {
    label: 'Unknown',
    tone: 'text-warning',
    meaning:
      'M-Pesa has not said either way — the request may or may not have reached them. ' +
      'The balance is already debited, so this must not be refunded on a guess.',
  },
  never_received: {
    label: 'Never received',
    tone: 'text-accent',
    meaning: 'Established that M-Pesa never had the request. Refunded.',
  },
  completed: { label: 'Paid', tone: 'text-profit', meaning: 'The customer has the money.' },
  failed: { label: 'Failed', tone: 'text-accent', meaning: 'M-Pesa did not pay. Refunded.' },
}

export function AdminPayouts() {
  const [params, setParams] = useSearchParams()
  const filter = params.get('filter') ?? 'open'
  const page = Number(params.get('page') ?? 1)
  const [detail, setDetail] = useState<AdminPayout | null>(null)
  const [resolving, setResolving] = useState<Outcome | null>(null)
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: adminKeys.payouts({ filter, page }),
    queryFn: () => adminApi.payouts({ filter, page }),
  })

  const resolve = useMutation({
    mutationFn: ({ id, outcome, reason }: { id: string; outcome: Outcome; reason: string }) =>
      adminApi.resolvePayout(id, outcome, reason),
    onSuccess: (_tx, vars) => {
      toast.success(
        vars.outcome === 'paid' ? 'Recorded as paid' : 'Refunded to the customer',
        vars.outcome === 'paid'
          ? 'The withdrawal is closed. No balance changed — the money had already left.'
          : "The customer's balance has been returned.",
      )
      setResolving(null)
      setDetail(null)
      queryClient.invalidateQueries({ queryKey: ['admin'] })
    },
  })

  const isOpen = (p: AdminPayout) => ['pending', 'sent', 'unknown'].includes(p.status)

  const columns: Column<AdminPayout>[] = [
    {
      key: 'age',
      header: 'Started',
      render: (p) => (
        <span className={cn('text-muted', p.given_up && 'text-warning')}>
          {relativeTime(p.created_at)}
          {p.given_up ? (
            <span className="ml-1.5 text-[10px] uppercase" title="Nothing is checking this any more">
              no longer checking
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'status',
      header: 'Payout',
      render: (p) => <span className={STATUS[p.status].tone}>{STATUS[p.status].label}</span>,
    },
    {
      key: 'account',
      header: 'Account',
      render: (p) => (
        <Link
          to={`/admin/users/${p.user_id}`}
          onClick={(e) => e.stopPropagation()}
          className="tnum text-accent hover:underline"
        >
          {p.account_number}
        </Link>
      ),
    },
    { key: 'to', header: 'To', render: (p) => <span className="tnum text-muted">+{p.phone_number}</span> },
    { key: 'amount', header: 'Amount', align: 'right', render: (p) => <Money value={p.amount} /> },
    {
      key: 'asked',
      header: 'Asked',
      align: 'right',
      render: (p) => (
        <span className="text-muted tnum" title="Status queries sent to M-Pesa about this payout">
          {p.reconcile_attempts}
        </span>
      ),
    },
  ]

  return (
    <>
      <AdminPageHeader title="Payouts" />

      <div className="mb-3">
        <FilterChips
          value={filter}
          onChange={(v) => {
            const p = new URLSearchParams(params)
            if (v) p.set('filter', v)
            else p.delete('filter')
            p.delete('page')
            setParams(p, { replace: true })
          }}
          options={[
            { label: 'Open', value: 'open' },
            { label: 'Unknown', value: 'unknown' },
            { label: 'No longer checking', value: 'given_up' },
            { label: 'Settled', value: 'settled' },
            { label: 'All', value: 'all' },
          ]}
        />
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(p) => p.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        onRowClick={setDetail}
        empty={
          <p className="text-center text-body text-muted">
            {filter === 'open' || filter === 'unknown'
              ? 'No payouts are waiting. Every withdrawal has an answer.'
              : 'No payouts match'}
          </p>
        }
        page={query.data?.page ?? page}
        pages={query.data?.pages ?? 0}
        total={query.data?.total ?? 0}
        limit={query.data?.limit ?? 25}
        onPageChange={(next) => {
          const p = new URLSearchParams(params)
          p.set('page', String(next))
          setParams(p, { replace: true })
        }}
      />

      <Sheet open={detail !== null} onOpenChange={(open) => !open && setDetail(null)} title="Payout">
        {detail ? (
          <div className="space-y-3">
            <div className="text-center">
              <p className="text-title font-bold">
                <Money value={detail.amount} />
              </p>
              <p className={cn('mt-1 text-body', STATUS[detail.status].tone)}>
                {STATUS[detail.status].label}
              </p>
            </div>

            <p className="rounded-xl border border-border bg-elevated px-3 py-2 text-meta text-muted">
              {STATUS[detail.status].meaning}
            </p>

            <div className="divide-y divide-hairline">
              <Row label="To" value={<span className="tnum">+{detail.phone_number}</span>} />
              <Row
                label="Account"
                value={
                  <Link to={`/admin/users/${detail.user_id}`} className="text-accent hover:underline">
                    {detail.account_number} ({detail.account_type})
                  </Link>
                }
              />
              <Row label="Withdrawal" value={detail.ledger_status} />
              <Row label="Started" value={absoluteTime(detail.created_at)} />
              <Row
                label="Requested"
                value={detail.requested_at ? absoluteTime(detail.requested_at) : 'not sent'}
              />
              <Row label="Status queries sent" value={String(detail.reconcile_attempts)} />
              <Row
                label="Still checking"
                value={detail.given_up ? 'no — this is yours now' : isOpen(detail) ? 'yes' : '—'}
              />
              {/* The id to search for on the M-PESA portal, and the only reference
                  a payout settled without a receipt will ever have. */}
              <Row
                label="M-Pesa request id"
                value={<span className="tnum break-all">{detail.originator_conversation_id}</span>}
              />
              {detail.receipt ? (
                <Row label="M-Pesa receipt" value={<span className="tnum">{detail.receipt}</span>} />
              ) : null}
              {detail.result_code ? <Row label="Result code" value={detail.result_code} /> : null}
              {detail.settled_by ? <Row label="Settled by" value={detail.settled_by} /> : null}
            </div>

            {detail.result_desc ? (
              <p className="text-meta text-faint">{detail.result_desc}</p>
            ) : null}

            {isOpen(detail) ? (
              <div className="space-y-2 rounded-xl border border-border bg-elevated p-3">
                <p className="text-meta text-muted">
                  Check this request id on the M-Pesa portal before choosing. Each option records
                  something different, and two of them return the customer's money.
                </p>
                <Button block variant="outline" onClick={() => setResolving('paid')}>
                  The customer was paid
                </Button>
                <Button block variant="danger" onClick={() => setResolving('never_received')}>
                  M-Pesa has no record of it
                </Button>
                <Button block variant="danger" onClick={() => setResolving('failed')}>
                  M-Pesa had it and it failed
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
      </Sheet>

      <ReasonDialog
        open={resolving !== null && detail !== null}
        onOpenChange={(open) => !open && setResolving(null)}
        title={
          resolving === 'paid'
            ? 'Record this payout as paid?'
            : resolving === 'never_received'
              ? 'M-Pesa never received it?'
              : 'M-Pesa reported a failure?'
        }
        consequence={
          detail && resolving === 'paid' ? (
            <>
              Closes the withdrawal as paid. <strong className="text-text">No balance changes</strong>{' '}
              — the money left when the withdrawal was requested. If M-Pesa later reports it failed,
              that report is logged and refunds nothing, so only do this once the portal shows the
              payment.
            </>
          ) : detail ? (
            <>
              Returns <strong className="text-text"><Money value={detail.amount} /></strong> to{' '}
              <strong className="text-text">{detail.account_number}</strong>
              {resolving === 'never_received'
                ? ', and records that M-Pesa never had the request. '
                : ', and records that M-Pesa had the request and did not pay. '}
              If M-Pesa later reports the payout succeeded, that report is logged and credits nothing
              — the customer would hold both the money and the refund, and a B2C payout cannot be
              reversed. Be sure from the portal before refunding.
            </>
          ) : null
        }
        confirmLabel={resolving === 'paid' ? 'Record as paid' : 'Refund the customer'}
        tone={resolving === 'paid' ? 'safe' : 'danger'}
        pending={resolve.isPending}
        error={resolve.error}
        onConfirm={(reason) =>
          detail && resolving && resolve.mutate({ id: detail.transaction_id, outcome: resolving, reason })
        }
      />
    </>
  )
}

function Row({ label, value }: { label: string; value: React.ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2">
      <span className="text-meta text-muted">{label}</span>
      <span className="text-body text-text">{value}</span>
    </div>
  )
}
