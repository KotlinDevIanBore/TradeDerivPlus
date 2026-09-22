import { Navigate, useLocation, useSearchParams } from 'react-router-dom'
import { MAIN_SURFACE, type PanelName } from './usePanel'

/**
 * The surface's old route, `/trade`.
 *
 * Everything that route was ever given means the same thing at `/`: a symbol,
 * a direction, the levels a signal carries, and which panel is open. So the
 * query string crosses whole rather than by name. This is permanent -- signal
 * links, the old deposit flow and anything a user bookmarked all point here.
 */
export function SurfaceRedirect() {
  const { search } = useLocation()

  return <Navigate to={`${MAIN_SURFACE}${search}`} replace />
}

/**
 * An old screen's route, pointed at the panel that replaced it.
 *
 * The catch-all is not good enough for these: it would land people on the bare
 * surface with their parameters silently dropped, and the parameters are the
 * point -- `/portfolio?tab=closed` is a link somebody sent somebody else.
 *
 * `carry` names the parameters that mean the same thing on the other side.
 * Anything else the old URL held is left behind deliberately rather than
 * forwarded into a query string it has no meaning in.
 */
export function PanelRedirect({ panel, carry = [] }: { panel: PanelName; carry?: string[] }) {
  const [params] = useSearchParams()

  const next = new URLSearchParams({ panel })
  for (const key of carry) {
    const value = params.get(key)
    if (value) next.set(key, value)
  }

  return <Navigate to={`${MAIN_SURFACE}?${next}`} replace />
}
