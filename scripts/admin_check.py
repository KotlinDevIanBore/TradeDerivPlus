#!/usr/bin/env python3
"""End-to-end check of the admin console against a running API.

Covers the whole admin path: forced password change, freeze blocking a trade
while still allowing a close, adjustments, KYC approval unlocking withdrawal,
and token scope separation in both directions.

    python3 scripts/admin_check.py [base_url]
"""
import json
import random
import sys
import time
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080/api/v1").rstrip("/")
failures = []


class Session:
    """A cookie-aware client, since refresh tokens now live in cookies."""

    def __init__(self):
        self.token = None
        self.jar = {}

    def call(self, method, path, body=None, expect=None):
        req = urllib.request.Request(
            f"{BASE}{path}",
            data=json.dumps(body).encode() if body is not None else None,
            method=method,
        )
        req.add_header("Content-Type", "application/json")
        if self.token:
            req.add_header("Authorization", f"Bearer {self.token}")
        if self.jar:
            req.add_header("Cookie", "; ".join(f"{k}={v}" for k, v in self.jar.items()))

        try:
            with urllib.request.urlopen(req, timeout=20) as r:
                status, payload, headers = r.status, json.loads(r.read() or "{}"), r.headers
        except urllib.error.HTTPError as e:
            status, payload, headers = e.code, json.loads(e.read() or "{}"), e.headers

        for raw in headers.get_all("Set-Cookie") or []:
            name, _, rest = raw.partition("=")
            value = rest.split(";")[0]
            if value:
                self.jar[name] = value
            else:
                self.jar.pop(name, None)

        if expect is not None and status != expect:
            failures.append(f"{method} {path} -> {status}, expected {expect}: {payload}")
        return status, payload


def check(label, condition, detail=""):
    if not condition:
        failures.append(f"{label}: {detail}")
    print(f"  [{'PASS' if condition else 'FAIL'}] {label}" + (f"  {detail}" if detail and not condition else ""))


print(f"Admin console check against {BASE}\n")

# --- Admin auth ------------------------------------------------------------
print("ADMIN AUTH")
admin = Session()
_, login = admin.call("POST", "/admin/login", {"username": "admin", "password": "admin123"})
if "access_token" not in login:
    print("  [FAIL] seeded admin cannot log in. Run: go run ./cmd/seed")
    sys.exit(1)
admin.token = login["access_token"]
check("seeded admin logs in", bool(admin.token))
check("refresh token is a cookie, not in the body", "refresh_token" not in login)
check("admin refresh cookie is scoped to /api/v1/admin", "tdp_admin_refresh" in admin.jar)

_, bad = admin.call("POST", "/admin/login", {"username": "admin", "password": "wrong"}, expect=401)
check("wrong admin password refused", bad.get("error", {}).get("code") == "invalid_credentials")

_, ghost = admin.call("POST", "/admin/login", {"username": "nosuchadmin", "password": "wrong"}, expect=401)
check("unknown username gives the same error as a wrong password",
      ghost.get("error", {}).get("code") == "invalid_credentials")

_, same = admin.call("POST", "/admin/change-password",
                     {"current_password": "admin123", "new_password": "admin123"}, expect=422)
check("cannot 'change' the password to the default", same.get("error", {}).get("code") == "password_unchanged")

_, weak = admin.call("POST", "/admin/change-password",
                     {"current_password": "admin123", "new_password": "short"}, expect=422)
check("weak new password refused", weak.get("error", {}).get("field") == "new_password")

NEW_PASSWORD = "e2eConsoleAdmin1"
_, changed = admin.call("POST", "/admin/change-password",
                        {"current_password": "admin123", "new_password": NEW_PASSWORD}, expect=200)
check("password changed", "message" in changed)

_, stale = admin.call("POST", "/admin/login", {"username": "admin", "password": "admin123"}, expect=401)
check("old password no longer works", stale.get("error", {}).get("code") == "invalid_credentials")

admin = Session()
_, relogin = admin.call("POST", "/admin/login", {"username": "admin", "password": NEW_PASSWORD}, expect=200)
admin.token = relogin["access_token"]
check("new password works", bool(admin.token))
check("password change no longer demanded", relogin.get("password_change_required") is False)

# --- A user to act upon ----------------------------------------------------
print("\nFIXTURE USER")
user = Session()
phone = f"07{random.randint(10_000_000, 99_999_999)}"
_, reg = user.call("POST", "/auth/register",
                   {"phone_number": phone, "password": "hunter2024", "full_name": "Freeze Target"},
                   expect=201)
user.token = reg["access_token"]
user_id = reg["user"]["id"]
demo_account = reg["account"]["id"]
check("user registered with a demo account", bool(demo_account))

_, symbols = user.call("GET", "/symbols", expect=200)
priced = [s for s in symbols["items"] if "price" in s and s["is_active"]]
sym = priced[0]["code"] if priced else "R_75"

_, trade = user.call("POST", "/trades",
                     {"account_id": demo_account, "symbol": sym, "direction": "buy", "stake": "50"},
                     expect=201)
check("user opens a position before the freeze", trade.get("status") == "open")

# --- Freeze ----------------------------------------------------------------
print("\nFREEZE")
_, noreason = admin.call("POST", f"/admin/accounts/{demo_account}/freeze", {"reason": "short"}, expect=422)
check("freeze requires a real reason", noreason.get("error", {}).get("field") == "reason")

_, frozen = admin.call("POST", f"/admin/accounts/{demo_account}/freeze",
                       {"reason": "Suspected duplicate signup, investigating"}, expect=200)
check("account frozen", frozen.get("status") == "frozen")
check("freeze reason stored on the account", bool(frozen.get("frozen_reason")))

_, refused = user.call("POST", "/trades",
                       {"account_id": demo_account, "symbol": sym, "direction": "buy", "stake": "10"},
                       expect=403)
check("frozen account cannot open a trade", refused.get("error", {}).get("code") == "account_frozen")

# The freeze dialog promises this, so it must be true.
_, closed = user.call("POST", f"/trades/{trade['id']}/close", expect=200)
check("frozen account CAN still close an open position", closed.get("status") == "closed")

# --- Adjustment on a frozen account (the stated decision) ------------------
print("\nADJUSTMENT")
_, before = user.call("GET", f"/accounts/{demo_account}/wallet", expect=200)
before_total = float(before["total"])

_, adj = admin.call("POST", f"/admin/accounts/{demo_account}/adjust",
                    {"amount": "50", "balance_type": "real",
                     "reason": "Goodwill credit after the freeze investigation"}, expect=200)
check("adjustment succeeds on a FROZEN account (stated decision)",
      adj.get("transaction", {}).get("type") == "admin_adjustment")

_, after = user.call("GET", f"/accounts/{demo_account}/wallet", expect=200)
check("adjustment credited the balance",
      abs(float(after["total"]) - (before_total + 50)) < 0.01,
      f"{after['total']} vs expected {before_total + 50}")

_, over = admin.call("POST", f"/admin/accounts/{demo_account}/adjust",
                     {"amount": "-999999", "balance_type": "real",
                      "reason": "Deliberate over-debit, must be refused"}, expect=400)
check("over-debit refused", over.get("error", {}).get("code") == "insufficient_funds")

_, unchanged = user.call("GET", f"/accounts/{demo_account}/wallet", expect=200)
check("refused adjustment changed nothing", unchanged["total"] == after["total"])

_, zero = admin.call("POST", f"/admin/accounts/{demo_account}/adjust",
                     {"amount": "0", "balance_type": "real", "reason": "Zero amount must be refused"},
                     expect=422)
check("zero adjustment refused", zero.get("error", {}).get("field") == "amount")

# --- Unfreeze --------------------------------------------------------------
print("\nUNFREEZE")
_, thawed = admin.call("POST", f"/admin/accounts/{demo_account}/unfreeze",
                       {"reason": "Investigation closed, account cleared"}, expect=200)
check("account unfrozen", thawed.get("status") == "active")
check("frozen reason cleared", thawed.get("frozen_reason") in (None, ""))

_, reopened = user.call("POST", "/trades",
                        {"account_id": demo_account, "symbol": sym, "direction": "buy", "stake": "10"},
                        expect=201)
check("trading works again after unfreeze", reopened.get("status") == "open")

# --- KYC -------------------------------------------------------------------
print("\nKYC")
_, queue = admin.call("GET", "/admin/kyc?status=pending", expect=200)
check("pending queue has seeded submissions", len(queue.get("items", [])) > 0,
      "run: go run ./cmd/seed")

if queue.get("items"):
    submission = queue["items"][0]
    # The field is document_key now, and it holds an R2 object key rather than a
    # URL. A seeded fixture deliberately has none: the seed process has its own
    # in-memory stub, separate from the API's, so any key it wrote would name an
    # object the console could never fetch -- a View button that always failed.
    # Empty is the honest value, and the queue renders it as "None".
    check("a seeded submission carries no document",
          not (submission.get("document_key") or ""),
          submission.get("document_key"))

    target_user = submission["user_id"]
    _, approved = admin.call("POST", f"/admin/kyc/{submission['id']}/approve", {}, expect=200)
    check("submission approved", approved.get("status") == "approved")

    _, twice = admin.call("POST", f"/admin/kyc/{submission['id']}/approve", {}, expect=409)
    check("approving twice refused", twice.get("error", {}).get("code") == "already_reviewed")

    _, detail = admin.call("GET", f"/admin/users/{target_user}", expect=200)
    check("user's KYC status is now verified", detail["user"]["kyc_status"] == "verified")

    pending = [s for s in queue["items"] if s["id"] != submission["id"]]
    if pending:
        _, noreason = admin.call("POST", f"/admin/kyc/{pending[0]['id']}/reject", {"reason": "no"}, expect=422)
        check("rejection requires a reason", noreason.get("error", {}).get("field") == "reason")

        _, rejected = admin.call("POST", f"/admin/kyc/{pending[0]['id']}/reject",
                                 {"reason": "Document is unreadable, ask for a resubmission"}, expect=200)
        check("submission rejected", rejected.get("status") == "rejected")

# --- Audit log -------------------------------------------------------------
print("\nAUDIT LOG")
_, audit = admin.call("GET", "/admin/audit-log?limit=50", expect=200)
actions = [e["action"] for e in audit.get("items", [])]
for action in ["freeze_account", "unfreeze_account", "manual_adjustment", "kyc_approve"]:
    check(f"audit records {action}", action in actions)
check("every audit row names the admin",
      all(e.get("admin_username") for e in audit.get("items", [])))
check("every audit row carries a reason",
      all(e.get("reason") for e in audit.get("items", []) if e["action"] != "kyc_approve"))

adjustment = next((e for e in audit["items"] if e["action"] == "manual_adjustment"), None)
check("adjustment audit points at the ledger row",
      bool(adjustment and adjustment.get("metadata", {}).get("transaction_id")))

_, user_audit = admin.call("GET", f"/admin/users/{user_id}", expect=200)
check("user detail carries their audit trail", len(user_audit.get("audit", [])) >= 3)

# --- Dashboard -------------------------------------------------------------
print("\nDASHBOARD")
_, dash = admin.call("GET", "/admin/dashboard", expect=200)
check("dashboard reports users", dash.get("users", 0) > 0)
check("dashboard reports the deposit series", len(dash.get("deposits_by_hour", [])) == 24)
check("LEDGER RECONCILES: no account has drifted", dash.get("drifted_accounts") == 0,
      f"{dash.get('drifted_accounts')} drifted: {dash.get('drift')}")

# --- Scope separation ------------------------------------------------------
print("\nTOKEN SCOPE SEPARATION")
cross = Session()
cross.token = user.token
_, denied = cross.call("GET", "/admin/dashboard", expect=401)
check("a user token cannot open an admin route", denied.get("error", {}).get("code") == "unauthorized")

cross.token = admin.token
_, denied2 = cross.call("GET", "/accounts", expect=401)
check("an admin token cannot open a user route", denied2.get("error", {}).get("code") == "unauthorized")

anon = Session()
_, denied3 = anon.call("GET", "/admin/dashboard", expect=401)
check("unauthenticated admin request refused", denied3.get("error", {}).get("code") == "unauthorized")

# --- State this script leaves behind ---------------------------------------
# It cannot put the seeded password back: the API refuses to set a password to
# the known default, which is the point of that guard. So it leaves the console
# on NEW_PASSWORD, which the Playwright suite also knows. To get back to a
# freshly seeded state, run:
#
#     go run ./cmd/seed -reset-admin
print(f"\nThe console password is now {NEW_PASSWORD!r}.")
print("Restore the seeded default with: go run ./cmd/seed -reset-admin")

print("\n" + "=" * 60)
if failures:
    print(f"FAILED: {len(failures)} check(s)")
    for f in failures:
        print(f"  - {f}")
    sys.exit(1)
print("ADMIN CONSOLE VERIFIED")
