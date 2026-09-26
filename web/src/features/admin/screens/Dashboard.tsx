import { useState } from 'react'
import { Link } from 'react-router-dom'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { AlertTriangle, CheckCircle2 } from 'lucide-react'
import { ErrorState } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Skeleton } from '@/components/ui/Skeleton'
import { toast } from '@/components/ui/Toast'
import { cn } from '@/lib/cn'
import { Tile } from '../components/Tile'
import { edgeNoisePP, edgePercent } from './Finance'
import { money, relativeTime, signedMoney } from '@/lib/format'
import { adminApi, adminKeys } from '../adminApi'
import { AdminPageHeader } from '../AdminShell'
import { ActionBadge } from '../components/badges'
import { ReasonDialog } from '../components/ReasonDialog'
import type { AccountDrift, HourlyDeposits } from '../adminTypes'


/**
 * The reconciliation tile. Zero is the healthy state and reads quietly; any
 * other number means a balance changed outside a transaction that also wrote
 * the ledger, which is the one class of bug that silently destroys money. It
 * turns red and pins above everything else.
 */
function ReconciliationTile({ count, drift }: { count: number; drift: AccountDrift[] }) {
  if (count === 0) {
    return (
      <div className="flex items-center gap-2 rounded-card border border-profit/30 bg-profit/[0.05] px-4 py-3">
        <CheckCircle2 className="h-4 w-4 shrink-0 text-profit" aria-hidden />
        <p className="text-body text-text">
          Ledger reconciles. <span className="text-muted">No account has drifted.</span>
        </p>
      </div>
    )
  }

  return (
    <div className="rounded-card border border-accent bg-accent-subtle px-4 py-3">
      <div className="flex items-center gap-2">
        <AlertTriangle className="h-4 w-4 shrink-0 text-accent" aria-hidden />
        <p className="text-body font-semibold text-text">
          {count} account{count === 1 ? '' : 's'} drifted from the ledger
        </p>
      </div>
      <p className="mt-1 text-meta text-muted">
        A balance changed without a matching ledger row. Investigate before anything else.
      </p>
      <ul className="mt-3 space-y-1">
        {drift.slice(0, 5).map((d) => (
          <li key={d.account_id} className="flex items-center justify-between gap-3 text-meta tnum">
            <Link to={`/admin/transactions?search=${d.account_number}`} className="text-accent hover:underline">
              {d.account_number}
            </Link>
            <span className="text-muted">
              ledger {money(d.ledger_total)} vs cache {money(d.cached_total)}
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}

/** Deposit success versus failure per hour. A Daraja outage shows up here. */
function DepositChart({ data }: { data: HourlyDeposits[] }) {
  const peak = Math.max(1, ...data.map((d) => d.completed + d.failed))

  return (
    <div className="rounded-card border border-border bg-surface px-4 py-3">
      <div className="flex items-baseline justify-between">
        <h2 className="text-card font-semibold">Deposits, last 24 hours</h2>
        <span className="flex items-center gap-3 text-meta text-muted">
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-profit" /> completed
          </span>
          <span className="flex items-center gap-1">
            <span className="h-2 w-2 rounded-sm bg-accent" /> failed
          </span>
        </span>
      </div>

      <div className="mt-4 flex h-[120px] items-end gap-1">
        {data.map((d) => {
          const total = d.completed + d.failed
          return (
            <div
              key={d.hour}
              className="flex flex-1 flex-col justify-end gap-px"
              title={`${new Date(d.hour).getHours()}:00 — ${d.completed} completed, ${d.failed} failed`}
            >
              {d.failed > 0 ? (
                <div className="rounded-t-sm bg-accent" style={{ height: `${(d.failed / peak) * 100}%` }} />
              ) : null}
              {d.completed > 0 ? (
                <div
                  className={cn('bg-profit', d.failed === 0 && 'rounded-t-sm')}
                  style={{ height: `${(d.completed / peak) * 100}%` }}
                />
              ) : null}
              {total === 0 ? <div className="h-px bg-border" /> : null}
            </div>
          )
        })}
      </div>
    </div>
  )
}

/**
 * What the operator is about to do, in one sentence. Pulled out of the dialog
 * because it is the part with decisions in it — whether anything is running, and
 * how to count it — and those are worth checking without a query client.
 */
export function killConsequence(liveRuns: number): string {
  const runs =
    liveRuns === 0
      ? 'No runs are live right now.'
      : `${liveRuns} live run${liveRuns === 1 ? '' : 's'} will stop after the contract each has in flight settles.`
  return `${runs} Automated trading will also be turned off, so nobody can start a new run until it is turned back on.`
}

/**
 * The way back, and the one thing about it an operator could get wrong: turning
 * the feature on does not restart anything. Runs never resume — not across a
 * restart and not across the kill switch — so what this restores is the ability
 * to start a run, not the runs that were stopped.
 */
export const ENABLE_CONSEQUENCE =
  'Users will be able to start auto runs again. Runs the kill switch stopped stay stopped — nothing resumes on its own.'

/**
 * One control, reading the switch's current state.
 *
 * Both directions are audited and both ask for a written reason. The reason on
 * the way back is the one that carries the incident: a kill that says why,
 * followed by a restart that says nothing, leaves what changed in between
 * unanswerable.
 */
function BotSwitch({ liveRuns, enabled }: { liveRuns: number; enabled: boolean }) {
  const [open, setOpen] = useState(false)
  const queryClient = useQueryClient()

  const invalidate = () => {
    setOpen(false)
    // Every admin read is stale after this: the tile, any runs table, and the
    // audit log that now has a row in it.
    queryClient.invalidateQueries({ queryKey: ['admin'] })
  }

  const kill = useMutation({
    mutationFn: (reason: string) => adminApi.killBots(reason),
    onSuccess: (result) => {
      toast.success(
        result.runs_stopped === 1
          ? '1 auto run stopped'
          : `${result.runs_stopped} auto runs stopped`,
        'Automated trading is off until it is turned back on.',
      )
      invalidate()
    },
  })

  const enable = useMutation({
    mutationFn: (reason: string) => adminApi.enableBots(reason),
    onSuccess: () => {
      toast.success('Automated trading is on', 'Users can start auto runs again.')
      invalidate()
    },
  })

  const pending = kill.isPending || enable.isPending

  return (
    <>
      <Button
        variant={enabled ? 'danger' : 'outline'}
        size="sm"
        className={enabled ? undefined : 'border-profit/50 text-profit hover:bg-profit/10'}
        onClick={() => setOpen(true)}
      >
        {enabled ? 'Stop all auto runs' : 'Turn auto runs back on'}
      </Button>

      <ReasonDialog
        open={open}
        onOpenChange={setOpen}
        title={enabled ? 'Stop all auto runs' : 'Turn auto runs back on?'}
        consequence={enabled ? killConsequence(liveRuns) : ENABLE_CONSEQUENCE}
        confirmLabel={enabled ? 'Stop all auto runs' : 'Turn auto runs on'}
        tone={enabled ? 'danger' : 'safe'}
        pending={pending}
        error={kill.error ?? enable.error}
        onConfirm={(reason) => (enabled ? kill : enable).mutate(reason)}
      />
    </>
  )
}

export function AdminDashboard() {
  const dash = useQuery({
    queryKey: adminKeys.dashboard(),
    queryFn: () => adminApi.dashboard(),
    refetchInterval: 30_000,
  })

  const audit = useQuery({
    queryKey: adminKeys.audit({ recent: true }),
    queryFn: () => adminApi.auditLog({ page: 1 }),
  })

  if (dash.isError) {
    return (
      <>
        <AdminPageHeader title="Dashboard" />
        <ErrorState error={dash.error} onRetry={() => dash.refetch()} />
      </>
    )
  }

  const d = dash.data

  return (
    <>
      <AdminPageHeader
        title="Dashboard"
        action={
          dash.isLoading ? null : (
            <BotSwitch liveRuns={d!.live_bot_runs} enabled={d!.bot_enabled} />
          )
        }
      />

      {dash.isLoading ? (
        <Skeleton className="h-[52px] w-full rounded-card" />
      ) : (
        <ReconciliationTile count={d!.drifted_accounts} drift={d!.drift} />
      )}

      <div className="mt-3 grid grid-cols-2 gap-3 lg:grid-cols-4">
        {dash.isLoading
          ? Array.from({ length: 6 }).map((_, i) => (
              <Skeleton key={i} className="h-[76px] rounded-card" />
            ))
          : [
              { label: 'Users', value: d!.users.toLocaleString() },
              {
                label: 'Deposits today',
                value: d!.deposits_today.toLocaleString(),
                sub: money(d!.deposits_today_usd),
              },
              {
                label: 'Withdrawals today',
                value: d!.withdrawals_today.toLocaleString(),
                sub: money(d!.withdrawals_today_usd),
              },
              { label: 'Open positions', value: d!.open_positions.toLocaleString() },
              { label: 'Live connections', value: d!.live_connections.toLocaleString() },
              {
                label: 'Live auto runs',
                // A zero means two different things — nobody is running one, or
                // nobody can — and the sub-line is what separates them.
                className: d!.bot_enabled ? undefined : 'border-warning/40',
                // Linked only when there is something to look at, so the tile
                // does not invite a click that lands on an empty table.
                value:
                  d!.live_bot_runs > 0 ? (
                    <Link to="/admin/bot-runs" className="text-accent hover:underline">
                      {d!.live_bot_runs.toLocaleString()}
                    </Link>
                  ) : (
                    d!.live_bot_runs.toLocaleString()
                  ),
                sub: !d!.bot_enabled
                  ? 'automated trading is off'
                  : d!.live_bot_runs > 0
                    ? 'placing contracts now'
                    : undefined,
              },
              // The two finance tiles. Absent rather than zero when the
              // aggregate failed, so an empty dashboard never reads as "the
              // house made nothing". Labelled 24h, not "today", because that is
              // the window they actually cover and the screen they link to.
              ...(d?.finance
                ? [
                    {
                      label: 'House P&L · 24h',
                      value: (
                        <Link to="/admin/finance?range=24h" className="text-accent hover:underline">
                          {signedMoney(d.finance.house_pnl_24h)}
                        </Link>
                      ),
                      sub: `${money(d.finance.stakes_24h)} staked, real money`,
                    },
                    {
                      label: 'Edge · 24h',
                      value: (
                        <Link to="/admin/finance?range=24h" className="text-accent hover:underline">
                          {d.finance.edge.too_few_to_read
                            ? '—'
                            : edgePercent(d.finance.edge.realised)}
                        </Link>
                      ),
                      sub: d.finance.edge.too_few_to_read
                        ? `${d.finance.edge.sample_size} of ${d.finance.edge.min_sample} contracts`
                        : `${edgeNoisePP(d.finance.edge.sample_size)} at n=${d.finance.edge.sample_size}`,
                    },
                  ]
                : []),
            ].map((t) => <Tile key={t.label} {...t} />)}
      </div>

      <div className="mt-3 grid gap-3 lg:grid-cols-[1fr_360px]">
        {dash.isLoading ? (
          <Skeleton className="h-[200px] rounded-card" />
        ) : (
          <DepositChart data={d!.deposits_by_hour} />
        )}

        <section className="rounded-card border border-border bg-surface px-4 py-3">
          <div className="flex items-baseline justify-between">
            <h2 className="text-card font-semibold">Recent admin actions</h2>
            <Link to="/admin/audit-log" className="text-meta font-medium text-accent hover:underline">
              See all
            </Link>
          </div>

          {audit.isLoading ? (
            <div className="mt-3 space-y-2">
              {Array.from({ length: 5 }).map((_, i) => (
                <Skeleton key={i} className="h-8 w-full" />
              ))}
            </div>
          ) : (audit.data?.items.length ?? 0) === 0 ? (
            <p className="mt-4 text-meta text-muted">No admin actions recorded yet.</p>
          ) : (
            <ul className="mt-3 divide-y divide-hairline">
              {audit.data!.items.slice(0, 8).map((e) => (
                <li key={e.id} className="flex items-center gap-2 py-2">
                  <ActionBadge action={e.action} />
                  <span className="min-w-0 flex-1 truncate text-meta text-muted">
                    {e.admin_username}
                  </span>
                  <span className="shrink-0 text-meta text-faint">{relativeTime(e.created_at)}</span>
                </li>
              ))}
            </ul>
          )}
        </section>
      </div>
    </>
  )
}
