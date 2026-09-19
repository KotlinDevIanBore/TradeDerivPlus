import { ChevronLeft, ChevronRight } from 'lucide-react'
import { ErrorState } from '@/components/ErrorState'
import { Skeleton } from '@/components/ui/Skeleton'
import { cn } from '@/lib/cn'

export interface Column<T> {
  key: string
  header: string
  /** Money and counts right-align; everything else reads from the left. */
  align?: 'left' | 'right'
  width?: string
  render: (row: T) => React.ReactNode
}

interface DataTableProps<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T) => string
  isLoading?: boolean
  error?: unknown
  onRetry?: () => void
  onRowClick?: (row: T) => void
  empty?: React.ReactNode
  page?: number
  pages?: number
  total?: number
  limit?: number
  onPageChange?: (page: number) => void
}

/**
 * The one table every admin list uses. Sticky header, an empty and an error
 * slot so a list never renders a bare white box, and a pagination footer that
 * says where you are rather than just offering arrows.
 *
 * It scrolls horizontally inside its own container, so a wide table never
 * makes the page itself scroll sideways.
 */
export function DataTable<T>({
  columns,
  rows,
  rowKey,
  isLoading,
  error,
  onRetry,
  onRowClick,
  empty,
  page = 1,
  pages = 0,
  total = 0,
  limit = 25,
  onPageChange,
}: DataTableProps<T>) {
  if (error) {
    return (
      <div className="rounded-card border border-border bg-surface">
        <ErrorState error={error} onRetry={onRetry} />
      </div>
    )
  }

  return (
    <div className="overflow-hidden rounded-card border border-border bg-surface">
      <div className="overflow-x-auto">
        <table className="w-full min-w-[640px] border-collapse">
          <thead className="sticky top-0 z-10 bg-elevated">
            <tr>
              {columns.map((c) => (
                <th
                  key={c.key}
                  style={c.width ? { width: c.width } : undefined}
                  className={cn(
                    'whitespace-nowrap px-4 py-2.5 text-meta font-semibold uppercase tracking-wider text-faint',
                    c.align === 'right' ? 'text-right' : 'text-left',
                  )}
                >
                  {c.header}
                </th>
              ))}
            </tr>
          </thead>

          <tbody>
            {isLoading ? (
              Array.from({ length: 8 }).map((_, i) => (
                <tr key={i} className="border-t border-hairline">
                  {columns.map((c) => (
                    <td key={c.key} className="px-4 py-3">
                      <Skeleton className="h-4 w-full max-w-[140px]" />
                    </td>
                  ))}
                </tr>
              ))
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={columns.length} className="px-4 py-10">
                  {empty ?? <p className="text-center text-body text-muted">Nothing here yet</p>}
                </td>
              </tr>
            ) : (
              rows.map((row) => (
                <tr
                  key={rowKey(row)}
                  onClick={onRowClick ? () => onRowClick(row) : undefined}
                  tabIndex={onRowClick ? 0 : undefined}
                  onKeyDown={
                    onRowClick
                      ? (e) => {
                          if (e.key === 'Enter' || e.key === ' ') {
                            e.preventDefault()
                            onRowClick(row)
                          }
                        }
                      : undefined
                  }
                  className={cn(
                    'border-t border-hairline transition-colors',
                    onRowClick &&
                      'cursor-pointer hover:bg-elevated focus:bg-elevated focus:outline-none',
                  )}
                >
                  {columns.map((c) => (
                    <td
                      key={c.key}
                      className={cn(
                        'px-4 py-3 text-body',
                        c.align === 'right' ? 'text-right tnum' : 'text-left',
                      )}
                    >
                      {c.render(row)}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>

      {onPageChange && total > 0 ? (
        <div className="flex items-center justify-between gap-3 border-t border-hairline px-4 py-2.5">
          <p className="text-meta text-muted tnum">
            Showing {(page - 1) * limit + 1}–{Math.min(page * limit, total)} of {total}
          </p>
          <div className="flex items-center gap-1">
            <PageButton
              label="Previous page"
              disabled={page <= 1}
              onClick={() => onPageChange(page - 1)}
            >
              <ChevronLeft className="h-4 w-4" />
            </PageButton>
            <span className="px-2 text-meta text-muted tnum">
              {page} / {Math.max(pages, 1)}
            </span>
            <PageButton
              label="Next page"
              disabled={page >= pages}
              onClick={() => onPageChange(page + 1)}
            >
              <ChevronRight className="h-4 w-4" />
            </PageButton>
          </div>
        </div>
      ) : null}
    </div>
  )
}

function PageButton({
  label,
  disabled,
  onClick,
  children,
}: {
  label: string
  disabled: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      aria-label={label}
      disabled={disabled}
      onClick={onClick}
      className="rounded-lg p-1.5 text-muted transition-colors hover:bg-elevated hover:text-text disabled:pointer-events-none disabled:opacity-30"
    >
      {children}
    </button>
  )
}
