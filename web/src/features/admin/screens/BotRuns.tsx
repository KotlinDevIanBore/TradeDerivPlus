import { useState } from 'react'
import { Link, useSearchParams } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Money } from '@/components/Money'
import { StatusBadge } from '@/components/StatusBadge'
import { Button } from '@/components/ui/Button'
import { toast } from '@/components/ui/Toast'
import { contractTypeLabel, selectionLabel } from '@/features/trading/digitLabels'
import { money, relativeTime } from '@/lib/format'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { DataTable, type Column } from '../components/DataTable'
import { FilterChips } from '../components/SearchInput'
import { ReasonDialog } from '../components/ReasonDialog'
import type { AdminBotRun } from '../adminTypes'
import type { BotStopReason } from '@/types'

/**
 * A live run changes every couple of seconds. Ten is often enough to watch
 * placements climb without the table reloading under an operator's cursor while
 * they read a row.
 */
const REFETCH_MS = 10_000

/**
 * Console vocabulary, not the user's. The user is told "Auto run hit its stop
 * loss"; an operator scanning a column wants the two words that distinguish this
 * row from the one above it.
 */
const STOP_REASON: Record<BotStopReason, string> = {
  user: 'User stopped',
  stop_loss: 'Stop loss',
  take_profit: 'Take profit',
  max_consecutive_losses: 'Losing streak',
  max_contracts: 'Contract limit',
  max_duration: 'Time limit',
  admin_kill: 'Kill switch',
  admin_stop: 'Stopped here',
  disabled: 'Feature off',
  insufficient_funds: 'Out of funds',
  account_frozen: 'Account frozen',
  market_closed: 'Market closed',
  feed_down: 'Feed down',
  restart: 'Service restart',
  shutdown: 'Shutdown',
  error: 'Error',
}

/** The endings that mean something went wrong, rather than a limit being honoured. */
const FAULTS: BotStopReason[] = ['feed_down', 'error', 'insufficient_funds', 'account_frozen']

export function AdminBotRuns() {
  const [params, setParams] = useSearchParams()
  const [status, setStatus] = useState(params.get('status') ?? 'live')
  const [stopping, setStopping] = useState<AdminBotRun | null>(null)
  const page = Number(params.get('page') ?? 1)
  const queryClient = useQueryClient()

  const query = useQuery({
    queryKey: adminKeys.botRuns({ status, page }),
    queryFn: () => adminApi.botRuns({ status, page }),
    // Only while something can still change. A page of history is history.
    refetchInterval: status === 'stopped' ? false : REFETCH_MS,
  })

  const stop = useMutation({
    mutationFn: ({ id, reason }: { id: string; reason: string }) => adminApi.stopBotRun(id, reason),
    onSuccess: (run) => {
      toast.success(
        run.status === 'stopping' ? 'Run stopping' : 'Run stopped',
        run.status === 'stopping'
          ? 'It ends when the contract it has in flight settles.'
          : `${run.contracts_placed} contract${run.contracts_placed === 1 ? '' : 's'} placed, ${money(run.realised_pnl)} realised.`,
      )
      setStopping(null)
      queryClient.invalidateQueries({ queryKey: ['admin'] })
    },
  })

  const columns: Column<AdminBotRun>[] = [
    {
      key: 'started',
      header: 'Started',
      render: (r) => (
        <span className="whitespace-nowrap text-muted">{relativeTime(r.started_at)}</span>
      ),
    },
    {
      key: 'user',
      header: 'User',
      render: (r) => (
        <Link
          to={`/admin/users/${r.user_id}`}
          onClick={(e) => e.stopPropagation()}
          className="tnum text-accent hover:underline"
        >
          {r.user_phone}
        </Link>
      ),
    },
    {
      key: 'account',
      header: 'Account',
      render: (r) => (
        <span className="tnum text-muted">
          {r.account_number}
          {/* A real-money run should never be here while the flag is off, so
              saying which kind it is makes the wrong one obvious. */}
          {r.account_type === 'real' ? (
            <span className="ml-1.5 text-[10px] font-bold uppercase text-warning">real</span>
          ) : null}
        </span>
      ),
    },
    {
      // Symbol and contract are one column, not two. The table already has to
      // fit ten of them and a stop button, and an operator reads "R_75 Even/Odd
      // Even" as one fact about the run rather than three.
      key: 'contract',
      header: 'Contract',
      render: (r) => (
        <span className="whitespace-nowrap">
          <span className="tnum">{r.symbol_code}</span>
          <span className="ml-2">{contractTypeLabel(r.contract_type)}</span>
          <span className="ml-1.5 text-muted">{selectionLabel(r.contract_type, r.selection)}</span>
        </span>
      ),
    },
    {
      key: 'stake',
      header: 'Stake',
      align: 'right',
      render: (r) => (
        <span className="tnum">
          {money(r.stake)}
          <span className="ml-1 text-meta text-faint">/{Math.round(r.interval_ms / 1000)}s</span>
        </span>
      ),
    },
    {
      key: 'placed',
      header: 'Placed',
      align: 'right',
      render: (r) => (
        <span className="tnum">
          {r.contracts_placed}
          {r.limits.max_contracts ? <span className="text-faint">/{r.limits.max_contracts}</span> : null}
          <span className="ml-1.5 text-meta text-muted">
            {r.wins}/{r.losses}
            {r.voids > 0 ? `/${r.voids}` : ''}
          </span>
        </span>
      ),
    },
    {
      key: 'realised',
      header: 'Realised',
      align: 'right',
      render: (r) => <Money value={r.realised_pnl} signed />,
    },
    {
      // Status and reason share a column. Separately, "Reason" is an empty
      // column on the Live tab, where most of the looking happens, and the
      // width it costs is the width the stop button needed.
      key: 'status',
      header: 'Status',
      render: (r) => (
        <span className="flex items-center gap-2 whitespace-nowrap">
          <StatusBadge status={r.status} />
          {r.stop_reason ? (
            <span className={FAULTS.includes(r.stop_reason) ? 'text-accent' : 'text-muted'}>
              {STOP_REASON[r.stop_reason] ?? r.stop_reason}
            </span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'stop',
      header: '',
      align: 'right',
      render: (r) =>
        r.status === 'stopped' ? null : (
          <Button
            variant="outline"
            size="sm"
            className="border-accent/50 text-accent hover:bg-accent/10"
            onClick={(e) => {
              e.stopPropagation()
              setStopping(r)
            }}
          >
            Stop
          </Button>
        ),
    },
  ]

  return (
    <>
      <AdminPageHeader title="Auto runs" />

      <div className="mb-3">
        <FilterChips
          value={status}
          onChange={(v) => {
            setStatus(v)
            const p = new URLSearchParams(params)
            if (v) p.set('status', v)
            else p.delete('status')
            p.delete('page')
            setParams(p, { replace: true })
          }}
          options={[
            { label: 'Live', value: 'live' },
            { label: 'Finished', value: 'stopped' },
            { label: 'All', value: '' },
          ]}
        />
      </div>

      <DataTable
        columns={columns}
        rows={query.data?.items ?? []}
        rowKey={(r) => r.id}
        isLoading={query.isLoading}
        error={query.error}
        onRetry={() => query.refetch()}
        empty={
          <p className="text-center text-body text-muted">
            {status === 'live' ? 'No runs are live right now' : 'No runs to show'}
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

      <ReasonDialog
        open={stopping !== null}
        onOpenChange={(open) => !open && setStopping(null)}
        title="Stop this auto run?"
        consequence={stopping ? stopConsequence(stopping) : ''}
        confirmLabel="Stop run"
        pending={stop.isPending}
        error={stop.error}
        onConfirm={(reason) => stopping && stop.mutate({ id: stopping.id, reason })}
      />
    </>
  )
}

/**
 * What the operator is about to do, in one sentence, naming whose run it is.
 *
 * Two things it is careful about, for the same reason the kill switch's copy is:
 * a run holding a contract does not end when the button is pressed, and this
 * stops one run rather than the feature — the user can start another straight
 * away, which is the difference an operator reaching for the wrong control
 * needs to see before they press it.
 */
export function stopConsequence(run: Pick<AdminBotRun, 'user_phone' | 'account_number' | 'status'>): string {
  const whose = `${run.user_phone}'s run on account ${run.account_number}`
  const when =
    run.status === 'stopping'
      ? `${whose} is already stopping and will end when its contract settles.`
      : `${whose} will stop after any contract it has in flight settles.`
  return `${when} Automated trading stays on, and they can start another run.`
}
