import { AlertTriangle } from 'lucide-react'
import { Button } from '@/components/ui/Button'
import { cn } from '@/lib/cn'
import { ApiError } from '@/api/client'

/**
 * One line and a retry. A raw error string never reaches the user: anything the
 * API did not phrase for a human becomes a generic sentence.
 */
export function ErrorState({
  error,
  onRetry,
  className,
  compact,
}: {
  error?: unknown
  onRetry?: () => void
  className?: string
  compact?: boolean
}) {
  return (
    <div
      className={cn(
        'flex flex-col items-center justify-center text-center',
        compact ? 'px-4 py-6' : 'px-6 py-10',
        className,
      )}
    >
      <AlertTriangle className="mb-2 h-6 w-6 text-accent" aria-hidden />
      <p className="text-body text-text">{humanMessage(error)}</p>
      {onRetry ? (
        <Button variant="outline" size="sm" className="mt-4" onClick={onRetry}>
          Try again
        </Button>
      ) : null}
    </div>
  )
}

/**
 * Turns anything thrown into a sentence worth showing. The API's own messages
 * are already written for users, so they pass through.
 */
export function humanMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.isNetwork) return "Can't reach the server. Check your connection."
    if (error.status >= 500) return 'Something went wrong on our side.'
    return error.message
  }
  return "Something went wrong. We couldn't load this."
}
