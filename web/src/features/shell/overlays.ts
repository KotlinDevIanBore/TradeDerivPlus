import { create } from 'zustand'

/**
 * The shell's overlays: money flows and the things that reach them from
 * anywhere. Each is mounted once, by AppShell, so a screen no longer has to
 * own a copy of the deposit sheet to be able to offer a deposit.
 *
 * One at a time is deliberate. Opening Deposit from the navigation drawer
 * should put the drawer away, not stack two sheets.
 */
export type Overlay = 'deposit' | 'withdraw' | 'accounts' | 'menu'

interface OverlayState {
  open: Overlay | null
  show: (overlay: Overlay) => void
  hide: () => void
}

const useOverlayStore = create<OverlayState>((set) => ({
  open: null,
  show: (open) => set({ open }),
  hide: () => set({ open: null }),
}))

/**
 * Opens a shell overlay from anywhere, the same way `toast` raises a toast
 * from anywhere: the caller says what it wants, the shell owns the mount.
 */
export const overlay = {
  deposit: () => useOverlayStore.getState().show('deposit'),
  withdraw: () => useOverlayStore.getState().show('withdraw'),
  accounts: () => useOverlayStore.getState().show('accounts'),
  menu: () => useOverlayStore.getState().show('menu'),
  close: () => useOverlayStore.getState().hide(),
}

/** Whether the named overlay is the one currently on screen. */
export function useOverlayOpen(name: Overlay) {
  return useOverlayStore((s) => s.open === name)
}
