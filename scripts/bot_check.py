#!/usr/bin/env python3
"""Verify an automated run end to end against a live API.

Starts a run on the mock feed, watches it place contracts and stop on one of its
own limits, then checks the three things that matter: every contract it placed is
attributable to it, its totals agree with those contracts, and the ledger still
agrees with the wallet.

This is the CI form of the manual curl walkthrough. It needs bot_enabled to be on,
which `go run ./cmd/seed` does in development.

    python3 scripts/bot_check.py [base_url]
"""
import json
import random
import sys
import time
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080/api/v1").rstrip("/")
TOKEN = None
failures = []


def call(method, path, body=None):
    req = urllib.request.Request(
        f"{BASE}{path}",
        data=json.dumps(body).encode() if body is not None else None,
        method=method,
    )
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", f"Bearer {TOKEN}")
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return r.status, json.loads(r.read() or "{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or "{}")


def check(label, condition, detail=""):
    if condition:
        print(f"  [PASS] {label}")
    else:
        print(f"  [FAIL] {label}" + (f" — {detail}" if detail else ""))
        failures.append(label)


print(f"Automated run check against {BASE}\n")

phone = f"07{random.randint(10_000_000, 99_999_999)}"
_, reg = call("POST", "/auth/register", {"phone_number": phone, "password": "hunter2024"})
TOKEN = reg["access_token"]
account = reg["account"]["id"]
start_balance = 0.0

print("FLAGS")
_, me = call("GET", "/auth/me")
check("the API reports whether this user may run a bot", "bot_enabled" in me)
if not me.get("bot_enabled"):
    # Not a failure of the bot: the flag is off, which is the shipped default.
    # Say so plainly rather than reporting a broken feature.
    print("\nautomated trading is disabled for this user; nothing to check.")
    print("enable it in development with: go run ./cmd/seed")
    sys.exit(0)
check("real-money runs are reported separately", "bot_real_money_enabled" in me)

_, wallet = call("GET", f"/accounts/{account}/wallet")
start_balance = float(wallet["real_balance"]) + float(wallet["bonus_balance"])

# --- Refusals ---------------------------------------------------------------
# The gates are worth exercising before the happy path, because a run that cannot
# be misconfigured is most of what makes this feature safe.
print("\nREFUSALS")
run_body = {
    "account_id": account,
    "symbol": "R_75",
    "contract_type": "even_odd",
    "selection": {"parity": "even"},
    "stake": "10",
    "interval_ms": 1000,
    "limits": {"stop_loss": "40", "max_contracts": 6},
}

no_stop = dict(run_body, limits={})
status, body = call("POST", "/bot/runs", no_stop)
check("a run with no stop loss is refused", status == 422, f"got {status}")
check("and the refusal names the field", body.get("error", {}).get("field") == "stop_loss",
      json.dumps(body.get("error", {})))

too_big = dict(run_body, stake="100000")
status, body = call("POST", "/bot/runs", too_big)
check("a stake beyond the ceiling is refused", status == 422, f"got {status}")

too_fast = dict(run_body, interval_ms=10)
status, body = call("POST", "/bot/runs", too_fast)
check("an interval below the floor is refused", status == 422, f"got {status}")
check("and it names the interval", body.get("error", {}).get("field") == "interval_ms",
      json.dumps(body.get("error", {})))

# --- A run ------------------------------------------------------------------
print("\nRUN")
status, run = call("POST", "/bot/runs", run_body)
check("a valid run starts", status == 201, f"got {status}: {json.dumps(run)[:200]}")
if status != 201:
    print("\ncannot continue without a run")
    sys.exit(1)
run_id = run["id"]
check("it starts in the running state", run["status"] == "running", run["status"])

status, second = call("POST", "/bot/runs", run_body)
check("a second run on the same account is refused", status == 409, f"got {status}")
check("and says why", second.get("error", {}).get("code") == "bot_run_active",
      json.dumps(second.get("error", {})))

# Contracts settle about once a second on the mock feed, so six placements plus
# their settlements fit well inside this budget.
print("\n  watching the run…")
final = None
deadline = time.time() + 90
last = None
while time.time() < deadline:
    _, r = call("GET", f"/bot/runs/{run_id}")
    shown = (r["status"], r["contracts_placed"], r["wins"], r["losses"], r["voids"], r["realised_pnl"])
    if shown != last:
        print(f"    {shown}")
        last = shown
    if r["status"] == "stopped":
        final = r
        break
    time.sleep(1)

check("the run stopped on its own", final is not None, "still running after 90s")
if final is None:
    call("POST", f"/bot/runs/{run_id}/stop")
    print("\n" + "=" * 60)
    print("FAILED: the run never stopped")
    sys.exit(1)

# Any of the run's own limits is a correct ending here; what must not happen is
# an internal fault or a run that stopped for no recorded reason.
expected = {"max_contracts", "stop_loss", "take_profit", "max_consecutive_losses", "max_duration"}
check("it stopped for a reason it was given", final["stop_reason"] in expected,
      f"stop_reason={final['stop_reason']} last_error={final.get('last_error')}")
check("no internal error was recorded", not final.get("last_error"), str(final.get("last_error")))

# --- Provenance -------------------------------------------------------------
print("\nPROVENANCE")
_, listed = call("GET", f"/digit-contracts?account_id={account}&bot_run_id={run_id}&limit=50")
items = listed.get("items", [])
check("the run's contracts can be listed by run id", len(items) == final["contracts_placed"],
      f"{len(items)} listed vs {final['contracts_placed']} placed")
check("every contract carries the run id", all(c["bot_run_id"] == run_id for c in items))

_, ledger = call("GET", f"/wallet/transactions?account_id={account}&limit=100")
opens = [t for t in ledger["items"] if t["type"] == "digit_open"]
check("every debit names the run in its metadata",
      opens and all((t.get("metadata") or {}).get("bot_run_id") == run_id for t in opens),
      f"{len(opens)} digit_open rows")

# --- The totals agree with the contracts ------------------------------------
print("\nTOTALS")
wins = sum(1 for c in items if c["status"] == "won")
losses = sum(1 for c in items if c["status"] == "lost")
voids = sum(1 for c in items if c["status"] == "void")
realised = sum(float(c["pnl"]) for c in items if c["pnl"] is not None)

check("wins agree", wins == final["wins"], f"contracts {wins} vs run {final['wins']}")
check("losses agree", losses == final["losses"], f"contracts {losses} vs run {final['losses']}")
check("voids agree", voids == final["voids"], f"contracts {voids} vs run {final['voids']}")
check("realised P/L agrees", abs(realised - float(final["realised_pnl"])) < 0.01,
      f"contracts {realised:.2f} vs run {final['realised_pnl']}")
check("nothing was left open", all(c["status"] != "open" for c in items))

# A void is a refund, so it must have moved nothing.
void_pnl = [float(c["pnl"]) for c in items if c["status"] == "void" and c["pnl"] is not None]
check("voided contracts cost nothing", all(abs(p) < 0.01 for p in void_pnl), str(void_pnl))

# --- Money ------------------------------------------------------------------
print("\nMONEY")
_, wallet = call("GET", f"/accounts/{account}/wallet")
end_balance = float(wallet["real_balance"]) + float(wallet["bonus_balance"])
moved = end_balance - start_balance
check("the balance moved by exactly the run's realised P/L",
      abs(moved - float(final["realised_pnl"])) < 0.01,
      f"balance moved {moved:.2f}, run realised {final['realised_pnl']}")

# The invariant the whole money design rests on.
total = sum(float(t["amount"]) for t in ledger["items"] if t["status"] == "completed")
check("the ledger still agrees with the wallet cache", abs(total - end_balance) < 0.01,
      f"ledger {total:.2f} vs cache {end_balance:.2f}")

# --- Stopping is idempotent -------------------------------------------------
print("\nSTOPPING")
status, again = call("POST", f"/bot/runs/{run_id}/stop")
check("stopping an already-stopped run is not an error", status == 200, f"got {status}")
check("and the original reason survives", again.get("stop_reason") == final["stop_reason"],
      f"{again.get('stop_reason')} vs {final['stop_reason']}")

print("\n" + "=" * 60)
if failures:
    print(f"FAILED: {len(failures)}")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("AUTOMATED RUN VERIFIED")
