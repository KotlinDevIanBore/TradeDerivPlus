import { Money } from '@/components/Money'
import { cn } from '@/lib/cn'
import { money, relativeTime } from '@/lib/format'
import type { DigitContract } from '@/types'
import { contractHeadline, contractTypeLabel } from '@/features/trading/digitLabels'

/**
 * Open digit contract — informational only. Settles on the next tick; no close.
 */
export function DigitOpenCard({ contract }: { contract: DigitContract }) {
  return (
    <div className="rounded-card border border-border bg-surface p-4">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <p className="truncate text-card font-semibold">{contractHeadline(contract)}</p>
          <p className="mt-0.5 text-meta text-muted">
            {contractTypeLabel(contract.contract_type)} · entry digit {contract.entry_digit}
          </p>
        </div>
        <div className="shrink-0 text-right">
          <p className="text-meta text-muted">Pays if wins</p>
          <p className="text-card font-bold tnum text-profit">{money(contract.potential_payout)}</p>
        </div>
      </div>

      <div className="mt-3 flex items-center justify-between gap-2 text-meta text-muted">
        <span className="flex items-center gap-2">
          <span className="tnum">{money(contract.stake)} stake</span>
          <BotChip contract={contract} />
        </span>
        <span title="Settles on the next tick after entry">Settles next tick</span>
      </div>
      <p className="mt-1 text-nano text-faint">{relativeTime(contract.opened_at)}</p>
    </div>
  )
}

/**
 * Settled digit contract row for Positions closed tab and History.
 * Void is visually distinct from loss — refund, not a lost stake.
 */
export function DigitSettledRow({ contract }: { contract: DigitContract }) {
  const badge = statusBadge(contract.status)
  return (
    <div className="flex items-center gap-3 px-4 py-3">
      <div className="min-w-0 flex-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-body font-semibold">{contractHeadline(contract)}</span>
          <span
            className={cn(
              'rounded-md px-1.5 py-0.5 text-nano font-semibold uppercase tracking-wide',
              badge.className,
            )}
          >
            {badge.label}
          </span>
          <BotChip contract={contract} />
        </div>
        <p className="mt-0.5 text-meta text-muted">
          {contractTypeLabel(contract.contract_type)}
          {contract.settlement_digit !== null && contract.settlement_digit !== undefined
            ? ` · settled on ${contract.settlement_digit}`
            : ''}
          {' · '}
          {money(contract.stake)} stake
        </p>
        <p className="text-nano text-faint">
          {contract.settled_at
            ? relativeTime(contract.settled_at)
            : relativeTime(contract.opened_at)}
        </p>
      </div>
      <div className="shrink-0 text-right">
        {contract.status === 'void' ? (
          <span className="text-body font-semibold text-muted">Refunded</span>
        ) : contract.pnl !== null ? (
          <Money value={contract.pnl} signed className="text-body font-semibold" />
        ) : (
          <span className="text-faint">—</span>
        )}
      </div>
    </div>
  )
}

/**
 * Marks a contract an automated run placed, in both the open card and the settled
 * row — so Positions and History both show it, since they share these two
 * components.
 *
 * Neutral rather than coloured: whose hand placed a contract says nothing about
 * how it went, and the row already uses colour for that.
 */
function BotChip({ contract }: { contract: DigitContract }) {
  if (!contract.bot_run_id) return null
  return (
    <span
      title="Placed by an automated run"
      className="rounded-md bg-elevated px-1.5 py-0.5 text-nano font-semibold uppercase tracking-wide text-muted"
    >
      Bot
    </span>
  )
}

function statusBadge(status: DigitContract['status']): { label: string; className: string } {
  switch (status) {
    case 'won':
      return { label: 'Won', className: 'bg-profit/15 text-profit' }
    case 'lost':
      return { label: 'Lost', className: 'bg-loss/15 text-loss' }
    case 'void':
      return { label: 'Void', className: 'bg-elevated text-muted' }
    default:
      return { label: status, className: 'bg-elevated text-muted' }
  }
}
