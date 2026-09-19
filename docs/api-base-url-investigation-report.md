# API base URL and route-path investigation report

## Executive summary

This is a configuration mismatch, not a frontend route-prefix bug.

The frontend API client builds requests as:

```ts
const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'
return fetch(`${BASE}${path}`, ...)
```

That means `VITE_API_URL` must already include the versioned API prefix. The backend is mounted at `/api/v1`, and all route strings in `web/src/api/*.ts` are relative paths such as `/auth/register`, `/accounts`, `/wallet/deposit`, etc.

The correct production value is:

```text
VITE_API_URL=/api/v1
```

or, if the deployment environment expects a full origin URL:

```text
https://<your-domain>/api/v1
```

Not `/api`.

---

## 1) Where the base URL and route path are actually joined

The join point is in `web/src/api/client.ts`:

```ts
import { useSessionStore } from '@/store/session'

const BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'
```

and later:

```ts
return fetch(`${BASE}${path}`, {
  method,
  headers,
  body: body === undefined ? undefined : JSON.stringify(body),
  signal,
  credentials: 'include',
})
```

This proves the client does not append `/v1` automatically. It expects the configured base already to contain `/api/v1`.

If `VITE_API_URL` is set to `/api`, then the resulting URL becomes `/api` + `/auth/register` = `/api/auth/register`.

But the actual backend register endpoint is:

```go
v1 := r.Group("/api/v1")
authRoutes.POST("/register", d.Handler.Auth.Register)
```

So the real path is `/api/v1/auth/register`.

---

## 2) Exact path strings used in each API file

### `web/src/api/auth.ts`

```ts
register: (input: RegisterInput) =>
  api.post<AuthResult>('/auth/register', input, { anonymous: true }),

login: (phone_number: string, password: string) =>
  api.post<AuthResult>('/auth/login', { phone_number, password }, { anonymous: true }),

refresh: () => api.post<AccessGrant>('/auth/refresh', undefined, { anonymous: true }),

logout: () => api.post<void>('/auth/logout'),

me: () => api.get<User>('/auth/me'),
```

This file calls `/auth/register`, `/auth/login`, `/auth/refresh`, `/auth/logout`, and `/auth/me`.

### `web/src/api/accounts.ts`

```ts
list: () => api.get<List<Account>>('/accounts'),

create: (account_type: AccountType) => api.post<Account>('/accounts', { account_type }),

wallet: (accountId: string) => api.get<Wallet>(`/accounts/${accountId}/wallet`),
```

This file calls `/accounts`, `/accounts/:id/wallet`.

### `web/src/api/wallet.ts`

```ts
deposit: (input: DepositInput) => api.post<DepositResponse>('/wallet/deposit', input),

withdraw: (input: WithdrawInput) => api.post<Transaction>('/wallet/withdraw', input),

transactions: (accountId: string, opts: { limit?: number; page?: number; types?: TxType[] } = {}) =>
  api.get<Page<Transaction>>(
    `/wallet/transactions${qs({
      account_id: accountId,
      limit: opts.limit ?? 25,
      page: opts.page ?? 1,
      type: opts.types,
    })}`,
  ),

transaction: (id: string) => api.get<Transaction>(`/wallet/transactions/${id}`),

rate: () => api.get<{ kes_per_usd: number }>('/wallet/rate'),
```

This file calls `/wallet/deposit`, `/wallet/withdraw`, `/wallet/transactions`, `/wallet/transactions/:id`, and `/wallet/rate`.

### `web/src/api/trades.ts`

```ts
list: (accountId: string, status?: TradeStatus, opts: { limit?: number; page?: number } = {}) =>
  api.get<Page<Trade>>(
    `/trades${qs({
      account_id: accountId,
      status,
      limit: opts.limit ?? 25,
      page: opts.page ?? 1,
    })}`,
  ),

summary: (accountId: string) =>
  api.get<TradeSummary>(`/trades/summary${qs({ account_id: accountId })}`),

open: (input: OpenTradeInput) => api.post<Trade>('/trades', input),

close: (tradeId: string) => api.post<Trade>(`/trades/${tradeId}/close`),
```

This file calls `/trades`, `/trades/summary`, `/trades/:id/close`.

### `web/src/api/market.ts`

```ts
symbols: () => api.get<List<TradeSymbol>>('/symbols'),

ticks: (code: string, minutes = 5) =>
  api.get<{ symbol: string; items: { price: string; epoch: number }[] }>(
    `/symbols/${encodeURIComponent(code)}/ticks${qs({ minutes })}`,
  ),

signals: (symbol?: string, limit = 20) =>
  api.get<List<Signal>>(`/signals${qs({ symbol, limit })}`),
```

This file calls `/symbols`, `/symbols/:code/ticks`, and `/signals`.

---

## 3) Correct `VITE_API_URL` value

The correct value is:

```text
/api/v1
```

Reason:

- `client.ts` sets `BASE = import.meta.env.VITE_API_URL ?? 'http://localhost:8080/api/v1'`
- `request()` then calls `fetch(`${BASE}${path}`)`
- The route strings all start with `/auth`, `/accounts`, `/wallet`, `/trades`, `/symbols`, etc.
- Therefore the base must include `/api/v1` so the final URL is `/api/v1/auth/register`, `/api/v1/accounts`, and so on.

This is not a guess; it is the actual implementation.

---

## 4) Is this a config-only mismatch or a code bug?

This is a config mismatch only.

There is no inconsistent path prefix across the frontend API files. The repo is internally consistent:

- `auth.ts` uses `/auth/register`
- `wallet.ts` uses `/wallet/deposit`
- `trades.ts` uses `/trades`
- `market.ts` uses `/symbols`
- `accounts.ts` uses `/accounts`

All of them assume the base already includes `/api/v1`.

There is no file in `web/src/api/` that says `/v1/auth/register` while another says `/auth/register`; they all follow the same pattern.

So this is not a code bug in the API client files; it is a deployment environment variable bug.

---

## 5) doc/API contract drift check

### `docs/api-contract.md`

The doc explicitly states:

```md
Base URL: `http://localhost:8080/api/v1`
```

This matches the backend mount and the client implementation.

The route sections are written relative to that base, e.g.:

```md
### `POST /auth/register`
```

which is intended to be resolved against `/api/v1`.

### Backend router

In `internal/routes/routes.go`:

```go
v1 := r.Group("/api/v1")

authRoutes := v1.Group("/auth", ...)
{
  authRoutes.POST("/register", d.Handler.Auth.Register)
  authRoutes.POST("/login", d.Handler.Auth.Login)
  authRoutes.POST("/refresh", d.Handler.Auth.Refresh)
  authRoutes.POST("/logout", d.Handler.Auth.Logout)
}
```

This matches `/api/v1/auth/register`, `/api/v1/auth/login`, etc.

The same pattern is used for all routes:

```go
protected.GET("/accounts", d.Handler.Account.List)
protected.GET("/wallet/transactions", d.Handler.Wallet.Transactions)
protected.GET("/trades", d.Handler.Trade.List)
protected.GET("/symbols", d.Handler.Market.Symbols)
```

These are all anchored under `/api/v1`.

### Findings on drift

No genuine route-path drift was found between the API contract and the registered backend routes.

The contract and implementation are aligned on the versioned mount point:

- contract base: `/api/v1`
- actual router base: `/api/v1`

The only differences are documentation-only details or historical wording in some planning docs, not actual runtime route mismatches.

---

## Conclusion

The app is correctly written to expect `/api/v1` in `VITE_API_URL`.

The frontend source files are consistent with each other and with the server mount point.

The production variable should be configured to:

```text
VITE_API_URL=/api/v1
```

and not `/api`.

No code fix is needed unless you want to harden the config by adding validation or documentation at the frontend layer, but the current route definitions themselves are not buggy.
