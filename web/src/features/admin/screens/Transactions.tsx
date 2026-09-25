import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { absoluteTime, relativeTime, transactionTitle } from '@/lib/format'
import { cn } from '@/lib/cn'
import { Sheet } from '@/components/ui/Sheet'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { DataTable, type Column } from '../components/DataTable'
import { ReasonDialog } from '../components/ReasonDialog'
import { FilterChips, SearchInput } from '../components/SearchInput'
import type { AdminTransaction } from '../adminTypes'

/** A deposit pending longer than this is worth flagging to support. */
const STUCK_AFTER_MS = 10 * 60_000

function isPendingDeposit(t: AdminTransaction) {
  return t.type === 'deposit' && t.status === 'pending'
}

/**
 * Pending past the point where the callback would normally have arrived. The
 * server is asking Daraja about it on a schedule by now; the marker says a
 * person may need to as well. Open the row to resolve it.
 */
function isStuck(t: AdminTransaction) {
  return isPendingDeposit(t) && Date.now() - new Date(t.created_at).getTime() > STUCK_AFTER_MS
}

/** How a settled deposit got its outcome, from the ledger row. */
const SETTLED_BY: Record<string, string> = {
  callback: 'M-Pesa callback',
  query: 'Status query to M-Pesa',
  admin: 'An operator',
}

export function AdminTransactions() {
  const [params, setParams] = useSearchParams()
  const [search, setSearch] = useState(params.get('search') ?? '')
  const [type, setType] = useState(params.get('type') ?? '')
  const [status, setStatus] = useState(params.get('status') ?? '')
  const [detail, setDetail] = useState<AdminTransaction | null>(null)
  const [resolving, setResolving] = useState<'completed' | 'failed' | null>(null)
  const page = Number(params.get('page') ?? 1)
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: adminKeys.transactions({ search, type, status, page }),
    queryFn: () => adminApi.transactions({ search, type, status, page }),
  })

  const resolve = useMutation({
    mutationFn: ({ id, outcome, reason }: { id: string; outcome: 'completed' | 'failed'; reason: string }) =>
      adminApi.resolveDeposit(id, outcome, reason),
    onSuccess: (tx) => {
      toast.success(tx.status === 'completed' ? 'Deposit credited' : 'Deposit closed as not received')
      setResolving(null)
      setDetail(tx)
      queryClient.invalidateQueries({ queryKey: ['admin'] })
    },
  })

  const update = (patch: Record<string, string>) => {
    const p = new URLSearchParams(params)
    for (const [k, v] of Object.entries(patch)) {
      if (v) p.set(k, v)
      else p.delete(k)
    }
    p.delete('page')
    setParams(p, { replace: true })
  }

  const columns: Column<AdminTransaction>[] = [
    {
      key: 'time',
      header: 'Time',
      render: (t) => (
        <span className={cn('text-muted', isStuck(t) && 'text-warning')}>
          {relativeTime(t.created_at)}
          {isStuck(t) ? (
            <span className="ml-1.5 text-[10px] uppercase" title="Open this row to resolve it">
              stuck?
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'type',
      header: 'Type',
      render: (t) => <span>{transactionTitle(t.type, t.metadata)}</span>,
    },
    {
      key: 'account',
      header: 'Account',
      render: (t) => (
        <Link
          to={`/admin/users/${t.user_id}`}
          onClick={(e) => e.stopPropagation()}
          className="tnum text-accent hover:underline"
        >
          {t.account_number}
        </Link>
      ),
    },
    {
      key: 'amount',
      header: 'Amount',
      align: 'right',
      render: (t) => <Money value={t.amount} signed />,
    },
    { key: 'status', header: 'Status', render: (t) => <StatusBadge status={t.status} /> },
    {
      key: 'admin',
      header: 'Admin',
      render: (t) => <span className="text-muted">{t.admin_username ?? '—'}</span>,
    },
  ]

  return (
    <>
      <AdminPageHeader title="Transactions" />

      <div className="mb-3 space-y-3">
        <SearchInput
          value={search}
          onChange={(v) => {
            setSearch(v)
            update({ search: v })
          }}
          placeholder="Reference, receipt, phone, account number or id"
        />
        <div className="flex flex-wrap gap-3">
          <FilterChips
            value={type}
            onChange={(v) => {
              setType(v)
              update({ type: v })
            }}
            options={[
              { label: 'All types', value: '' },
              { label: 'Deposits', value: 'deposit' },
              { label: 'Withdrawals', value: 'withdrawal' },
              { label: 'Trades', value: 'trade_close' },
              { label: 'Adjustments', value: 'admin_adjustment' },
            ]}
          />
          <FilterChips
            value={status}
            onChange={(v) => {
              setStatus(v)
              update({ status: v })
            }}
            options={[
              { label: 'Any status', value: '' },
              { label: 'Pending', value: 'pending' },
              { label: 'Completed', value: 'completed' },
              { label: 'Failed', value: 'failed' },
            ]}
          />
        </div>
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(t) => t.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        onRowClick={setDetail}
        empty={<p className="text-center text-body text-muted">No transactions match</p>}
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

      <Sheet
        open={detail !== null}
        onOpenChange={(open) => !open && setDetail(null)}
        title="Transaction"
      >
        {detail ? (
          <div className="space-y-3">
            <div className="text-center">
              <p className="text-title font-bold">
                <Money value={detail.amount} signed />
              </p>
              <p className="mt-1 text-body text-muted">
                {transactionTitle(detail.type, detail.metadata)}
              </p>
            </div>

            <div className="divide-y divide-hairline">
              <Row label="Status" value={<StatusBadge status={detail.status} />} />
              <Row label="When" value={absoluteTime(detail.created_at)} />
              <Row
                label="Account"
                value={
                  <Link to={`/admin/users/${detail.user_id}`} className="text-accent hover:underline">
                    {detail.account_number} ({detail.account_type})
                  </Link>
                }
              />
              <Row label="User" value={<span className="tnum">+{detail.user_phone}</span>} />
              <Row label="Balance" value={detail.balance_type === 'bonus' ? 'Bonus' : 'Real'} />
              {detail.admin_username ? (
                <Row
                  label={detail.type === 'deposit' ? 'Resolved by' : 'Adjusted by'}
                  value={detail.admin_username}
                />
              ) : null}
              {detail.type === 'deposit' && detail.status !== 'pending' ? (
                // A deposit settled without a receipt says how, rather than
                // looking like a callback that lost one.
                <Row
                  label="Settled by"
                  value={
                    SETTLED_BY[String(detail.metadata?.settled_by ?? '')] ??
                    (detail.metadata?.settled_by ? String(detail.metadata.settled_by) : '—')
                  }
                />
              ) : null}
              {typeof detail.metadata?.receipt === 'string' ? (
                <Row label="M-Pesa receipt" value={<span className="tnum">{detail.metadata.receipt}</span>} />
              ) : null}
              {typeof detail.metadata?.checkout_request_id === 'string' ? (
                <Row
                  label="M-Pesa request"
                  value={<span className="tnum break-all">{detail.metadata.checkout_request_id}</span>}
                />
              ) : null}
              <Row
                label="Reference"
                value={<span className="tnum break-all">{detail.reference ?? detail.id}</span>}
              />
            </div>

            {isPendingDeposit(detail) ? (
              // The only way to close a deposit that never got its callback.
              // Not Adjust: an adjustment is a new row, and the deposit would
              // stay pending for a late callback to credit a second time.
              <div className="space-y-2 rounded-xl border border-border bg-elevated p-3">
                <p className="text-meta text-muted">
                  Still pending. The server is asking M-Pesa about it on a schedule and will credit
                  it if M-Pesa confirms the payment. If you have confirmed the outcome yourself —
                  with the customer and on Safaricom's portal — you can settle it here.
                </p>
                <div className="flex gap-2">
                  <Button block variant="outline" onClick={() => setResolving('completed')}>
                    Mark received
                  </Button>
                  <Button block variant="danger" onClick={() => setResolving('failed')}>
                    Mark not received
                  </Button>
                </div>
              </div>
            ) : null}

            <details className="rounded-xl border border-border bg-elevated px-3 py-2">
              <summary className="cursor-pointer text-meta text-muted">Raw metadata</summary>
              <pre className="mt-2 overflow-x-auto text-[11px] text-muted">
                {JSON.stringify(detail.metadata ?? {}, null, 2)}
              </pre>
            </details>
          </div>
        ) : null}
      </Sheet>

      {/* --- Resolve a stuck deposit --- */}
      <ReasonDialog
        open={resolving !== null && detail !== null}
        onOpenChange={(open) => !open && setResolving(null)}
        title={resolving === 'completed' ? 'Mark deposit as received?' : 'Mark deposit as not received?'}
        consequence={
          detail && resolving === 'completed' ? (
            <>
              Credits <strong className="text-text"><Money value={detail.amount} /></strong> to{' '}
              <strong className="text-text">{detail.account_number}</strong> now, as if M-Pesa had
              confirmed it. If M-Pesa's callback arrives later it will be ignored — this row is
              settled, so nothing can credit it twice. Do this only once Safaricom's records show
              the payment.
            </>
          ) : detail ? (
            <>
              Closes the deposit on{' '}
              <strong className="text-text">{detail.account_number}</strong> as failed. Nothing is
              credited, and the customer is told the deposit was closed by support. If M-Pesa later
              reports it paid, that callback is logged as an error and credits nothing — so be sure
              before closing it.
            </>
          ) : null
        }
        confirmLabel={resolving === 'completed' ? 'Credit deposit' : 'Close as not received'}
        tone={resolving === 'completed' ? 'safe' : 'danger'}
        pending={resolve.isPending}
        error={resolve.error}
        onConfirm={(reason) =>
          detail && resolving && resolve.mutate({ id: detail.id, outcome: resolving, reason })
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
