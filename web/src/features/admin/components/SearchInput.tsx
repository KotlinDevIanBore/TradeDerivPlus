import { useEffect, useState } from 'react'
import { Search, X } from 'lucide-react'

/**
 * Debounced search. Typing does not fire a request per keystroke, and Escape
 * clears without reaching for the mouse.
 */
export function SearchInput({
  value,
  onChange,
  placeholder,
  debounceMs = 300,
}: {
  value: string
  onChange: (value: string) => void
  placeholder: string
  debounceMs?: number
}) {
  const [local, setLocal] = useState(value)

  useEffect(() => setLocal(value), [value])

  useEffect(() => {
    if (local === value) return
    const id = setTimeout(() => onChange(local), debounceMs)
    return () => clearTimeout(id)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [local, debounceMs])

  return (
    <div className="flex w-full max-w-[420px] items-center gap-2 rounded-xl border border-border bg-elevated px-3 focus-within:border-accent">
      <Search className="h-4 w-4 shrink-0 text-faint" aria-hidden />
      <input
        type="search"
        value={local}
        placeholder={placeholder}
        onChange={(e) => setLocal(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            setLocal('')
            onChange('')
          }
        }}
        className="h-10 w-full bg-transparent text-body text-text outline-none placeholder:text-faint"
      />
      {local ? (
        <button
          type="button"
          onClick={() => {
            setLocal('')
            onChange('')
          }}
          className="shrink-0 p-1 text-faint hover:text-text"
          aria-label="Clear search"
        >
          <X className="h-3.5 w-3.5" />
        </button>
      ) : null}
    </div>
  )
}

export function FilterChips<T extends string>({
  options,
  value,
  onChange,
}: {
  options: { label: string; value: T }[]
  value: T
  onChange: (value: T) => void
}) {
  return (
    <div className="no-scrollbar flex gap-2 overflow-x-auto">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={
            'shrink-0 rounded-pill border px-3 py-1.5 text-meta font-medium transition-colors ' +
            (o.value === value
              ? 'border-accent bg-accent-subtle text-text'
              : 'border-border text-muted hover:text-text')
          }
        >
          {o.label}
        </button>
      ))}
    </div>
  )
}
