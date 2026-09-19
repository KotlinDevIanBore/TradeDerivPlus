import { useEffect } from 'react'
import { create } from 'zustand'
import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react'
import { cn } from '@/lib/cn'

export type ToastTone = 'success' | 'error' | 'info'

export interface Toast {
  id: string
  title: string
  body?: string
  tone: ToastTone
  /** Optional action, e.g. "View in Portfolio". */
  action?: { label: string; onClick: () => void }
  duration: number
}

interface ToastState {
  toasts: Toast[]
  push: (toast: Omit<Toast, 'id' | 'duration' | 'tone'> & { tone?: ToastTone; duration?: number }) => void
  dismiss: (id: string) => void
}

export const useToastStore = create<ToastState>((set) => ({
  toasts: [],
  push: (t) =>
    set((s) => ({
      toasts: [
        ...s.toasts.slice(-2), // never stack more than three
        {
          id: crypto.randomUUID(),
          tone: t.tone ?? 'info',
          duration: t.duration ?? 5000,
          title: t.title,
          body: t.body,
          action: t.action,
        },
      ],
    })),
  dismiss: (id) => set((s) => ({ toasts: s.toasts.filter((t) => t.id !== id) })),
}))

/** Imperative helper so non-component code can raise a toast. */
export const toast = {
  success: (title: string, body?: string) => useToastStore.getState().push({ title, body, tone: 'success' }),
  error: (title: string, body?: string) => useToastStore.getState().push({ title, body, tone: 'error' }),
  info: (title: string, body?: string) => useToastStore.getState().push({ title, body, tone: 'info' }),
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
        // Clear of the bottom tab bar on mobile; top-right on desktop.
        'bottom-[calc(theme(spacing.tabbar)+16px)]',
        'sm:bottom-auto sm:left-auto sm:right-6 sm:top-6 sm:w-[360px] sm:px-0',
      )}
    >
      {toasts.map((t) => (
        <ToastRow key={t.id} item={t} />
      ))}
    </div>
  )
}
