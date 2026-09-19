import { useSearchParams } from 'react-router-dom'
import { Link } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { absoluteTime, money, relativeTime } from '@/lib/format'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { ActionBadge } from '../components/badges'
import { DataTable, type Column } from '../components/DataTable'
import { FilterChips } from '../components/SearchInput'
import type { AuditEntry } from '../adminTypes'

/**
 * Read-only and append-only. There is no search box on purpose: the audit log
 * is read top to bottom, and a search field would suggest some rows matter
 * less than others.
 */
export function AdminAuditLog() {
  const [params, setParams] = useSearchParams()
  const action = params.get('action') ?? ''
  const page = Number(params.get('page') ?? 1)

  const query = useQuery({
    queryKey: adminKeys.audit({ action, page }),
    queryFn: () => adminApi.auditLog({ action, page }),
  })

  const columns: Column<AuditEntry>[] = [
    {
      key: 'time',
      header: 'When',
      render: (e) => (
        <span className="text-muted" title={absoluteTime(e.created_at)}>
          {relativeTime(e.created_at)}
        </span>
      ),
    },
    { key: 'admin', header: 'Admin', render: (e) => <span>{e.admin_username}</span> },
    { key: 'action', header: 'Action', render: (e) => <ActionBadge action={e.action} /> },
    {
      key: 'target',
      header: 'Target',
      render: (e) => {
        const accountNumber = e.metadata?.account_number as string | undefined
        if (e.target_type === 'user') {
          return (
            <Link to={`/admin/users/${e.target_id}`} className="text-accent hover:underline">
              User
            </Link>
          )
        }
        return <span className="tnum text-muted">{accountNumber ?? e.target_type}</span>
      },
    },
    {
      key: 'reason',
      header: 'Reason',
      render: (e) => <span className="block max-w-[340px] text-muted">{e.reason || '—'}</span>,
    },
    {
      key: 'details',
      header: 'Details',
      align: 'right',
      render: (e) => {
        const amount = e.metadata?.amount as string | undefined
        const balanceType = e.metadata?.balance_type as string | undefined
        if (!amount) return <span className="text-faint">—</span>
        return (
          <span className="text-muted tnum">
            {money(amount)} {balanceType}
          </span>
        )
      },
    },
  ]

  return (
    <>
      <AdminPageHeader title="Audit log" />

      <div className="mb-3">
        <FilterChips
          value={action}
          onChange={(v) => {
            const p = new URLSearchParams(params)
            if (v) p.set('action', v)
            else p.delete('action')
            p.delete('page')
            setParams(p, { replace: true })
          }}
          options={[
            { label: 'All actions', value: '' },
            { label: 'Freezes', value: 'freeze_account' },
            { label: 'Unfreezes', value: 'unfreeze_account' },
            { label: 'Adjustments', value: 'manual_adjustment' },
            { label: 'KYC approvals', value: 'kyc_approve' },
            { label: 'KYC rejections', value: 'kyc_reject' },
          ]}
        />
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(e) => e.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        empty={<p className="text-center text-body text-muted">No admin actions recorded yet</p>}
        page={query.data?.page ?? page}
        pages={query.data?.pages ?? 0}
        total={query.data?.total ?? 0}
        limit={query.data?.limit ?? 50}
        onPageChange={(next) => {
          const p = new URLSearchParams(params)
          p.set('page', String(next))
          setParams(p, { replace: true })
        }}
      />
    </>
  )
}
