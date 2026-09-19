#!/usr/bin/env python3
"""Verify stop-loss and take-profit settle automatically from the tick stream.

Opens two positions on a live symbol with levels placed just either side of the
current price, then waits for the server's tick watcher to close them.

    python3 scripts/sltp_check.py [base_url]
"""
import json
import random
import sys
import time
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080/api/v1").rstrip("/")
TOKEN = None


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


phone = f"07{random.randint(10_000_000, 99_999_999)}"
_, reg = call("POST", "/auth/register", {"phone_number": phone, "password": "hunter2024"})
TOKEN = reg["access_token"]
account = reg["account"]["id"]

_, symbols = call("GET", "/symbols")
priced = [s for s in symbols["items"] if "price" in s and s["is_active"]]
if not priced:
    print("FAIL: no symbol has a live price")
    sys.exit(1)

sym = priced[0]
code = sym["code"]
print(f"Using {code} at {sym['price']}\n")

failures = []


def open_with(direction, sl_mult, tp_mult, label):
    """Open a position with levels a hair either side of the current price."""
    _, symbols_now = call("GET", "/symbols")
    current = float(next(s["price"] for s in symbols_now["items"] if s["code"] == code))
    pip = sym["pip_size"]

    body = {
        "account_id": account,
        "symbol": code,
        "direction": direction,
        "stake": "50",
        "stop_loss": f"{current * sl_mult:.{pip}f}",
        "take_profit": f"{current * tp_mult:.{pip}f}",
    }
    status, trade = call("POST", "/trades", body)
    if status != 201:
        failures.append(f"{label}: open failed {status} {trade}")
        return None
    print(f"  opened {label}: entry {trade['entry_price']}  SL {trade['stop_loss']}  TP {trade['take_profit']}")
    return trade


# A buy with both levels within a hair of price settles within a few ticks
# whichever way the random walk moves.
print("Opening positions with levels a hair either side of price:")
buy = open_with("buy", 0.9995, 1.0005, "buy")
sell = open_with("sell", 1.0005, 0.9995, "sell")

print("\nWaiting for the tick watcher to settle them (up to 60s)...")
opened = [t for t in (buy, sell) if t]
settled = {}

for _ in range(60):
    time.sleep(1)
    _, closed = call("GET", f"/trades?account_id={account}&status=closed")
    for t in closed["items"]:
        if t["id"] not in settled:
            settled[t["id"]] = t
            print(f"  [PASS] {t['direction']} closed at {t['exit_price']}  pnl {t['pnl']}")
    if len(settled) == len(opened):
        break

if len(settled) < len(opened):
    failures.append(f"only {len(settled)} of {len(opened)} positions auto-closed within 60s")

# The stake plus result must be back in the wallet, and the ledger must agree.
_, wallet = call("GET", f"/accounts/{account}/wallet")
_, ledger = call("GET", f"/wallet/transactions?account_id={account}&limit=50")

opens = [t for t in ledger["items"] if t["type"] == "trade_open"]
closes = [t for t in ledger["items"] if t["type"] == "trade_close"]
print(f"\nLedger: {len(opens)} opens, {len(closes)} closes")
if len(closes) != len(settled):
    failures.append(f"ledger has {len(closes)} close rows for {len(settled)} settled trades")

# Reconstruct the balance from the ledger and check the cache agrees.
total = sum(float(t["amount"]) for t in ledger["items"] if t["status"] == "completed")
cached = float(wallet["real_balance"]) + float(wallet["bonus_balance"])
print(f"Ledger sum {total:.2f}  vs wallet cache {cached:.2f}")
if abs(total - cached) > 0.01:
    failures.append(f"ledger sum {total:.2f} disagrees with wallet cache {cached:.2f}")
else:
    print("  [PASS] ledger and wallet cache agree")

print("\n" + "=" * 56)
if failures:
    print(f"FAILED: {len(failures)}")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("STOP LOSS / TAKE PROFIT AUTO-CLOSE VERIFIED")
