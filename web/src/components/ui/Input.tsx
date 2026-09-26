import { forwardRef, useId } from 'react'
import { cn } from '@/lib/cn'

export interface InputProps extends React.InputHTMLAttributes<HTMLInputElement> {
  label?: string
  /** Validation message shown under the field, styled as an error. */
  error?: string
  /** Neutral helper text; hidden while an error is showing. */
  hint?: string
  /** Fixed text inside the field's left edge, e.g. a currency or dial code. */
  prefix?: string
  /** Control rendered against the field's right edge, e.g. a Max chip. */
  trailing?: React.ReactNode
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input(
  { className, label, error, hint, prefix, trailing, id, ...props },
  ref,
) {
  const generated = useId()
  const inputId = id ?? generated
  const describedBy = error ? `${inputId}-error` : hint ? `${inputId}-hint` : undefined

  return (
    <div className="w-full">
      {label ? (
        <label htmlFor={inputId} className="mb-1.5 block text-meta font-medium text-muted">
          {label}
        </label>
      ) : null}

      <div
        className={cn(
          'flex items-center gap-2 rounded-xl border bg-elevated px-3 transition-colors',
          'focus-within:border-accent',
          error ? 'border-accent' : 'border-border',
        )}
      >
        {prefix ? <span className="shrink-0 text-body text-muted tnum">{prefix}</span> : null}
        <input
          ref={ref}
          id={inputId}
          aria-invalid={error ? true : undefined}
          aria-describedby={describedBy}
          className={cn(
            // 16px, deliberately, and not the 14px body size the rest of the form
            // uses. This input rendered at 16px for months by accident -- a
            // tailwind-merge bug dropped text-body -- and that accident was doing a
            // job: iOS Safari zooms the whole page when a field under 16px takes
            // focus. Fixing the merge (lib/cn.ts) would have shrunk every input in
            // the app to 14px and switched that zoom on. text-card is the 16px
            // token, so the size is now stated rather than fallen back into.
            'h-11 w-full bg-transparent text-card text-text outline-none',
            'placeholder:text-faint',
            className,
          )}
          {...props}
        />
        {trailing ? <div className="shrink-0">{trailing}</div> : null}
      </div>

      {error ? (
        <p id={`${inputId}-error`} className="mt-1.5 text-meta text-accent">
          {error}
        </p>
      ) : hint ? (
        <p id={`${inputId}-hint`} className="mt-1.5 text-meta text-faint">
          {hint}
        </p>
      ) : null}
    </div>
  )
})
