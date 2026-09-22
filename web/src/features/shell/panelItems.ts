import { Bot, Briefcase, Receipt, User, type LucideIcon } from 'lucide-react'
import { PANELS, type PanelName } from './usePanel'

/**
 * What each panel is called and what it looks like in the chrome.
 *
 * One place, because three things name the same panel: the top bar's row, the
 * bottom bar's icons, and the panel's own header. The bottom bar shows the
 * icon alone, so an icon that does not carry the label's meaning by itself is
 * the wrong icon.
 */
export const PANEL_ITEMS: Record<PanelName, { label: string; icon: LucideIcon }> = {
  positions: { label: 'Positions', icon: Briefcase },
  history: { label: 'History', icon: Receipt },
  signals: { label: 'Signals', icon: Bot },
  account: { label: 'Account', icon: User },
}

/**
 * Panels shown in the chrome. Signals stay in PANEL_ITEMS and the panel map
 * but are hidden when digit contracts are on — their CFD entry/SL/TP shape
 * does not apply.
 */
export function chromePanels(digitContractsEnabled: boolean): readonly PanelName[] {
  if (!digitContractsEnabled) return PANELS
  return PANELS.filter((name) => name !== 'signals')
}
