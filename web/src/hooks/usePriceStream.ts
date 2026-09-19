import { useEffect, useRef } from 'react'
import { createSocket, type ReconnectingSocket } from '@/lib/socket'
import { useMarketStore } from '@/store/market'
import { useSessionStore } from '@/store/session'
import { marketApi } from '@/api/market'
import type { Tick } from '@/types'

/**
 * Subscribes to live prices for a set of symbols. The socket is opened once and
 * the subscription set is changed in place, so switching symbol on the Trade
 * screen does not tear down and rebuild the connection.
 *
 * Also seeds the chart from buffered server history, so a chart has something
 * to draw immediately instead of waiting for ticks to accumulate.
 */
export function usePriceStream(symbols: string[]) {
  const token = useSessionStore((s) => s.accessToken)
  const pushTick = useMarketStore((s) => s.pushTick)
  const seedHistory = useMarketStore((s) => s.seedHistory)
  const setStatus = useMarketStore((s) => s.setStatus)

  const socketRef = useRef<ReconnectingSocket | null>(null)
  const subscribedRef = useRef<string[]>([])
  const seededRef = useRef<Set<string>>(new Set())

  // Sorted and joined so the effect below only re-runs on a real change.
  const key = [...symbols].sort().join(',')

  useEffect(() => {
    if (!token) return

    // Nothing to watch is not a connection problem; say so rather than
    // leaving the indicator stuck on "connecting".
    if (symbols.length === 0) {
      setStatus('idle')
      return
    }

    const initial = key.split(',').filter(Boolean)
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
    }
    // The socket is rebuilt only when the token changes; symbol changes are
    // handled by the subscription effect below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [token])

  // Change the subscription set in place when the symbols change.
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
