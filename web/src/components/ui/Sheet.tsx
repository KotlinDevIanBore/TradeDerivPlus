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
  /** Names what is being closed, where "Close" alone would be ambiguous. */
  closeLabel?: string
  /** The scrolling body, for content that needs to watch it scroll. */
  bodyRef?: React.Ref<HTMLDivElement>
  /**
   * Where it comes from. `bottom` is the sheet everything else uses; `left` is
   * the navigation drawer, which stays a drawer at every width because only a
   * phone ever opens it; `panel` is a panel's phone form, which fills the
   * screen and never becomes the centred dialog a bottom sheet becomes,
   * because by that width a panel is a column and not a dialog at all.
   */
  side?: 'bottom' | 'left' | 'panel'
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
  closeLabel = 'Close',
  bodyRef,
  side = 'bottom',
}: SheetProps) {
  const drawer = side === 'left'
  const panel = side === 'panel'
  // Both fill an edge of the screen and scroll inside themselves.
  const column = drawer || panel

  return (
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-40 bg-black/70 animate-fade-in" />
        <Dialog.Content
          className={cn(
            'fixed z-50 border-border bg-elevated text-text focus:outline-none',
            drawer
              ? // Left drawer, at every width: the page stays visible beside it.
                [
                  'inset-y-0 left-0 flex w-[86%] max-w-[320px] flex-col',
                  'rounded-r-3xl border-r animate-slide-in-left',
                ]
              : panel
                ? // A panel's phone form takes the whole screen.
                  'inset-0 flex flex-col animate-slide-up'
                : [
                  // Mobile: bottom sheet.
                  'inset-x-0 bottom-0 rounded-t-3xl border-t animate-slide-up',
                  full ? 'top-0 rounded-t-none' : 'max-h-[85vh]',
                  // Desktop: centred dialog.
                  'sm:inset-x-auto sm:bottom-auto sm:left-1/2 sm:top-1/2 sm:w-[440px]',
                  'sm:-translate-x-1/2 sm:-translate-y-1/2 sm:rounded-card sm:border',
                  'sm:animate-fade-in sm:max-h-[85vh]',
                ],
            className,
          )}
        >
          {/* Drag handle, bottom sheets only. */}
          {!full && !column ? (
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
                aria-label={closeLabel}
              >
                <X className="h-5 w-5" />
              </Dialog.Close>
            ) : null}
          </div>

          <div
            ref={bodyRef}
            className={cn('overflow-y-auto px-5 pb-6', (full || column) && 'flex-1')}
          >
            {children}
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  )
}

export const SheetClose = Dialog.Close
