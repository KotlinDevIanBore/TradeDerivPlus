const WS_BASE = import.meta.env.VITE_WS_URL ?? 'ws://localhost:8080/api/v1/ws'

/**
 * `idle` means nothing is subscribed, which is not a fault: the Home screen
 * with no open positions has no reason to hold a price socket open.
 */
export type ConnectionStatus = 'idle' | 'connecting' | 'live' | 'reconnecting' | 'offline'

export interface Envelope<T = unknown> {
  type: string
  payload: T
}

interface SocketOptions {
  /** Path under the ws base, e.g. `/prices`. */
  path: string
  /** Query parameters other than the token. */
  params?: Record<string, string | undefined>
  token: string
  onMessage: (envelope: Envelope) => void
  onStatus: (status: ConnectionStatus) => void
}

export interface ReconnectingSocket {
  /** Send a subscription change without reconnecting. */
  send: (data: unknown) => void
  close: () => void
}

const MAX_BACKOFF = 30_000
const GIVE_UP_AFTER = 3

/**
 * A WebSocket that reconnects with exponential backoff and reports its status
 * so the UI can show the connection dot and freeze prices when the feed is
 * stale. After three consecutive failures it reports `offline`, which is the
 * cue for the banner offering a manual reconnect.
 */
export function createSocket(opts: SocketOptions): ReconnectingSocket {
  let ws: WebSocket | null = null
  let backoff = 1000
  let failures = 0
  let closed = false
  let retryTimer: ReturnType<typeof setTimeout> | null = null

  const url = () => {
    const sp = new URLSearchParams()
    for (const [k, v] of Object.entries(opts.params ?? {})) {
      if (v) sp.set(k, v)
    }
    sp.set('token', opts.token)
    return `${WS_BASE}${opts.path}?${sp.toString()}`
  }

  const connect = () => {
    if (closed) return
    opts.onStatus(failures === 0 ? 'connecting' : 'reconnecting')

    let socket: WebSocket
    try {
      socket = new WebSocket(url())
    } catch {
      scheduleRetry()
      return
    }
    ws = socket

    socket.onopen = () => {
      failures = 0
      backoff = 1000
      opts.onStatus('live')
    }

    socket.onmessage = (event) => {
      try {
        opts.onMessage(JSON.parse(event.data as string) as Envelope)
      } catch {
        // A frame we cannot parse is not worth tearing the connection down for.
      }
    }

    socket.onerror = () => {
      // onclose always follows; handle the retry there so it happens once.
    }

    socket.onclose = () => {
      if (closed) return
      scheduleRetry()
    }
  }

  const scheduleRetry = () => {
    failures += 1
    opts.onStatus(failures >= GIVE_UP_AFTER ? 'offline' : 'reconnecting')

    retryTimer = setTimeout(connect, backoff)
    backoff = Math.min(backoff * 2, MAX_BACKOFF)
  }

  connect()

  return {
    send: (data: unknown) => {
      if (ws?.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify(data))
      }
    },
    close: () => {
      closed = true
      if (retryTimer) clearTimeout(retryTimer)
      ws?.close()
      ws = null
      opts.onStatus('offline')
    },
  }
}

export { WS_BASE }
