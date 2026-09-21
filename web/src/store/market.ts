import { create } from 'zustand'
import type { ConnectionStatus } from '@/lib/socket'
import type { Tick } from '@/types'

/** How many ticks to keep per symbol for the chart's ring buffer. */
const BUFFER = 600

interface MarketState {
  prices: Record<string, Tick>
  history: Record<string, Tick[]>
  status: ConnectionStatus
  /** Epoch seconds of the last tick on any symbol, for the stale check. */
  lastTickAt: number
  /**
   * Which symbols the mounted screens need priced, counted rather than listed:
   * two screens wanting the same symbol must both let go before it stops
   * streaming. The socket that serves this set belongs to the shell.
   */
  watched: Record<string, number>

  pushTick: (tick: Tick) => void
  seedHistory: (symbol: string, ticks: Tick[]) => void
  setStatus: (status: ConnectionStatus) => void
  watch: (symbols: string[]) => void
  unwatch: (symbols: string[]) => void
  reset: () => void
}

export const useMarketStore = create<MarketState>((set) => ({
  prices: {},
  history: {},
  status: 'idle',
  lastTickAt: 0,
  watched: {},

  pushTick: (tick) =>
    set((s) => {
      const buf = s.history[tick.symbol] ?? []
      const next = buf.length >= BUFFER ? [...buf.slice(buf.length - BUFFER + 1), tick] : [...buf, tick]
      return {
        prices: { ...s.prices, [tick.symbol]: tick },
        history: { ...s.history, [tick.symbol]: next },
        lastTickAt: Date.now(),
      }
    }),

  seedHistory: (symbol, ticks) =>
    set((s) => {
      // Only seed when the live stream has not already filled this symbol,
      // so a slow history fetch cannot clobber newer ticks.
      if ((s.history[symbol]?.length ?? 0) > ticks.length) return s
      const last = ticks.at(-1)
      return {
        history: { ...s.history, [symbol]: ticks.slice(-BUFFER) },
        prices: last ? { ...s.prices, [symbol]: last } : s.prices,
      }
    }),

  setStatus: (status) => set({ status }),

  watch: (symbols) =>
    set((s) => {
      const watched = { ...s.watched }
      for (const symbol of symbols) watched[symbol] = (watched[symbol] ?? 0) + 1
      return { watched }
    }),

  unwatch: (symbols) =>
    set((s) => {
      const watched = { ...s.watched }
      for (const symbol of symbols) {
        const remaining = (watched[symbol] ?? 0) - 1
        if (remaining > 0) watched[symbol] = remaining
        else delete watched[symbol]
      }
      return { watched }
    }),

  reset: () => set({ prices: {}, history: {}, status: 'idle', lastTickAt: 0, watched: {} }),
}))

/**
 * A stable reference for "no ticks yet". Returning a fresh `[]` from the
 * selector would give useSyncExternalStore a new snapshot on every render and
 * spin the component into an infinite update loop.
 */
const NO_TICKS: Tick[] = []

/** Latest price for one symbol, or undefined before the first tick. */
export const usePrice = (symbol: string | undefined) =>
  useMarketStore((s) => (symbol ? s.prices[symbol] : undefined))

export const useTickHistory = (symbol: string | undefined) =>
  useMarketStore((s) => (symbol ? (s.history[symbol] ?? NO_TICKS) : NO_TICKS))

export const useConnectionStatus = () => useMarketStore((s) => s.status)
