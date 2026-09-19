import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { absoluteTime, relativeTime, transactionTitle } from '@/lib/format'
import { cn } from '@/lib/cn'
import { Sheet } from '@/components/ui/Sheet'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { DataTable, type Column } from '../components/DataTable'
import { FilterChips, SearchInput } from '../components/SearchInput'
import type { AdminTransaction } from '../adminTypes'

/** A deposit pending longer than this is worth flagging to support. */
const STUCK_AFTER_MS = 10 * 60_000

function isStuck(t: AdminTransaction) {
  return (
    t.type === 'deposit' &&
    t.status === 'pending' &&
    Date.now() - new Date(t.created_at).getTime() > STUCK_AFTER_MS
  )
}

export function AdminTransactions() {
  const [params, setParams] = useSearchParams()
  const [search, setSearch] = useState(params.get('search') ?? '')
  const [type, setType] = useState(params.get('type') ?? '')
  const [status, setStatus] = useState(params.get('status') ?? '')
  const [detail, setDetail] = useState<AdminTransaction | null>(null)
  const page = Number(params.get('page') ?? 1)

  const query = useQuery({
    queryKey: adminKeys.transactions({ search, type, status, page }),
    queryFn: () => adminApi.transactions({ search, type, status, page }),
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
          {isStuck(t) ? <span className="ml-1.5 text-[10px] uppercase">stuck?</span> : null}
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
                <Row label="Adjusted by" value={detail.admin_username} />
              ) : null}
              <Row
                label="Reference"
                value={<span className="tnum break-all">{detail.reference ?? detail.id}</span>}
              />
            </div>

            <details className="rounded-xl border border-border bg-elevated px-3 py-2">
              <summary className="cursor-pointer text-meta text-muted">Raw metadata</summary>
              <pre className="mt-2 overflow-x-auto text-[11px] text-muted">
                {JSON.stringify(detail.metadata ?? {}, null, 2)}
              </pre>
            </details>
          </div>
        ) : null}
      </Sheet>
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
