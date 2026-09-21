import { useSyncExternalStore } from 'react'

/** Tailwind's `lg`, which is where the app switches to its desktop layout. */
const DESKTOP = '(min-width: 1024px)'

function subscribe(onChange: () => void) {
  const query = window.matchMedia(DESKTOP)
  query.addEventListener('change', onChange)
  return () => query.removeEventListener('change', onChange)
}

/**
 * Whether the viewport is desktop-sized, in JavaScript rather than in CSS.
 *
 * Most responsive behaviour belongs in Tailwind and should stay there. This is
 * for the cases where the two breakpoints need genuinely different markup --
 * the Positions panel is an inline column on a desktop and a modal sheet on a
 * phone, which is a difference in behaviour, not in styling.
 */
export function useIsDesktop() {
  return useSyncExternalStore(
    subscribe,
    () => window.matchMedia(DESKTOP).matches,
    () => false,
  )
}
