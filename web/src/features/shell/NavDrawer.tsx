import { ArrowDownToLine, ArrowUpFromLine, LogOut, type LucideIcon } from 'lucide-react'
import { Sheet } from '@/components/ui/Sheet'
import { useLogout } from '@/hooks/useAuth'
import { cn } from '@/lib/cn'
import { overlay } from './overlays'

const row =
  'flex w-full items-center gap-3 rounded-xl px-3 py-3 text-body font-medium transition-colors'

function Action({
  label,
  icon: Icon,
  onClick,
}: {
  label: string
  icon: LucideIcon
  onClick: () => void
}) {
  return (
    <li>
      <button type="button" onClick={onClick} className={cn(row, 'text-text hover:bg-surface')}>
        <Icon className="h-5 w-5 shrink-0 text-muted" aria-hidden />
        {label}
      </button>
    </li>
  )
}

/**
 * The three things that are not panels: two money flows and the way out.
 *
 * It used to list the panels as well, which made it a second route to the same
 * four places the bottom bar now carries. Everything left here raises a sheet
 * or ends the session -- none of it navigates, because there is nowhere to
 * navigate to.
 */
export function NavDrawer({
  open,
  onOpenChange,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
}) {
  const logout = useLogout()
  const close = () => onOpenChange(false)

  return (
    <Sheet open={open} onOpenChange={onOpenChange} side="left" title="Menu">
      {/* The sheet's body is the scroll area; this makes it a column so the
          way out can sit at the foot of the drawer rather than under the
          last item. */}
      <div className="flex min-h-full flex-col">
        <nav aria-label="Menu">
          <ul className="-mx-1 space-y-0.5">
            <Action label="Deposit" icon={ArrowDownToLine} onClick={overlay.deposit} />
            <Action label="Withdraw" icon={ArrowUpFromLine} onClick={overlay.withdraw} />
          </ul>
        </nav>

        <div className="mt-auto border-t border-hairline pt-3">
          <button
            type="button"
            onClick={() => {
              close()
              logout()
            }}
            className={cn(row, '-mx-1 text-accent hover:bg-accent-subtle')}
          >
            <LogOut className="h-5 w-5 shrink-0" aria-hidden />
            Log out
          </button>
        </div>
      </div>
    </Sheet>
  )
}
