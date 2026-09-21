import { useEffect, useMemo, useRef } from 'react'
import { createSocket, type ReconnectingSocket } from '@/lib/socket'
import { useMarketStore } from '@/store/market'
import { useSessionStore } from '@/store/session'
import { marketApi } from '@/api/market'
import type { Tick } from '@/types'

/**
 * Declares which symbols a screen needs priced. Registrations are counted, so
 * a symbol keeps streaming until everything that asked for it has gone.
 *
 * This does not open a connection. The socket belongs to the shell, because a
 * screen unmounting must never be able to take the feed down with it.
 */
export function useWatchSymbols(symbols: string[]) {
  const watch = useMarketStore((s) => s.watch)
  const unwatch = useMarketStore((s) => s.unwatch)

  // Sorted and joined so the effect only re-runs on a real change.
  const key = [...symbols].sort().join(',')

  useEffect(() => {
    const wanted = key.split(',').filter(Boolean)
    if (wanted.length === 0) return

    watch(wanted)
    return () => unwatch(wanted)
  }, [key, watch, unwatch])
}

/**
 * The app's one price socket, mounted by the shell and by nothing else.
 *
 * It serves the merged set every mounted screen has asked for: the instrument
 * on the chart plus the symbols of any open positions. The set changes in
 * place, so switching instrument does not tear the connection down, and it is
 * only closed when the shell itself goes away.
 *
 * Also seeds each newly watched symbol from the server's buffer, so a chart has
 * something to draw immediately instead of waiting for ticks to accumulate.
 */
export function usePriceFeed() {
  const token = useSessionStore((s) => s.accessToken)
  const watched = useMarketStore((s) => s.watched)
  const pushTick = useMarketStore((s) => s.pushTick)
  const seedHistory = useMarketStore((s) => s.seedHistory)
  const setStatus = useMarketStore((s) => s.setStatus)

  const key = useMemo(() => Object.keys(watched).sort().join(','), [watched])
  const streaming = key.length > 0

  const socketRef = useRef<ReconnectingSocket | null>(null)
  const subscribedRef = useRef<string[]>([])
  const seededRef = useRef<Set<string>>(new Set())

  // Read at connect time only: the socket must not be rebuilt when the set
  // changes, which is what the subscription effect below is for.
  const keyRef = useRef(key)
  keyRef.current = key

  useEffect(() => {
    if (!token) return

    // Nothing to watch is not a connection problem; say so rather than
    // leaving the indicator stuck on "connecting".
    if (!streaming) {
      setStatus('idle')
      return
    }

    const initial = keyRef.current.split(',').filter(Boolean)
    subscribedRef.current = initial

    const socket = createSocket({
      path: '/prices',
      params: { symbols: initial.join(',') },
      token,
      onMessage: (env) => {
        if (env.type === 'tick') pushTick(env.payload as Tick)
      },
      onStatus: setStatus,
    })
    socketRef.current = socket

    return () => {
      socket.close()
      socketRef.current = null
      subscribedRef.current = []
      // Closing is deliberate here, so idle is the truth. `close()` itself
      // reports nothing, or a screen letting go of its last symbol would
      // announce the market as offline to every other screen.
      setStatus('idle')
    }
  }, [token, streaming, pushTick, setStatus])

  // Change the subscription set in place when the watched symbols change.
  useEffect(() => {
    const socket = socketRef.current
    if (!socket) return

    const next = key.split(',').filter(Boolean)
    const previous = subscribedRef.current

    const added = next.filter((s) => !previous.includes(s))
    const removed = previous.filter((s) => !next.includes(s))

    if (added.length) socket.send({ action: 'subscribe', symbols: added })
    if (removed.length) socket.send({ action: 'unsubscribe', symbols: removed })

    subscribedRef.current = next
  }, [key])

  // Seed each newly watched symbol from the server's tick buffer, once.
  useEffect(() => {
    if (!token) return
    const wanted = key.split(',').filter(Boolean)

    for (const symbol of wanted) {
      if (seededRef.current.has(symbol)) continue
      seededRef.current.add(symbol)

      marketApi
        .ticks(symbol, 5)
        .then((res) => {
          seedHistory(symbol, res.items.map((t) => ({ symbol, price: t.price, epoch: t.epoch })))
        })
        .catch(() => {
          // History is an optimisation; the live stream still fills the chart.
          seededRef.current.delete(symbol)
        })
    }
  }, [key, token, seedHistory])
}
