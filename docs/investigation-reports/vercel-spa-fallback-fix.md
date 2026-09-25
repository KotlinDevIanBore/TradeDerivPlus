# Fix: Vercel served its own 404 for every client-side route

**Date:** 25 September 2026 · **Branch:** `fix/vercel-spa-fallback` (off `master`)
**Kind:** one-rule configuration fix. No application code changed.

---

## 1. The symptom

`GET https://trade-deriv-plus.vercel.app/admin/login` returned Vercel's platform 404 rather than
the app. It was reported as an admin-console problem. It was not: **every** client-side route was
returning 404, and had been since the deployment existed.

The 404 was the platform's, not the app's — `x-vercel-error: NOT_FOUND`,
`content-type: text/plain`, body `The page could not be found / NOT_FOUND`. React Router never
ran, so the app's own catch-all route (`App.tsx:83`, a redirect to `/`) could not fire.

| Path | Before |
|---|---|
| `/` | 200 (`text/html`) |
| `/login`, `/register`, `/portfolio`, `/trade`, `/wallet`, `/signals`, `/profile` | 404 |
| `/admin`, `/admin/login` | 404 |
| `/nonexistent-path-xyz` | 404 |

Only the root worked. This had gone unnoticed because navigating *within* the app never asks the
server — client-side routing handles it — so the failure surfaces only on a pasted URL, a refresh,
or a bookmark. Deep links and reloads were broken for the whole application, not just the console.

## 2. The cause

`web/vercel.json` held exactly one rewrite: `/api/:path*` to the Railway backend. There was no
catch-all returning `/index.html`, so any path that is neither a real file in the build output nor
under `/api` matched nothing, and Vercel answered 404 before the app loaded.

## 3. Ruling out the other explanation first

"Config not loaded" and "config incomplete" need different fixes, so the distinction was checked
rather than assumed. `GET /api/v1/symbols` through the Vercel domain returned the backend's own
`401 {"error":{"code":"unauthorized","message":"Sign in to continue"}}` carrying
`x-railway-edge: lhr1` and an `x-railway-request-id`. That response can only originate at Railway,
so the rewrite was active and the project's root directory was correctly pointed at `web/`. The
file was being read; it was simply incomplete.

## 4. The change

```json
{
  "rewrites": [
    { "source": "/api/:path*", "destination": "https://tradederivplus-production.up.railway.app/api/:path*" },
    { "source": "/(.*)", "destination": "/index.html" }
  ]
}
```

Two properties of that ordering matter:

- **The `/api` rule must stay first.** Vercel evaluates rewrites top-down, first match wins. With
  the catch-all ahead of it, every API call would be answered with HTML — surfacing as a JSON
  parse error in the client rather than as a routing bug, which is a considerably worse failure to
  diagnose than the one being fixed.
- **Static assets are unaffected.** Vercel checks the filesystem before applying rewrites, so
  `/assets/index-<hash>.js` is served as a real file and never reaches the catch-all. This is why
  the root page already loaded its JS and CSS correctly.

## 5. Verification

Re-run of the report's own route list against production after the deploy, plus the proxy check
that the catch-all could have broken.

VERIFICATION_PLACEHOLDER

## 6. Notes

The Vercel project's framework preset is worth checking: a "Vite" preset handles some of this
automatically, and its absence is the likely reason the fallback was missing. The explicit rewrite
is the more robust fix regardless, since it does not depend on preset detection.

Frontend routing only. No backend change, no flag change; `withdrawals_enabled` and
`bot_real_money_enabled` both remain false.
