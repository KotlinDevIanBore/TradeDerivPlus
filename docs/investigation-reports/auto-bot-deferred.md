# AUTO mode — deferred items

Anything consciously put off during the AUTO-mode phases, with where it came from and where it is
meant to land. The point is that a promise made in one checkpoint's report does not depend on someone
rereading every prior report to notice it was made.

**Append when you defer something. Move it to Closed when it lands, with the checkpoint that did it.**

---

## Open

Phase 4's first item is closed (see Closed). What is left here is a product
decision, a tooling decision, or a ticket of its own — nothing that blocks the
rest of Phase 4.


| Item | Promised in | Target | Notes |
|---|---|---|---|
| The e2e suite is not typechecked | P3 CP4 | Tooling decision | `tsconfig.app.json` includes `src` only, so nothing type-checks `web/e2e`. Playwright transpiles specs without checking them, so a wrong field name on a fixture is a runtime `undefined` rather than a build error — which is how a spec can assert against nothing and pass. Adding `e2e` to a project reference is small; whether it is clean today is unknown, which is why this is not being done at the end of a phase. |
| Real-money runs (`bot_real_money_enabled` → true) | Plan §10.3 | Product decision | Gating UI is built and tested in all three states (P2 CP2). Flipping it is a product call, not an engineering one, and §10.1 — whether to ship at all against a placeholder payout table — comes first. |
| UI copy sign-off on negative expected value | Plan §10.2 | Product decision | The ticket states expected return per contract and a money worst case. Whether that is the right disclosure is a compliance question engineering cannot answer. |
| Regulatory treatment of automated trading | Plan §10.4 | Product decision | Unaffected by any phase so far. |
| Restricting which contract types a run may use | Plan §10.5 | Product decision | All three are offered with the Matches warning (P2 kickoff default). Narrowing to the −0.50% band is a one-line server change if wanted. |
| Manual-API idempotency key on `POST /digit-contracts` | Investigation §6.3, plan §3.3 | Its own ticket | Explicitly out of scope for the bot: the run's own path is sequential and never retries, so it does not need one. The *manual* surface still lacks it. |
| `.prettierrc` for the web package | P2 CP2 | Tooling decision | There is no prettier config; house style (single quotes, no semicolons, width 100) is maintained by hand, and running prettier with its defaults silently churns files. A config would express the style rather than requiring it to be remembered. Not added unilaterally — it changes the project's tooling posture. |
| Order-dependent admin KYC browser test | P0 report §7 | Spawned task `task_766655c6` | Fails after `admin_check.py` has run, passes alone, self-skips once the queue is empty. Reproduced on unmodified baseline, so it is not from this work. |
| Past-runs list ("Runs" tab in the Positions panel) | Plan §6 Phase 4 | Phase 4, optional | `useBotRuns` is now used for the session strip's "last run ended with…" cell, but there is still no screen listing a run's history. |
| A rate-limited token refresh signs the user out | P2 CP4 | Its own ticket | `refreshAccessToken` clears the session on any non-2xx (`web/src/api/client.ts:60`), so a 429 or a 502 on `POST /auth/refresh` is indistinguishable from a revoked refresh token. Reproduced in the browser: a reload during a limiter burst landed on the login screen with a valid session. Only 401/403 mean the credential is no good; everything else is transient and should be retried, most of all at `bootstrapSession`, where it is the difference between a reload and a logout. Not fixed here — it is auth, not the bot, and predates all of this. |
| CORS preflights are charged to the auth limiter | P2 CP4 | Its own ticket | Every browser signup spends two tokens, not one: the `OPTIONS` preflight is counted alongside the `POST` (54 preflights for 54 signup attempts in one suite run). It halves the effective auth burst for real browsers while leaving it intact for a non-browser client, which is the wrong way round. |

### Phase 3's four prerequisites — closed by P3 CP1

Left here rather than deleted, because the table is what the fixes were measured
against. Each row is now done; see `phase3-checkpoint-1.md`.

| Gap | Closed by |
|---|---|
| `GET /admin/bot/runs?status=stopped` returned an empty list by design | `BotRunRepo.ListAll` replaces the early return; live, finished or both, and paged |
| No admin single-run stop | `POST /admin/bot/runs/:id/stop` → `AdminService.StopBotRun` |
| A run carried no owner identity | `repository.AdminBotRun` carries account number, account type, user id and phone, on the admin read path only |
| No audited action for stopping one run | `models.ActionBotRunStop` over `models.TargetBotRun`, written in the same transaction as the status change |

## Closed

| Item | Promised in | Closed by | Notes |
|---|---|---|---|
| `digit_max_stake` on `/auth/me` | P0 report §6 ("moves onto `/auth/me` in Phase 1") | **P2 CP2** | Phase 1 did not pick it up; the browser check for §5.2's "server's maximum" hint failed without it. One string field on `meResponse`. |
| `?bot_run_id=` filter on the digit list | P1 CP3 §3 (deferred as untested code) | **P1 CP4** | Implemented between checkpoints; covered by `TestDigitContractsFilterByBotRun`. |
| Digit e2e coverage in CI | Investigation §8.1 | **P0** | Its own job, `e2e-digit`, with its own database. |
| Per-contract toasts drowning a run | Found in P2 CP3 | **P2 CP3** | `digit_settled` toasts fire per contract, which is right for manual trading and a stream under a run. Suppressed when the contract carries a `bot_run_id`. |
| Browser tests verifying a stale build | P2 CP1 §3 | **P2 CP2** | Playwright runs its own dev server on its own port; `CORS_ORIGIN` became a list to allow it. |
| A user stop announced itself twice | P2 CP4 §3.2 | **P3 CP1** | `Finalise` now reports whether it wrote the ending, and only the winner broadcasts or counts the stop. The client-side guard from P2 CP4 stays: a duplicate delivery must not double-toast whatever caused it. Covered by `TestARunStopIsAnnouncedOnce`, which fails if either gate is removed. |
| Admin UI for runs and the kill switch | Plan §5.4 | **P3 CP2–CP3** | The dashboard's sixth tile and the kill switch (CP2); `/admin/bot-runs` with live and finished runs, a per-row stop and the tile's link to it (CP3). |
| Admin e2e for the kill switch | Plan §6 Phase 3 | **P3 CP4** | `admin.spec.ts` — the §6 case, plus a second for the per-row stop that postdates §6's wording. |
| The kill switch stopped announcing itself | Introduced P3 CP1, found P3 CP4 | **P3 CP4** | CP1's "only whoever wrote the ending announces it" silenced the kill switch, whose ending is written by one UPDATE that no goroutine wins. `StopAll` now announces what it stops. Covered by an assertion in `TestBotKillSwitchStopsAllRuns`, checked by mutation. |
| The console can turn automated trading off and cannot turn it back on | P3 CP4 | **P3.5** | `POST /admin/bot/enable`, audited under its own `bot_enable` action and requiring a reason for the same argument that makes `unfreeze_account` require one. The dashboard reads the switch's current state and offers the matching control. The kill switch is no longer a decision only a database can undo, and the admin e2e no longer ends with the feature off. |
| A stop landing mid-placement frees the live-run slot | P3 CP1 | **P4 CP1** | Both paths now take the run row's lock: `DigitService.Open` before it inserts, and `applyStop` before it counts. That leaves exactly two orderings, and both are correct — the stop waits for a placement already holding the row and then counts it, or the placement finds the ending and refuses with `ErrBotRunNotLive`. The user's stop and the console's share `applyStop`, so they cannot disagree. Covered by `TestPlacementRefusedOnceItsRunIsStopped` and `TestStopWaitsForAPlacementAlreadyHoldingTheRun`; removing either lock reproduces the original symptom. |
| `bot_contracts_total` counted every contract twice | P4 CP2 | **P4 CP3** | Placements moved to `bot_contracts_placed_total`; `bot_contracts_total{outcome}` now counts settlements, one per contract. §2.9's alert is corrected to `increase(...)` — `rate()` is per second and the threshold was per minute, so as written it needed sixty contracts per second per run and could never fire. Both errors are recorded in the plan against the table they came from. Measured after the fix: 20 runs, 400 settled and 400 placed per minute, threshold 1,200, ratio 0.33. |
| `bot_placement_errors_total` was never built | Plan §2.9 | **P4 CP3** | Built, labelled by the error's own code (`insufficient_funds`, `account_frozen`, `market_closed`, `no_price`, `too_many_open`, `bot_run_not_live`, `error`) — the same vocabulary the API answers with. Recorded in one place rather than per branch, so a branch added later cannot quietly stop counting. |
