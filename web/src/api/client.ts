import { useSessionStore } from '@/store/session'

const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'

/**
 * The error shape the API returns for every non-2xx response. `message` is safe
 * to show a user; `field` puts it under the right input; `code` is for
 * branching without matching on English.
 */
export interface ApiErrorBody {
  message: string
  code: string
  field?: string
}

export class ApiError extends Error {
  readonly code: string
  readonly field?: string
  readonly status: number

  constructor(status: number, body: ApiErrorBody) {
    super(body.message)
    this.name = 'ApiError'
    this.status = status
    this.code = body.code
    this.field = body.field
  }

  /** True when the server could not be reached at all, rather than refusing. */
  get isNetwork() {
    return this.code === 'network'
  }
}

function networkError() {
  return new ApiError(0, {
    message: "Can't reach the server. Check your connection.",
    code: 'network',
  })
}

/**
 * A single in-flight refresh, shared by every request that gets a 401, so a
 * burst of parallel queries does not fire a burst of refreshes.
 */
let refreshInFlight: Promise<string | null> | null = null

/**
 * Exchanges the refresh cookie for a new access token. There is no token to
 * pass: the browser holds it in an httpOnly cookie and sends it because of
 * `credentials: 'include'`. That is the whole point -- this code could not
 * read the refresh token even if it wanted to.
 */
async function refreshAccessToken(): Promise<string | null> {
  try {
    const res = await fetch(`${BASE}/auth/refresh`, {
      method: 'POST',
      credentials: 'include',
    })
    if (!res.ok) {
      useSessionStore.getState().clear()
      return null
    }
    const tokens = await res.json()
    useSessionStore.getState().setTokens(tokens)
    return tokens.access_token as string
  } catch {
    return null
  }
}

/**
 * Called once at boot. The access token is memory-only, so a page reload
 * starts without one; this trades the refresh cookie for a fresh token before
 * the router decides whether to redirect to login. Without it every reload
 * would look like a signed-out session.
 */
export async function bootstrapSession(): Promise<boolean> {
  if (useSessionStore.getState().accessToken) return true
  return (await refreshAccessToken()) !== null
}

interface RequestOptions {
  method?: string
  body?: unknown
  /** Skip the Authorization header, for login and register. */
  anonymous?: boolean
  signal?: AbortSignal
}

async function request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, anonymous = false, signal } = opts

  const send = async (token: string | null): Promise<Response> => {
    const headers: Record<string, string> = {}
    if (body !== undefined) headers['Content-Type'] = 'application/json'
    if (token && !anonymous) headers.Authorization = `Bearer ${token}`

    return fetch(`${BASE}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
      signal,
      // Required for the refresh cookie to travel at all.
      credentials: 'include',
    })
  }

  let res: Response
  try {
    res = await send(useSessionStore.getState().accessToken)
  } catch {
    throw networkError()
  }

  // One silent refresh-and-retry on an expired access token.
  if (res.status === 401 && !anonymous) {
    refreshInFlight ??= refreshAccessToken().finally(() => {
      refreshInFlight = null
    })
    const fresh = await refreshInFlight

    if (!fresh) {
      useSessionStore.getState().clear()
      throw new ApiError(401, { message: 'Your session has expired', code: 'unauthorized' })
    }
    try {
      res = await send(fresh)
    } catch {
      throw networkError()
    }
  }

  if (res.status === 204) return undefined as T

  let payload: unknown
  try {
    payload = await res.json()
  } catch {
    payload = null
  }

  if (!res.ok) {
    const body = (payload as { error?: ApiErrorBody } | null)?.error
    throw new ApiError(res.status, body ?? {
      message: 'Something went wrong. Try again.',
      code: 'unknown',
    })
  }
  return payload as T
}

export const api = {
  get: <T>(path: string, signal?: AbortSignal) => request<T>(path, { signal }),
  post: <T>(path: string, body?: unknown, opts?: { anonymous?: boolean }) =>
    request<T>(path, { method: 'POST', body, anonymous: opts?.anonymous }),
}

/** Build a query string, dropping empty values so no `?limit=` ever goes out. */
export function qs(params: Record<string, string | number | boolean | undefined | null | string[]>): string {
  const sp = new URLSearchParams()
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined || value === null || value === '') continue
    if (Array.isArray(value)) {
      value.forEach((v) => sp.append(key, v))
    } else {
      sp.set(key, String(value))
    }
  }
  const s = sp.toString()
  return s ? `?${s}` : ''
}

export { BASE as API_BASE }
