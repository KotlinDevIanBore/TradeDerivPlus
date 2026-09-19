import * as Dialog from '@radix-ui/react-dialog'
import { X } from 'lucide-react'
import { cn } from '@/lib/cn'

interface SheetProps {
  open: boolean
  onOpenChange: (open: boolean) => void
  title?: string
  /** Hide the title visually but keep it for screen readers. */
  hideTitle?: boolean
  description?: string
  children: React.ReactNode
  className?: string
  /** Full height instead of the default content-sized sheet. */
  full?: boolean
  showClose?: boolean
}

/**
 * A bottom sheet on mobile that becomes a centred dialog from `sm` up, which is
 * the one pattern the plan asks for on deposit, withdraw, the account switcher
 * and the symbol picker.
 */
export function Sheet({
  open,
  onOpenChange,
  title,
  hideTitle,
  description,
  children,
  className,
  full,
  showClose = true,
}: SheetProps) {
  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/70 animate-fade-in" />
        <Dialog.Content
          className={cn(
            'fixed z-50 border-border bg-elevated text-text focus:outline-none',
            // Mobile: bottom sheet.
            'inset-x-0 bottom-0 rounded-t-3xl border-t animate-slide-up',
            full ? 'top-0 rounded-t-none' : 'max-h-[85vh]',
            // Desktop: centred dialog.
            'sm:inset-x-auto sm:bottom-auto sm:left-1/2 sm:top-1/2 sm:w-[440px]',
            'sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-card sm:border',
            'sm:animate-fade-in sm:max-h-[85vh]',
            className,
          )}
        >
          {/* Drag handle, mobile only. */}
          {!full ? (
            <div className="flex justify-center pt-3 sm:hidden" aria-hidden>
              <div className="h-1 w-10 rounded-full bg-border" />
            </div>
          ) : null}

          <div className="flex items-start justify-between gap-3 px-5 pb-2 pt-4">
            <div className={cn(hideTitle && 'sr-only')}>
              {title ? (
                <Dialog.Title className="text-card font-semibold">{title}</Dialog.Title>
              ) : null}
              {description ? (
                <Dialog.Description className="mt-1 text-meta text-muted">
                  {description}
                </Dialog.Description>
              ) : null}
            </div>
            {showClose ? (
              <Dialog.Close
                className="-mr-1 -mt-1 rounded-lg p-2 text-muted transition-colors hover:bg-surface hover:text-text"
                aria-label="Close"
              >
                <X className="h-5 w-5" />
              </Dialog.Close>
            ) : null}
          </div>

          <div className={cn('overflow-y-auto px-5 pb-6', full && 'flex-1')}>{children}</div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export const SheetClose = Dialog.Close
