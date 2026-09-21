import { useLocation, useNavigate, useSearchParams } from 'react-router-dom'
import { useIsDesktop } from '@/hooks/useIsDesktop'
import { mergeParams } from '@/lib/searchParams'

/**
 * The route the panels sit over. Trade is still a route of its own in this
 * phase; in Phase 4 it becomes `/` and this constant follows it, which is why
 * nothing else in the app writes the path literally.
 */
export const MAIN_SURFACE = '/trade'

/** Phase 3 adds history, signals and account. */
export const PANELS = ['positions'] as const
export type PanelName = (typeof PANELS)[number]

/**
 * What the URL says when a desktop user has closed the panel.
 *
 * Desktop needs a word for "closed" because there, an absent parameter means
 * *open*: the panel is persistent by default, the way the benchmark's is, so a
 * fresh session shows it without a query string to say so. A phone has the
 * plainer arrangement -- absent means closed -- and needs no sentinel.
 */
const CLOSED = 'none'

/** Which panel a URL asks for, given who is asking. Pure, so it can be tested. */
export function resolvePanel(
  raw: string | null,
  { desktop, onMainSurface }: { desktop: boolean; onMainSurface: boolean },
): PanelName | null {
  if (!onMainSurface) return null
  if (raw === CLOSED) return null
  if (raw && (PANELS as readonly string[]).includes(raw)) return raw as PanelName

  // An unknown value falls back to the default rather than to nothing, so a
  // stale link from a future phase lands somewhere usable instead of on an
  // empty column.
  return desktop ? 'positions' : null
}

/**
 * Reads and writes the panel on the URL rather than in component state, which
 * is what makes the back button close a panel, makes a panel linkable, and
 * carries the panel through the login round trip the shell already does with
 * `?next=`.
 *
 * Opening and closing push history entries deliberately: going back from an
 * open panel should close it, not leave the app.
 */
export function usePanel() {
  const [params, setParams] = useSearchParams()
  const { pathname } = useLocation()
  const navigate = useNavigate()
  const desktop = useIsDesktop()

  const onMainSurface = pathname === MAIN_SURFACE
  const panel = resolvePanel(params.get('panel'), { desktop, onMainSurface })

  const href = (name: PanelName) => `${MAIN_SURFACE}?panel=${name}`

  // A panel only exists over the main surface, so asking for one from
  // somewhere else means going there with it open.
  const open = (name: PanelName) =>
    onMainSurface ? setParams(mergeParams({ panel: name })) : navigate(href(name))

  const close = () => setParams(mergeParams({ panel: desktop ? CLOSED : null }))

  return {
    panel,
    open,
    close,
    toggle: (name: PanelName) => (panel === name ? close() : open(name)),
    /** For links that must arrive with a panel already open. */
    href,
  }
}
