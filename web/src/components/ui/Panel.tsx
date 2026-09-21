import { X } from 'lucide-react'
import { useIsDesktop } from '@/hooks/useIsDesktop'
import { Sheet } from './Sheet'

/**
 * A panel of content beside the main surface.
 *
 * The two breakpoints are genuinely different things, not one thing styled two
 * ways. On a desktop this is an ordinary column in the page: no backdrop, no
 * focus trap, no scroll lock, and nothing to dismiss -- it is meant to be read
 * *while* trading, so it must not take the keyboard or the pointer away from
 * the chart beside it. On a phone there is no room to sit beside anything, so
 * it is a full-screen sheet with everything a modal owes the user.
 *
 * That is why the desktop case is plain markup rather than a Radix dialog with
 * `modal={false}`: a non-modal dialog still moves focus and still stacks as an
 * overlay, and neither is wanted for a column that is simply part of the page.
 */
export function Panel({
  open,
  onClose,
  title,
  children,
}: {
  open: boolean
  onClose: () => void
  title: string
  children: React.ReactNode
}) {
  const desktop = useIsDesktop()

  if (desktop) {
    if (!open) return null

    return (
      <aside
        aria-label={title}
        className="sticky top-topbar flex h-[calc(100vh-theme(spacing.topbar))] w-[300px] shrink-0 flex-col border-r border-hairline bg-bg"
      >
        <div className="flex items-center justify-between gap-3 px-4 pb-2 pt-4">
          <h2 className="text-card font-semibold">{title}</h2>
          <button
            type="button"
            onClick={onClose}
            aria-label={`Close ${title}`}
            className="-mr-1 rounded-lg p-1.5 text-muted transition-colors hover:bg-surface hover:text-text"
          >
            <X className="h-4 w-4" aria-hidden />
          </button>
        </div>

        <div className="flex-1 overflow-y-auto px-4 pb-6">{children}</div>
      </aside>
    )
  }

  return (
    <Sheet
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose()
      }}
      side="panel"
      title={title}
      closeLabel={`Close ${title}`}
    >
      {children}
    </Sheet>
  )
}
