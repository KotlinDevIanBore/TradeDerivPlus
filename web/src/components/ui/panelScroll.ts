import { createContext, useContext } from 'react'

export const PanelScrollContext = createContext<HTMLElement | null>(null)

/**
 * The element a panel scrolls inside, for content that needs to know where
 * scrolling happens rather than merely that it does.
 *
 * A panel scrolls itself, so `window.scrollY` is zero however far down its
 * content the user is. Anything watching the window from inside a panel is
 * watching the wrong thing. Returns null until the panel has mounted, and for
 * content rendered outside a panel.
 */
export function usePanelScroll() {
  return useContext(PanelScrollContext)
}
