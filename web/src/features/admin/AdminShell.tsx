import { useEffect } from 'react'
import { Link, NavLink, Navigate, Outlet, useLocation, useNavigate } from 'react-router-dom'
import {
  AlertTriangle,
  Bot,
  ClipboardList,
  LayoutDashboard,
  LogOut,
  Receipt,
  ShieldCheck,
  Users as UsersIcon,
  type LucideIcon,
} from 'lucide-react'
import { useQuery, useQueryClient } from '@tanstack/react-query'
import { cn } from '@/lib/cn'
import { adminApi, adminKeys } from './adminApi'
import { useAdminSession } from './adminSession'

interface NavItem {
  to: string
  label: string
  icon: LucideIcon
  badge?: number
}

/**
 * The admin frame. Desktop-first and data-dense: an admin sits at a desk, so
 * this is a fixed sidebar and tables rather than the user app's mobile-first
 * cards. It collapses to a top bar below md so a support person on a tablet is
 * not locked out.
 */
export function AdminShell() {
  const location = useLocation()
  const navigate = useNavigate()
  const queryClient = useQueryClient()
  const { admin, accessToken, passwordChangeRequired, clear } = useAdminSession()

  // The banner tracks whether the default password is still in place; the
  // redirect tracks whether the backend is blocking the session. In
  // development the first is true and the second is false, which is exactly
  // the case the two flags exist to tell apart.
  const onDefaultPassword = admin?.must_change_password ?? false

  // The pending count on the sidebar is the queue's own nag.
  const kyc = useQuery({
    queryKey: adminKeys.kyc('pending', 1),
    queryFn: () => adminApi.kyc('pending', 1),
    enabled: Boolean(accessToken) && !passwordChangeRequired,
    refetchInterval: 60_000,
  })

  // A session still on the default password can do exactly one thing.
  useEffect(() => {
    if (passwordChangeRequired && location.pathname !== '/admin/change-password') {
      navigate('/admin/change-password', { replace: true })
    }
  }, [passwordChangeRequired, location.pathname, navigate])

  if (!accessToken) {
    const next = encodeURIComponent(location.pathname + location.search)
    return <Navigate to={`/admin/login?next=${next}`} replace />
  }

  const items: NavItem[] = [
    { to: '/admin', label: 'Dashboard', icon: LayoutDashboard },
    { to: '/admin/users', label: 'Users', icon: UsersIcon },
    { to: '/admin/transactions', label: 'Transactions', icon: Receipt },
    { to: '/admin/kyc', label: 'KYC queue', icon: ShieldCheck, badge: kyc.data?.total },
    { to: '/admin/bot-runs', label: 'Auto runs', icon: Bot },
    { to: '/admin/audit-log', label: 'Audit log', icon: ClipboardList },
  ]

  const signOut = async () => {
    try {
      await adminApi.logout()
    } catch {
      // Never trap an admin in a signed-in state.
    }
    clear()
    queryClient.clear()
    navigate('/admin/login', { replace: true })
  }

  return (
    <div className="flex min-h-full">
      <aside className="hidden w-[240px] shrink-0 flex-col border-r border-hairline md:flex">
        <div className="px-4 py-5">
          <Link to="/admin" className="flex items-center gap-2">
            <span className="flex h-7 w-7 items-center justify-center rounded-lg bg-accent text-[11px] font-bold text-white">
              TD
            </span>
            <span className="text-body font-bold tracking-tight">
              TradeDerivPlus <span className="text-muted">Admin</span>
            </span>
          </Link>
        </div>

        <nav aria-label="Admin" className="flex-1 px-3">
          <ul className="space-y-1">
            {items.map(({ to, label, icon: Icon, badge }) => (
              <li key={to}>
                <NavLink
                  to={to}
                  end={to === '/admin'}
                  className={({ isActive }) =>
                    cn(
                      'flex items-center gap-3 rounded-xl px-3 py-2.5 text-body font-medium transition-colors',
                      isActive
                        ? 'bg-accent-subtle text-accent'
                        : 'text-muted hover:bg-surface hover:text-text',
                    )
                  }
                >
                  <Icon className="h-4 w-4 shrink-0" aria-hidden />
                  <span className="flex-1">{label}</span>
                  {badge ? (
                    <span className="rounded-pill bg-warning/20 px-1.5 py-0.5 text-[10px] font-bold text-warning tnum">
                      {badge}
                    </span>
                  ) : null}
                </NavLink>
              </li>
            ))}
          </ul>
        </nav>

        {/* Who is doing this is always on screen. */}
        <div className="border-t border-hairline px-4 py-4">
          <p className="text-body font-medium">{admin?.username}</p>
          <p className="mt-0.5 text-meta capitalize text-muted">{admin?.role}</p>
          <div className="mt-3 space-y-1.5">
            <Link
              to="/admin/change-password"
              className="block text-meta text-muted transition-colors hover:text-text"
            >
              Change password
            </Link>
            <button
              type="button"
              onClick={signOut}
              className="flex items-center gap-1.5 text-meta text-muted transition-colors hover:text-accent"
            >
              <LogOut className="h-3 w-3" aria-hidden />
              Sign out
            </button>
          </div>
        </div>
      </aside>

      <div className="min-w-0 flex-1">
        {/* Mobile nav: the same destinations, scrolled horizontally. */}
        <div className="no-scrollbar flex gap-2 overflow-x-auto border-b border-hairline px-4 py-2 md:hidden">
          {items.map(({ to, label }) => (
            <NavLink
              key={to}
              to={to}
              end={to === '/admin'}
              className={({ isActive }) =>
                cn(
                  'shrink-0 rounded-pill px-3 py-1.5 text-meta font-medium',
                  isActive ? 'bg-accent-subtle text-accent' : 'text-muted',
                )
              }
            >
              {label}
            </NavLink>
          ))}
        </div>

        {onDefaultPassword ? (
          <div className="flex items-center gap-2 border-b border-warning/30 bg-warning/[0.06] px-4 py-2.5 lg:px-6">
            <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden />
            <p className="flex-1 text-meta text-text">
              You are using the default password. Change it now.
            </p>
            <Link
              to="/admin/change-password"
              className="text-meta font-semibold text-warning hover:underline"
            >
              Change password
            </Link>
          </div>
        ) : null}

        <main className="px-4 py-5 lg:px-6">
          <Outlet />
        </main>
      </div>
    </div>
  )
}

export function AdminPageHeader({
  title,
  breadcrumb,
  action,
}: {
  title: string
  breadcrumb?: React.ReactNode
  action?: React.ReactNode
}) {
  return (
    <div className="mb-5">
      {breadcrumb ? <div className="mb-1 text-meta text-muted">{breadcrumb}</div> : null}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <h1 className="text-title font-bold tracking-tight">{title}</h1>
        {action}
      </div>
    </div>
  )
}
