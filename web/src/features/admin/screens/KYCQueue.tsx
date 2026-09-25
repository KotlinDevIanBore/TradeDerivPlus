import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { CheckCircle2 } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { Sheet } from '@/components/ui/Sheet'
import { toast } from '@/components/ui/Toast'
import { relativeTime } from '@/lib/format'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { KYCBadge } from '../components/badges'
import { DataTable, type Column } from '../components/DataTable'
import { FilterChips } from '../components/SearchInput'
import { ReasonDialog } from '../components/ReasonDialog'
import type { KYCSubmission } from '../adminTypes'

export function AdminKYCQueue() {
  const [params, setParams] = useSearchParams()
  const queryClient = useQueryClient()
  const status = params.get('status') ?? 'pending'
  const page = Number(params.get('page') ?? 1)

  const [approving, setApproving] = useState<KYCSubmission | null>(null)
  const [rejecting, setRejecting] = useState<KYCSubmission | null>(null)

  const query = useQuery({
    queryKey: adminKeys.kyc(status, page),
    queryFn: () => adminApi.kyc(status, page),
  })

  const invalidate = () => {
    queryClient.invalidateQueries({ queryKey: ['admin', 'kyc'] })
    queryClient.invalidateQueries({ queryKey: ['admin', 'audit'] })
    queryClient.invalidateQueries({ queryKey: adminKeys.dashboard() })
  }

  const approve = useMutation({
    mutationFn: (id: string) => adminApi.approveKYC(id),
    onSuccess: (k) => {
      toast.success('Verification approved', `+${k.user_phone} can now withdraw`)
      setApproving(null)
      invalidate()
    },
  })

  const reject = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => adminApi.rejectKYC(id, reason),
    onSuccess: () => {
      toast.success('Verification rejected')
      setRejecting(null)
      invalidate()
    },
  })

  const columns: Column<KYCSubmission>[] = [
    {
      key: 'submitted',
      header: 'Submitted',
      render: (k) => <span className="text-muted">{relativeTime(k.submitted_at)}</span>,
    },
    {
      key: 'user',
      header: 'User',
      render: (k) => (
        <Link to={`/admin/users/${k.user_id}`} className="text-accent hover:underline">
          <span className="block max-w-[180px] truncate">{k.user_full_name ?? '—'}</span>
          <span className="text-meta text-muted tnum">+{k.user_phone}</span>
        </Link>
      ),
    },
    { key: 'id_number', header: 'ID number', render: (k) => <span className="tnum">{k.id_number || '—'}</span> },
    {
      key: 'document',
      header: 'Document',
      render: (k) =>
        // A key is not a URL and must never be rendered as a link. Viewing is a
        // separate request that mints a short-lived signed URL and audits the
        // view; that control arrives with the upload flow. Until then the key is
        // shown as the opaque string it is.
        k.document_key ? (
          <span className="font-mono text-meta text-muted" title={k.document_key}>
            {k.document_key.length > 24 ? `${k.document_key.slice(0, 24)}…` : k.document_key}
          </span>
        ) : (
          <span className="italic text-faint">None</span>
        ),
    },
    { key: 'status', header: 'Status', render: (k) => <KYCBadge status={k.status} /> },
    {
      key: 'reviewer',
      header: 'Reviewed by',
      render: (k) => (
        <span className="text-muted">
          {k.reviewer_name ?? '—'}
          {k.reviewed_at ? (
            <span className="block text-meta text-faint">{relativeTime(k.reviewed_at)}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'actions',
      header: '',
      align: 'right',
      render: (k) =>
        k.status === 'pending' ? (
          <span className="flex justify-end gap-2" onClick={(e) => e.stopPropagation()}>
            <Button
              size="sm"
              variant="outline"
              className="border-profit/50 text-profit hover:bg-profit/10"
              onClick={() => setApproving(k)}
            >
              Approve
            </Button>
            <Button size="sm" variant="danger" onClick={() => setRejecting(k)}>
              Reject
            </Button>
          </span>
        ) : null,
    },
  ]

  return (
    <>
      <AdminPageHeader title="KYC queue" />

      <div className="mb-3">
        <FilterChips
          value={status}
          onChange={(v) => {
            const p = new URLSearchParams(params)
            p.set('status', v)
            p.delete('page')
            setParams(p, { replace: true })
          }}
          options={[
            { label: 'Pending', value: 'pending' },
            { label: 'Approved', value: 'approved' },
            { label: 'Rejected', value: 'rejected' },
            { label: 'All', value: 'all' },
          ]}
        />
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(k) => k.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        empty={
          status === 'pending' ? (
            // The empty queue is the good state, so it should look like one.
            <div className="flex flex-col items-center gap-2 text-center">
              <CheckCircle2 className="h-7 w-7 text-profit" aria-hidden />
              <p className="text-body text-text">Nothing waiting for review</p>
            </div>
          ) : (
            <p className="text-center text-body text-muted">No submissions here</p>
          )
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

      <Sheet
        open={approving !== null}
        onOpenChange={(open) => !open && setApproving(null)}
        title="Approve verification?"
      >
        <p className="text-body text-muted">
          This enables withdrawals on{' '}
          <strong className="text-text tnum">+{approving?.user_phone}</strong>'s real account.
        </p>
        <div className="mt-6 space-y-2">
          <Button
            size="lg"
            block
            variant="outline"
            className="border-profit/50 text-profit hover:bg-profit/10"
            loading={approve.isPending}
            onClick={() => approving && approve.mutate(approving.id)}
          >
            Approve
          </Button>
          <Button variant="ghost" block onClick={() => setApproving(null)}>
            Cancel
          </Button>
        </div>
      </Sheet>

      <ReasonDialog
        open={rejecting !== null}
        onOpenChange={(open) => !open && setRejecting(null)}
        title="Reject verification?"
        consequence={
          <>
            <strong className="text-text tnum">+{rejecting?.user_phone}</strong> stays unable to
            withdraw. The reason is recorded and should say what they need to fix.
          </>
        }
        confirmLabel="Reject"
        pending={reject.isPending}
        error={reject.error}
        onConfirm={(reason) => rejecting && reject.mutate({ id: rejecting.id, reason })}
      />
    </>
  )
}
