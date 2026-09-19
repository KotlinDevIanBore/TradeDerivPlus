import { useEffect, useRef, useState } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { createSocket } from '@/lib/socket'
import { useSessionStore } from '@/store/session'
import { queryKeys } from '@/lib/queryKeys'
import type { List, Signal } from '@/types'

/**
 * Listens for new signals app-wide. New signals are prepended into every cached
 * signal list, and returned here so the Signals screen can show its "new
 * signal" pill and the shell can raise a toast.
 */
export function useSignalStream(onSignal?: (signal: Signal) => void) {
  const token = useSessionStore((s) => s.accessToken)
  const queryClient = useQueryClient()
  const [latest, setLatest] = useState<Signal | null>(null)

  // Kept in a ref so a changing callback identity does not rebuild the socket.
  const handlerRef = useRef(onSignal)
  handlerRef.current = onSignal

  useEffect(() => {
    if (!token) return

    const socket = createSocket({
      path: '/signals',
      token,
      onMessage: (env) => {
        if (env.type !== 'signal') return
        const signal = env.payload as Signal

        setLatest(signal)
        handlerRef.current?.(signal)

        // Prepend into the "all symbols" list and this symbol's own list.
        // A refetch may already have picked this signal up, so drop any
        // existing copy rather than rendering the same id twice.
        for (const key of [queryKeys.signals(), queryKeys.signals(signal.symbol_code)]) {
          queryClient.setQueryData<List<Signal>>(key, (old) => {
            const rest = (old?.items ?? []).filter((s) => s.id !== signal.id)
            return { items: [signal, ...rest].slice(0, 100) }
          })
        }
      },
      onStatus: () => {
        // Signal pushes are a convenience; the list still refetches normally,
        // so this socket's status does not drive the connection indicator.
      },
    })

    return () => socket.close()
  }, [token, queryClient])

  return latest
}
