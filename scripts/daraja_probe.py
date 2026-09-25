#!/usr/bin/env python3
"""Observe what Daraja's STK Push Query actually returns, against sandbox.

A one-off instrument, not part of any suite and not run by CI. It exists to
answer one question that no public source answers consistently: what the query
endpoint returns while a transaction is still being processed, as opposed to
when it has definitively failed or definitively succeeded.

That distinction decides whether reconciliation code marks a paid deposit
failed, so it is established by observation before anything is written against
it.

    export MPESA_CONSUMER_KEY=... MPESA_CONSUMER_SECRET=... MPESA_PASSKEY=...
    python3 scripts/daraja_probe.py

Credentials come from the environment only. Nothing is written to disk, and the
raw bodies printed below are what should be pasted into the findings — read them
before sharing, in case sandbox echoes anything you would rather not.

SAFETY: refuses to run against anything but sandbox. This script sends a real
STK push; against production credentials that is a real charge to a real person.
"""
import base64
import json
import os
import sys
import time
import urllib.error
import urllib.request
from datetime import datetime

BASE = os.environ.get("MPESA_BASE_URL", "https://sandbox.safaricom.co.ke").rstrip("/")
KEY = os.environ.get("MPESA_CONSUMER_KEY", "")
SECRET = os.environ.get("MPESA_CONSUMER_SECRET", "")
SHORTCODE = os.environ.get("MPESA_SHORTCODE", "174379")
PASSKEY = os.environ.get("MPESA_PASSKEY", "")
# Safaricom's sandbox test number. It auto-completes; see the note in §NOTES.
MSISDN = os.environ.get("PROBE_MSISDN", "254708374149")
CALLBACK = os.environ.get("MPESA_CALLBACK_URL", "https://example.com/unused")
AMOUNT = int(os.environ.get("PROBE_AMOUNT", "1"))

if "sandbox.safaricom.co.ke" not in BASE:
    sys.exit(
        f"refusing to run against {BASE!r}.\n"
        "This sends a real STK push. Sandbox only — set MPESA_BASE_URL to "
        "https://sandbox.safaricom.co.ke, or run the production case by hand."
    )
if not (KEY and SECRET and PASSKEY):
    sys.exit("set MPESA_CONSUMER_KEY, MPESA_CONSUMER_SECRET and MPESA_PASSKEY in the environment")


def post(path, token, payload):
    """Returns (status, raw_body). Raw, because the shape is the thing under test."""
    req = urllib.request.Request(f"{BASE}{path}", data=json.dumps(payload).encode(), method="POST")
    req.add_header("Authorization", f"Bearer {token}")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=40) as r:
            return r.status, r.read().decode()
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()
    except Exception as e:
        return 0, f"<transport error: {e}>"


def token():
    req = urllib.request.Request(f"{BASE}/oauth/v1/generate?grant_type=client_credentials")
    req.add_header(
        "Authorization",
        "Basic " + base64.b64encode(f"{KEY}:{SECRET}".encode()).decode(),
    )
    with urllib.request.urlopen(req, timeout=40) as r:
        return json.loads(r.read())["access_token"]


def credentials():
    ts = datetime.now().strftime("%Y%m%d%H%M%S")
    return ts, base64.b64encode(f"{SHORTCODE}{PASSKEY}{ts}".encode()).decode()


def show(label, status, body):
    print(f"\n--- {label} ---")
    print(f"HTTP {status}")
    try:
        print(json.dumps(json.loads(body), indent=2, sort_keys=True))
    except Exception:
        print(body)


tok = token()
ts, password = credentials()

status, body = post("/mpesa/stkpush/v1/processrequest", tok, {
    "BusinessShortCode": SHORTCODE,
    "Password": password,
    "Timestamp": ts,
    "TransactionType": "CustomerPayBillOnline",
    "Amount": AMOUNT,
    "PartyA": MSISDN,
    "PartyB": SHORTCODE,
    "PhoneNumber": MSISDN,
    "CallBackURL": CALLBACK,
    "AccountReference": "probe",
    "TransactionDesc": "query probe",
})
show("STK PUSH", status, body)

checkout = ""
try:
    checkout = json.loads(body).get("CheckoutRequestID", "")
except Exception:
    pass
if not checkout:
    sys.exit("\nno CheckoutRequestID — cannot probe the query endpoint")

print(f"\nCheckoutRequestID: {checkout}")
print("Querying at 0s, 5s, 15s, 30s, 60s, 120s. Each raw body below is evidence.")

started = time.time()
for at in (0, 5, 15, 30, 60, 120):
    wait = at - (time.time() - started)
    if wait > 0:
        time.sleep(wait)
    ts, password = credentials()
    status, body = post("/mpesa/stkpushquery/v1/query", tok, {
        "BusinessShortCode": SHORTCODE,
        "Password": password,
        "Timestamp": ts,
        "CheckoutRequestID": checkout,
    })
    show(f"QUERY at ~{at}s", status, body)

print("""
NOTES for whoever runs this
---------------------------
1. The sandbox test MSISDN auto-completes with no PIN prompt, so this run shows
   the succeeded path and whatever precedes it. To observe a cancellation or a
   PIN timeout, run it again with PROBE_MSISDN set to a real Safaricom number
   you control that is registered on the sandbox app, and cancel the prompt.
2. What matters most is the FIRST query, before the transaction resolves. If it
   returns an error envelope rather than a ResultCode, record errorCode and
   errorMessage verbatim — that is the code reconciliation must treat as "ask
   again" rather than as a failure.
3. Note whether repeated queries are ever refused or throttled. Six in two
   minutes is deliberately gentle; if even that is throttled, the reconciliation
   interval has to respect it.
""")
