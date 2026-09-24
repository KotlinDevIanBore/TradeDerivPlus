import { Money } from '@/components/Money'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'
import { money, relativeTime } from '@/lib/format'
import { dec } from '@/lib/money'
import type { BotRun, DigitContract } from '@/types'
import { contractHeadline, selectionLabel } from './digitLabels'

/**
 * A run while it is running: what it is doing, how it is going, and how to stop
 * it.
 *
 * The figures come from the run row rather than being counted here, because the
 * server writes them inside the transaction that settles each contract — so they
 * agree with the contracts and the ledger by construction, and counting again on
 * the client could only introduce a disagreement.
 */
export function BotRunCard({
  run,
  lastContract,
  stopping,
  onStop,
  flushTop = false,
}: {
  run: BotRun
  /** The most recently settled contract of this run, if one has settled yet. */
  lastContract?: DigitContract
  /** A stop is in flight, as opposed to the run having reached 'stopping'. */
  stopping: boolean
  onStop: () => void
  flushTop?: boolean
}) {
  const realised = dec(run.realised_pnl)
  const stopLoss = dec(run.limits.stop_loss)

  // How close the run is to its floor, as a fraction. Only losses count: a run in
  // profit is nowhere near its stop loss, and showing a bar creeping backwards
  // would suggest otherwise.
  const towardsStopLoss =
    realised.isNegative() && stopLoss.greaterThan(0)
      ? Math.min(100, realised.abs().div(stopLoss).mul(100).toNumber())
      : 0
  const nearStopLoss = towardsStopLoss >= 75

  // 'stopping' is the server's state — a stop was asked for and a contract is
  // still in flight. `stopping` the prop is the request being sent.
  const isStopping = run.status === 'stopping' || stopping

  return (
    <div
      className={cn(
        'bg-surface p-4 lg:rounded-card lg:border lg:border-border',
        flushTop ? 'pt-2' : 'rounded-t-3xl border-t border-border',
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="flex items-center gap-2 text-card font-semibold">
            <span
              aria-hidden
              className={cn(
                'inline-block h-2 w-2 shrink-0 rounded-full',
                isStopping ? 'bg-warning' : 'animate-pulse bg-profit',
              )}
            />
            <span className="truncate">
              AUTO · {run.symbol_code} · {selectionLabel(run.contract_type, run.selection)}
            </span>
          </p>
          <p className="mt-0.5 text-meta text-muted tnum">
            {money(run.stake)} every {formatInterval(run.interval_ms)} · started{' '}
            {relativeTime(run.started_at)}
          </p>
        </div>
      </div>

      <dl className="mt-3 grid grid-cols-3 gap-x-3 gap-y-2">
        <Figure label="Placed">
          <span className="tnum">
            {run.contracts_placed}
            {run.limits.max_contracts ? (
              <span className="text-muted">/{run.limits.max_contracts}</span>
            ) : null}
          </span>
        </Figure>
        <Figure label="W / L / V">
          <span className="tnum">
            {run.wins} / {run.losses} / {run.voids}
          </span>
        </Figure>
        <Figure label="Realised">
          <Money value={run.realised_pnl} signed className="text-body font-semibold" />
        </Figure>
      </dl>

      <div className="mt-3">
        <div className="flex items-baseline justify-between text-meta text-muted">
          <span>Towards stop loss</span>
          <span className="tnum">
            {money(realised.isNegative() ? realised.abs().toString() : '0')} of{' '}
            {money(run.limits.stop_loss)}
          </span>
        </div>
        <div
          className="mt-1 h-1.5 w-full overflow-hidden rounded-full bg-elevated"
          role="progressbar"
          aria-label="Towards stop loss"
          aria-valuemin={0}
          aria-valuemax={100}
          aria-valuenow={Math.round(towardsStopLoss)}
        >
          <div
            className={cn(
              'h-full rounded-full transition-all',
              nearStopLoss ? 'bg-accent' : 'bg-warning',
            )}
            style={{ width: `${towardsStopLoss}%` }}
          />
        </div>
      </div>

      {lastContract ? (
        <p className="mt-3 border-t border-border pt-3 text-meta text-muted">
          Last: <span className="text-text">{contractHeadline(lastContract)}</span>{' '}
          <span className={cn('font-semibold', outcomeTone(lastContract.status))}>
            {outcomeWord(lastContract.status)}
          </span>
          {lastContract.settlement_digit !== null && lastContract.settlement_digit !== undefined
            ? ` · digit ${lastContract.settlement_digit}`
            : ''}
          {lastContract.settled_at ? ` · ${relativeTime(lastContract.settled_at)}` : ''}
        </p>
      ) : null}

      <Button
        block
        size="lg"
        variant="danger"
        className="mt-4"
        disabled={isStopping}
        loading={stopping}
        onClick={onStop}
      >
        {isStopping ? 'Stopping…' : 'Stop run'}
      </Button>

      {run.status === 'stopping' ? (
        <p className="mt-2 text-center text-nano text-warning">
          Stopping after the current contract settles.
        </p>
      ) : (
        <p className="mt-2 text-center text-nano text-faint">
          Stops on its own when a limit is reached.
        </p>
      )}
    </div>
  )
}

function Figure({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div>
      <dt className="text-nano font-semibold uppercase tracking-wide text-faint">{label}</dt>
      <dd className="mt-0.5 text-body font-semibold">{children}</dd>
    </div>
  )
}

function outcomeWord(status: DigitContract['status']): string {
  switch (status) {
    case 'won':
      return 'won'
    case 'lost':
      return 'lost'
    case 'void':
      return 'voided'
    default:
      return status
  }
}

function outcomeTone(status: DigitContract['status']): string {
  switch (status) {
    case 'won':
      return 'text-profit'
    case 'lost':
      return 'text-loss'
    default:
      return 'text-muted'
  }
}

function formatInterval(ms: number): string {
  const seconds = ms / 1000
  return Number.isInteger(seconds) ? `${seconds}s` : `${ms}ms`
}
