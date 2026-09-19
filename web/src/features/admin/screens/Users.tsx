import { useState } from 'react'
import { useNavigate, useSearchParams } from 'react-router-dom'
import { useQuery } from '@tanstack/react-query'
import { AccountPill } from '@/components/AccountPill'
import { Money } from '@/components/Money'
import { relativeTime } from '@/lib/format'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { AccountStatusBadge, KYCBadge } from '../components/badges'
import { DataTable, type Column } from '../components/DataTable'
import { SearchInput } from '../components/SearchInput'
import type { AdminUserRow } from '../adminTypes'

export function AdminUsers() {
  const navigate = useNavigate()
  const [params, setParams] = useSearchParams()
  const [search, setSearch] = useState(params.get('search') ?? '')
  const page = Number(params.get('page') ?? 1)

  const query = useQuery({
    queryKey: adminKeys.users({ search, page }),
    queryFn: () => adminApi.users({ search, page }),
  })

  const setPage = (next: number) => {
    const p = new URLSearchParams(params)
    p.set('page', String(next))
    setParams(p, { replace: true })
  }

  const onSearch = (value: string) => {
    setSearch(value)
    const p = new URLSearchParams(params)
    if (value) p.set('search', value)
    else p.delete('search')
    p.delete('page')
    setParams(p, { replace: true })
  }

  const columns: Column<AdminUserRow>[] = [
    {
      key: 'name',
      header: 'Name',
      render: (u) => (
        <span className="block max-w-[200px] truncate">{u.full_name ?? '—'}</span>
      ),
    },
    {
      key: 'phone',
      header: 'Phone',
      render: (u) => <span className="tnum text-muted">+{u.phone_number}</span>,
    },
    { key: 'kyc', header: 'KYC', render: (u) => <KYCBadge status={u.kyc_status} /> },
    {
      key: 'accounts',
      header: 'Accounts',
      render: (u) => (
        <span className="flex flex-wrap items-center gap-2">
          {u.accounts.length === 0 ? <span className="text-faint">—</span> : null}
          {u.accounts.map((a) => (
            <span key={a.id} className="flex items-center gap-1">
              <AccountPill account={a} />
              {a.status === 'frozen' ? <AccountStatusBadge status="frozen" /> : null}
            </span>
          ))}
        </span>
      ),
    },
    {
      key: 'balance',
      header: 'Real balance',
      align: 'right',
      render: (u) => <Money value={u.real_balance} />,
    },
    {
      key: 'joined',
      header: 'Joined',
      align: 'right',
      render: (u) => <span className="text-muted">{relativeTime(u.created_at)}</span>,
    },
  ]

  return (
    <>
      <AdminPageHeader title="Users" />

      <div className="mb-3">
        <SearchInput
          value={search}
          onChange={onSearch}
          placeholder="Phone, email, name or account number"
        />
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(u) => u.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        onRowClick={(u) => navigate(`/admin/users/${u.id}`)}
        empty={
          <p className="text-center text-body text-muted">
            {search ? `No users match "${search}"` : 'No users yet'}
          </p>
        }
        page={query.data?.page ?? page}
        pages={query.data?.pages ?? 0}
        total={query.data?.total ?? 0}
        limit={query.data?.limit ?? 25}
        onPageChange={setPage}
      />
    </>
  )
}
