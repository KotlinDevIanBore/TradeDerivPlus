import { useEffect, useState } from 'react'
import { ApiError } from '@/api/client'
import { humanMessage } from '@/components/ErrorState'
import { Button } from '@/components/ui/Button'
import { Sheet } from '@/components/ui/Sheet'
import { cn } from '@/lib/cn'

/** The audit log is only useful if the reason is. Ten characters is the floor. */
const MIN_REASON = 10

interface ReasonDialogProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title: string
  /** One line stating exactly what will happen, in plain words. */
  consequence: React.ReactNode
  confirmLabel: string
  tone?: 'danger' | 'safe'
  /** Extra inputs above the reason field, e.g. amount and balance type. */
  children?: React.ReactNode
  /** Disable confirm for reasons the caller knows about. */
  disabled?: boolean
  pending?: boolean
  error?: unknown
  onConfirm: (reason: string) => void
  /** Approve does not need a written reason; reject and every money move does. */
  reasonRequired?: boolean
}

/**
 * Every admin action that touches money or account state goes through this.
 * It states the consequence, demands a written reason that lands in the audit
 * log, and colours the confirm button by how destructive the action is.
 */
export function ReasonDialog({
  open,
  onOpenChange,
  title,
  consequence,
  confirmLabel,
  tone = 'danger',
  children,
  disabled,
  pending,
  error,
  onConfirm,
  reasonRequired = true,
}: ReasonDialogProps) {
  const [reason, setReason] = useState('')

  useEffect(() => {
    if (!open) setReason('')
  }, [open])

  const tooShort = reasonRequired && reason.trim().length < MIN_REASON
  const fieldError = error instanceof ApiError && error.field === 'reason' ? error.message : undefined

  return (
    <Sheet open={open} onOpenChange={onOpenChange} title={title}>
      <p className="text-body text-muted">{consequence}</p>

      {children ? <div className="mt-4 space-y-3">{children}</div> : null}

      <div className="mt-4">
        <label htmlFor="admin-reason" className="mb-1.5 block text-meta font-medium text-muted">
          Reason {reasonRequired ? '' : '(optional)'}
        </label>
        <textarea
          id="admin-reason"
          rows={3}
          value={reason}
          onChange={(e) => setReason(e.target.value)}
          placeholder="This goes in the audit log. Say why."
          className={cn(
            'w-full resize-none rounded-xl border bg-elevated px-3 py-2 text-body text-text outline-none',
            'placeholder:text-faint focus:border-accent',
            fieldError ? 'border-accent' : 'border-border',
          )}
        />
        {fieldError ? (
          <p className="mt-1.5 text-meta text-accent">{fieldError}</p>
        ) : reasonRequired ? (
          <p className="mt-1.5 text-meta text-faint">
            {reason.trim().length < MIN_REASON
              ? `${MIN_REASON - reason.trim().length} more characters`
              : 'Recorded against your username'}
          </p>
        ) : null}
      </div>

      {error && !(error instanceof ApiError && error.field === 'reason') ? (
        <p role="alert" className="mt-3 text-meta text-accent">
          {humanMessage(error)}
        </p>
      ) : null}

      <div className="mt-6 space-y-2">
        <Button
          size="lg"
          block
          variant={tone === 'danger' ? 'danger' : 'outline'}
          className={tone === 'safe' ? 'border-profit/50 text-profit hover:bg-profit/10' : undefined}
          disabled={tooShort || disabled}
          loading={pending}
          onClick={() => onConfirm(reason.trim())}
        >
          {confirmLabel}
        </Button>
        <Button variant="ghost" block onClick={() => onOpenChange(false)}>
          Cancel
        </Button>
      </div>
    </Sheet>
  )
}
