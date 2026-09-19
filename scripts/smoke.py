#!/usr/bin/env python3
"""End-to-end smoke test against a running API.

Exercises the whole money path: register -> trade -> close -> deposit, and
asserts the ledger and wallet agree at each step.

    python3 scripts/smoke.py [base_url]
"""
import json
import os
import random
import sys
import time
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080/api/v1").rstrip("/")
TOKEN = None
COOKIES = {}
failures = []


def call(method, path, body=None, expect=None):
    """Cookie-aware: the refresh token lives in an httpOnly cookie now, so the
    client has to hold one the way a browser does."""
    url = f"{BASE}{path}"
    data = json.dumps(body).encode() if body is not None else None
    req = urllib.request.Request(url, data=data, method=method)
    req.add_header("Content-Type", "application/json")
    if TOKEN:
        req.add_header("Authorization", f"Bearer {TOKEN}")
    if COOKIES:
        req.add_header("Cookie", "; ".join(f"{k}={v}" for k, v in COOKIES.items()))

    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            status, payload, headers = r.status, json.loads(r.read() or "{}"), r.headers
    except urllib.error.HTTPError as e:
        status, payload, headers = e.code, json.loads(e.read() or "{}"), e.headers

    for raw in headers.get_all("Set-Cookie") or []:
        name, _, rest = raw.partition("=")
        value = rest.split(";")[0]
        if value:
            COOKIES[name] = value
        else:
            COOKIES.pop(name, None)

    if expect is not None and status != expect:
        failures.append(f"{method} {path} -> {status}, expected {expect}: {payload}")
    return status, payload


def check(label, condition, detail=""):
    mark = "PASS" if condition else "FAIL"
    if not condition:
        failures.append(f"{label}: {detail}")
    print(f"  [{mark}] {label}" + (f"  {detail}" if detail and not condition else ""))


print(f"Smoke test against {BASE}\n")

# --- Auth ------------------------------------------------------------------
print("AUTH")
phone = f"07{random.randint(10_000_000, 99_999_999)}"
_, reg = call("POST", "/auth/register",
              {"full_name": "Smoke Test", "phone_number": phone, "password": "hunter2024"},
              expect=201)
TOKEN = reg["access_token"]
demo_account = reg["account"]["id"]
check("register returns an access token", bool(TOKEN))
check("register does NOT return a refresh token in the body", "refresh_token" not in reg)
check("register sets an httpOnly refresh cookie", "tdp_refresh" in COOKIES)
check("register creates a demo account", reg["account"]["account_type"] == "demo")

_, dup = call("POST", "/auth/register",
              {"phone_number": phone, "password": "hunter2024"}, expect=409)
check("duplicate phone is rejected", dup.get("error", {}).get("code") == "phone_taken")

_, weak = call("POST", "/auth/register",
               {"phone_number": f"07{random.randint(10_000_000, 99_999_999)}", "password": "short"},
               expect=422)
check("short password is rejected", weak.get("error", {}).get("field") == "password")

_, bad = call("POST", "/auth/login", {"phone_number": phone, "password": "wrongpass"}, expect=401)
check("wrong password is rejected", bad.get("error", {}).get("code") == "invalid_credentials")

_, login = call("POST", "/auth/login", {"phone_number": phone, "password": "hunter2024"}, expect=200)
check("login succeeds", bool(login.get("access_token")))
check("login does NOT return a refresh token in the body", "refresh_token" not in login)
TOKEN = login["access_token"]

# Refresh reads the cookie; there is no token to pass.
_, refreshed = call("POST", "/auth/refresh", expect=200)
check("refresh works from the cookie alone", bool(refreshed.get("access_token")))
TOKEN = refreshed["access_token"]

# And without the cookie it must fail, so the cookie is doing the work.
saved_cookies = dict(COOKIES)
COOKIES.clear()
_, nocookie = call("POST", "/auth/refresh", expect=401)
check("refresh without the cookie is refused", nocookie.get("error", {}).get("code") == "invalid_token")
COOKIES.update(saved_cookies)

_, me = call("GET", "/auth/me", expect=200)
check("me returns the profile", me["kyc_status"] == "pending")

# --- Accounts and wallet ---------------------------------------------------
print("\nACCOUNTS")
_, accounts = call("GET", "/accounts", expect=200)
check("one account after signup", len(accounts["items"]) == 1)

_, wallet = call("GET", f"/accounts/{demo_account}/wallet", expect=200)
check("demo starts with bonus balance", float(wallet["bonus_balance"]) == 10000.0)
check("demo starts with zero real balance", float(wallet["real_balance"]) == 0.0)

_, real = call("POST", "/accounts", {"account_type": "real"}, expect=201)
real_account = real["id"]
check("real account is created", real["account_type"] == "real")

_, dupacc = call("POST", "/accounts", {"account_type": "real"}, expect=409)
check("second real account is refused", dupacc.get("error", {}).get("code") == "account_exists")

# --- Market ----------------------------------------------------------------
print("\nMARKET")
_, symbols = call("GET", "/symbols", expect=200)
check("symbols are seeded", len(symbols["items"]) >= 12)
priced = [s for s in symbols["items"] if "price" in s]
check("symbols carry a live price", len(priced) > 0, f"{len(priced)} of {len(symbols['items'])} priced")
sym = priced[0]["code"] if priced else "R_75"

_, ticks = call("GET", f"/symbols/{sym}/ticks?minutes=5", expect=200)
check("tick history is buffered", len(ticks["items"]) > 0, f"{len(ticks['items'])} ticks")

# --- Trading ---------------------------------------------------------------
print("\nTRADING")
_, before = call("GET", f"/accounts/{demo_account}/wallet", expect=200)
start_balance = float(before["bonus_balance"])

_, trade = call("POST", "/trades",
                {"account_id": demo_account, "symbol": sym, "direction": "buy", "stake": "100"},
                expect=201)
check("trade opens", trade.get("status") == "open")
check("entry price comes from the server", float(trade["entry_price"]) > 0)
trade_id = trade["id"]

_, after_open = call("GET", f"/accounts/{demo_account}/wallet", expect=200)
check("stake is debited on open",
      abs(float(after_open["bonus_balance"]) - (start_balance - 100)) < 0.01,
      f"{after_open['bonus_balance']} vs expected {start_balance - 100}")

_, open_list = call("GET", f"/trades?account_id={demo_account}&status=open", expect=200)
check("open position is listed", any(t["id"] == trade_id for t in open_list["items"]))

_, summary = call("GET", f"/trades/summary?account_id={demo_account}", expect=200)
check("summary counts the position", summary["open_positions"] == 1)

# Invalid stop loss for a buy must be refused.
entry = float(trade["entry_price"])
_, badsl = call("POST", "/trades",
                {"account_id": demo_account, "symbol": sym, "direction": "buy",
                 "stake": "10", "stop_loss": str(entry * 1.5)}, expect=422)
check("stop loss above entry is refused on a buy",
      badsl.get("error", {}).get("field") == "stop_loss")

_, over = call("POST", "/trades",
               {"account_id": demo_account, "symbol": sym, "direction": "buy", "stake": "999999"},
               expect=400)
check("oversized stake is refused", over.get("error", {}).get("code") == "insufficient_funds")

time.sleep(2)  # let the price move so PnL is non-trivial
_, closed = call("POST", f"/trades/{trade_id}/close", expect=200)
check("trade closes", closed.get("status") == "closed")
check("close records a pnl", closed.get("pnl") is not None)
check("close records an exit price", closed.get("exit_price") is not None)

_, reclose = call("POST", f"/trades/{trade_id}/close", expect=409)
check("closing twice is refused", reclose.get("error", {}).get("code") == "trade_not_open")

_, after_close = call("GET", f"/accounts/{demo_account}/wallet", expect=200)
expected = start_balance - 100 + max(0.0, 100 + float(closed["pnl"]))
check("stake plus pnl is returned on close",
      abs(float(after_close["bonus_balance"]) - expected) < 0.02,
      f"{after_close['bonus_balance']} vs expected {expected:.2f}")

# --- Wallet: deposits ------------------------------------------------------
print("\nDEPOSIT (mock M-Pesa)")
_, demo_dep = call("POST", "/wallet/deposit",
                   {"account_id": demo_account, "amount_kes": "1000", "phone_number": phone},
                   expect=400)
check("deposit to a demo account is refused",
      demo_dep.get("error", {}).get("code") == "real_account_required")

_, tiny = call("POST", "/wallet/deposit",
               {"account_id": real_account, "amount_kes": "10", "phone_number": phone}, expect=422)
check("below-minimum deposit is refused", tiny.get("error", {}).get("field") == "amount")

_, badphone = call("POST", "/wallet/deposit",
                   {"account_id": real_account, "amount_kes": "1000", "phone_number": "0812345678"},
                   expect=422)
check("non-Safaricom number is refused", badphone.get("error", {}).get("field") == "phone_number")

_, dep = call("POST", "/wallet/deposit",
              {"account_id": real_account, "amount_kes": "12950", "phone_number": phone}, expect=202)
check("deposit starts pending", dep["status"] == "pending")
check("KES converts to USD", abs(float(dep["amount_usd"]) - 100.0) < 0.01,
      f"got {dep['amount_usd']}")

tx_id = dep["transaction_id"]
print("  waiting for the simulated callback...")
final = None
for _ in range(40):
    time.sleep(0.5)
    _, tx = call("GET", f"/wallet/transactions/{tx_id}", expect=200)
    if tx["status"] != "pending":
        final = tx
        break
check("deposit settles", final is not None, "still pending after 20s")
if final:
    print(f"       settled as: {final['status']}  ({final.get('metadata', {}).get('reason', '')})")
    _, rw = call("GET", f"/accounts/{real_account}/wallet", expect=200)
    if final["status"] == "completed":
        check("completed deposit credits the real balance", float(rw["real_balance"]) == 100.0,
              f"got {rw['real_balance']}")
    else:
        check("failed deposit credits nothing", float(rw["real_balance"]) == 0.0,
              f"got {rw['real_balance']}")

# --- Wallet: withdrawals ---------------------------------------------------
print("\nWITHDRAW")
_, wd = call("POST", "/wallet/withdraw",
             {"account_id": real_account, "amount_usd": "10", "phone_number": phone}, expect=403)
check("withdrawal is gated behind KYC", wd.get("error", {}).get("code") == "kyc_required")

# --- Ledger ----------------------------------------------------------------
print("\nLEDGER")
_, ledger = call("GET", f"/wallet/transactions?account_id={demo_account}", expect=200)
kinds = [t["type"] for t in ledger["items"]]
check("ledger records the bonus credit", "bonus_credit" in kinds)
check("ledger records the open", "trade_open" in kinds)
check("ledger records the close", "trade_close" in kinds)
check("ledger is paginated", all(k in ledger for k in ("total", "limit", "page", "pages")))

_, filtered = call("GET", f"/wallet/transactions?account_id={demo_account}&type=trade_open", expect=200)
check("ledger filters by type",
      all(t["type"] == "trade_open" for t in filtered["items"]) and len(filtered["items"]) > 0)

# --- Authorisation ---------------------------------------------------------
print("\nAUTHORISATION")
_, other = call("POST", "/auth/register",
                {"phone_number": f"07{random.randint(10_000_000, 99_999_999)}",
                 "password": "hunter2024"}, expect=201)
stolen, TOKEN = TOKEN, other["access_token"]
_, denied = call("GET", f"/accounts/{demo_account}/wallet", expect=403)
check("another user cannot read this wallet", denied.get("error", {}).get("code") == "forbidden")
_, denied2 = call("POST", f"/trades/{trade_id}/close", expect=403)
check("another user cannot close this trade", denied2.get("error", {}).get("code") == "forbidden")
TOKEN = None
_, anon = call("GET", "/accounts", expect=401)
check("unauthenticated requests are refused", anon.get("error", {}).get("code") == "unauthorized")

# --- Logout ---------------------------------------------------------------
print("\nLOGOUT")
TOKEN = stolen
call("POST", "/auth/logout", expect=204)
check("logout clears the refresh cookie", "tdp_refresh" not in COOKIES)
_, after_logout = call("POST", "/auth/refresh", expect=401)
check("refresh fails after logout", after_logout.get("error", {}).get("code") == "invalid_token")

# --- Rate limiting ---------------------------------------------------------
# The auth surface is the one unauthenticated write path, so its limiter is
# asserted rather than assumed. A sequential loop is too slow to drain the
# bucket on a fast refill, so this fires a genuine concurrent burst.
print("\nRATE LIMITING")


def burst_login(n=60):
    """Fire n login attempts at once and return the status codes."""
    import concurrent.futures

    def attempt(_):
        req = urllib.request.Request(
            f"{BASE}/auth/login",
            data=json.dumps({"phone_number": "254700000000", "password": "wrong"}).encode(),
            method="POST",
        )
        req.add_header("Content-Type", "application/json")
        try:
            with urllib.request.urlopen(req, timeout=20) as r:
                return r.status
        except urllib.error.HTTPError as e:
            return e.code
        except Exception:
            return 0

    with concurrent.futures.ThreadPoolExecutor(max_workers=n) as pool:
        return list(pool.map(attempt, range(n)))


codes = burst_login()
check("the auth limiter fires under a concurrent burst", 429 in codes,
      f"statuses seen: {sorted(set(codes))}")
check("the limiter refuses rather than erroring", all(c in (401, 429) for c in codes),
      f"unexpected statuses: {sorted(set(c for c in codes if c not in (401, 429)))}")

# --- Signals ---------------------------------------------------------------
print("\nSIGNALS")
TOKEN = stolen
_, sigs = call("GET", "/signals?limit=5", expect=200)
check("signals endpoint responds", "items" in sigs,
      f"{len(sigs.get('items', []))} signals so far")

print("\n" + "=" * 60)
if failures:
    print(f"FAILED: {len(failures)} check(s)")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("ALL CHECKS PASSED")
