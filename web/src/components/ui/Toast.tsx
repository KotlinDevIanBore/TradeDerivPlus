import { useEffect } from 'react'
import { create } from 'zustand'
import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react'
import { cn } from '@/lib/cn'

export type ToastTone = 'success' | 'error' | 'info'

/**
 * Repeated toasts of one kind fold into a single row instead of stacking. A
 * signal every two minutes and a toast per contract opened and settled meant the
 * stack was rarely empty on the trading screen, and at three rows it covered the
 * stake input. Collapsing does not change where toasts sit -- that is the
 * Toaster's job below -- it changes how often there are three of them.
 */
export interface ToastCollapse {
  /** Toasts sharing a key replace one another rather than stacking. */
  key: string
  /** The title once more than one has arrived, e.g. `${n} new signals`. */
  title: (count: number) => string
}

export interface ToastOptions {
  duration?: number
  collapse?: ToastCollapse
}

export interface Toast {
  id: string
  title: string
  body?: string
  tone: ToastTone
  /** Optional action, e.g. "View in Portfolio". */
  action?: { label: string; onClick: () => void }
  duration: number
  collapseKey?: string
  /** How many toasts this row stands for. 1 unless collapsed into. */
  count: number
}

interface ToastState {
  toasts: Toast[]
  push: (
    toast: Omit<Toast, 'id' | 'duration' | 'tone' | 'count' | 'collapseKey'> & {
      tone?: ToastTone
    } & ToastOptions,
  ) => void
  dismiss: (id: string) => void
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (t) =>
    set((s) => {
      const existing = t.collapse ? s.toasts.find((x) => x.collapseKey === t.collapse!.key) : undefined
      const count = (existing?.count ?? 0) + 1
      const rest = existing ? s.toasts.filter((x) => x !== existing) : s.toasts
      return {
        toasts: [
          ...rest.slice(-2), // never stack more than three
          {
            // A fresh id even when collapsing, so the row remounts and its timer
            // restarts from the latest arrival.
            id: crypto.randomUUID(),
            tone: t.tone ?? 'info',
            duration: t.duration ?? 5000,
            title: existing && t.collapse ? t.collapse.title(count) : t.title,
            body: t.body,
            action: t.action,
            collapseKey: t.collapse?.key,
            count,
          },
        ],
      }
    }),
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))

/** Imperative helper so non-component code can raise a toast. */
export const toast = {
  success: (title: string, body?: string, opts?: ToastOptions) =>
    useToastStore.getState().push({ title, body, tone: 'success', ...opts }),
  error: (title: string, body?: string, opts?: ToastOptions) =>
    useToastStore.getState().push({ title, body, tone: 'error', ...opts }),
  info: (title: string, body?: string, opts?: ToastOptions) =>
    useToastStore.getState().push({ title, body, tone: 'info', ...opts }),
  action: (title: string, label: string, onClick: () => void, tone: ToastTone = 'info') =>
    useToastStore.getState().push({ title, tone, action: { label, onClick } }),
}

const icons = {
  success: CheckCircle2,
  error: AlertCircle,
  info: Info,
}

const toneClasses: Record<ToastTone, string> = {
  success: 'text-profit',
  error: 'text-accent',
  info: 'text-muted',
}

function ToastRow({ item }: { item: Toast }) {
  const dismiss = useToastStore((s) => s.dismiss)
  const Icon = icons[item.tone]

  useEffect(() => {
    const id = setTimeout(() => dismiss(item.id), item.duration)
    return () => clearTimeout(id)
  }, [item.id, item.duration, dismiss])

  return (
    <div
      className={cn(
        'pointer-events-auto flex items-start gap-3 rounded-card border border-border',
        'bg-elevated px-4 py-3 shadow-lg shadow-black/40 animate-fade-in',
      )}
    >
      <Icon className={cn('mt-0.5 h-4 w-4 shrink-0', toneClasses[item.tone])} aria-hidden />
      <div className="min-w-0 flex-1">
        <p className="text-body font-medium text-text">{item.title}</p>
        {item.body ? <p className="mt-0.5 text-meta text-muted">{item.body}</p> : null}
        {item.action ? (
          <button
            type="button"
            onClick={() => {
              item.action!.onClick()
              dismiss(item.id)
            }}
            className="mt-2 text-meta font-semibold text-accent hover:underline"
          >
            {item.action.label}
          </button>
        ) : null}
      </div>
      <button
        type="button"
        onClick={() => dismiss(item.id)}
        className="-mr-1 -mt-1 rounded p-1 text-faint transition-colors hover:text-text"
        aria-label="Dismiss"
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  )
}

/** Mounted once in the app shell. */
export function Toaster() {
  const toasts = useToastStore((s) => s.toasts)

  return (
    <div
      role="status"
      aria-live="polite"
      className={cn(
        'pointer-events-none fixed inset-x-0 z-[60] flex flex-col gap-2 px-gutter',
        // Under the header, at every width. This used to be anchored 16px above
        // the tab bar -- which on the trading screen is 16px INSIDE the ticket,
        // so the stack grew upward over the buy buttons (one toast) and the stake
        // input (three). The bottom of a phone screen is where this app's
        // controls live; the top, below the header, is where a toast covers the
        // least that a user is about to touch. On a desktop, the old top-6 sat
        // over the top bar's account, deposit and withdraw controls, so it is
        // offset below the header there too.
        'top-[calc(theme(spacing.topbar)+8px)]',
        'sm:left-auto sm:right-6 sm:top-[calc(theme(spacing.topbar)+16px)] sm:w-[360px] sm:px-0',
      )}
    >
      {toasts.map((t) => (
        <ToastRow key={t.id} item={t} />
      ))}
    </div>
  )
}
