import { cn } from '@/lib/cn'
import type { AccountStatus } from '@/types'
import type { AuditAction } from '../adminTypes'

export function AccountStatusBadge({ status, className }: { status: AccountStatus; className?: string }) {
  if (status === 'active') {
    return (
      <span className={cn('rounded-pill border border-border px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-muted', className)}>
        active
      </span>
    )
  }
  return (
    <span className={cn('rounded-pill border border-accent/50 px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-accent', className)}>
      frozen
    </span>
  )
}

const kycTone: Record<string, string> = {
  pending: 'border-warning/40 text-warning',
  approved: 'border-profit/40 text-profit',
  verified: 'border-profit/40 text-profit',
  rejected: 'border-accent/40 text-accent',
}

export function KYCBadge({ status, className }: { status: string; className?: string }) {
  return (
    <span
      className={cn(
        'inline-flex rounded-pill border px-2 py-0.5 text-[10px] font-medium capitalize',
        kycTone[status] ?? 'border-border text-muted',
        className,
      )}
    >
      {status}
    </span>
  )
}

// Destructive and money-moving actions carry the accent; the reversals are
// green. Reading down the audit log, colour alone says what kind of day it was.
const actionTone: Record<AuditAction, string> = {
  freeze_account: 'border-accent/40 text-accent',
  unfreeze_account: 'border-profit/40 text-profit',
  manual_adjustment: 'border-accent/40 text-accent',
  kyc_approve: 'border-profit/40 text-profit',
  kyc_reject: 'border-accent/40 text-accent',
  // Muted, unlike the rest: looking at a document changes nothing and moves no
  // money. It is logged because it is access to the most sensitive data here, not
  // because it is the kind of row that should colour a day.
  kyc_document_view: 'border-border text-muted',
  // Both stop someone trading, so both read as destructive. The kill switch is
  // the platform-wide one and is not distinguished by colour — the label is what
  // separates them, and colour is for the kind of day, not the blast radius.
  bot_kill: 'border-accent/40 text-accent',
  bot_run_stop: 'border-accent/40 text-accent',
  // Green, like unfreeze: reading down the log, this is the row that says the
  // outage ended.
  bot_enable: 'border-profit/40 text-profit',
  // Money moves, on an operator's word rather than Safaricom's. Accent, like
  // an adjustment.
  deposit_resolve: 'border-accent/40 text-accent',
  // Money either stays out or comes back on an operator's word. Accent, like the
  // other two.
  payout_resolve: 'border-accent/40 text-accent',
}

const actionLabel: Record<AuditAction, string> = {
  freeze_account: 'Froze account',
  unfreeze_account: 'Unfroze account',
  manual_adjustment: 'Adjusted balance',
  kyc_approve: 'Approved KYC',
  kyc_reject: 'Rejected KYC',
  kyc_document_view: 'Viewed an ID document',
  bot_kill: 'Stopped all auto runs',
  bot_enable: 'Turned auto runs back on',
  bot_run_stop: 'Stopped an auto run',
  deposit_resolve: 'Resolved a deposit',
  payout_resolve: 'Resolved a payout',
}

export function ActionBadge({ action }: { action: AuditAction }) {
  return (
    <span
      className={cn(
        'inline-flex whitespace-nowrap rounded-pill border px-2 py-0.5 text-[10px] font-medium',
        actionTone[action] ?? 'border-border text-muted',
      )}
    >
      {actionLabel[action] ?? action}
    </span>
  )
}
