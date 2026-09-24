package mpesaclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The query's contract is its classification, so that is what these tests are
// about: every branch of it, against a stub that says exactly what it is told
// to. Nothing here touches Daraja.

// stub is a fake Daraja. It hands out one token, records how many times it was
// asked for one, and answers the query endpoint with whatever the test scripts.
type stub struct {
	srv       *httptest.Server
	authHits  atomic.Int32
	queryHits atomic.Int32
	status    atomic.Int32
	body      atomic.Value // string
	lastReq   atomic.Value // map[string]any
	lastAuth  atomic.Value // string
}

func newStub(t *testing.T) *stub {
	t.Helper()
	s := &stub{}
	s.status.Store(200)
	s.body.Store(`{"ResultCode":0,"ResultDesc":"ok"}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		s.authHits.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"tok-1","expires_in":"3599"}`)
	})
	mux.HandleFunc("/mpesa/stkpush/v1/processrequest", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"ResponseCode":"0","CheckoutRequestID":"ws_CO_1","MerchantRequestID":"m1"}`)
	})
	mux.HandleFunc("/mpesa/stkpushquery/v1/query", func(w http.ResponseWriter, r *http.Request) {
		s.queryHits.Add(1)
		s.lastAuth.Store(r.Header.Get("Authorization"))
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.lastReq.Store(req)
		w.WriteHeader(int(s.status.Load()))
		_, _ = io.WriteString(w, s.body.Load().(string))
	})
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *stub) answer(status int, body string) {
	s.status.Store(int32(status))
	s.body.Store(body)
}

func (s *stub) client() *Client {
	return New(Options{
		BaseURL:        s.srv.URL,
		ConsumerKey:    "key",
		ConsumerSecret: "secret",
		Shortcode:      "174379",
		Passkey:        "passkey",
		CallbackURL:    "https://example.invalid/callback",
	})
}

func TestSTKQueryClassification(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		want       QueryStatus
		wantCode   int
		wantErr    bool
		wantErrHas string
	}{
		// 3. Succeeded.
		{"ResultCode 0 as a number", 200, `{"ResultCode":0,"ResultDesc":"The service request is processed successfully."}`, QuerySucceeded, 0, false, ""},
		{"ResultCode 0 as a string", 200, `{"ResultCode":"0","ResultDesc":"The service request is processed successfully."}`, QuerySucceeded, 0, false, ""},

		// 4. Failed — every member of the allowlist, one of them as a string.
		{"1 insufficient balance", 200, `{"ResultCode":1,"ResultDesc":"The balance is insufficient for the transaction"}`, QueryFailed, 1, false, ""},
		{"1001 subscriber locked", 200, `{"ResultCode":1001,"ResultDesc":"Unable to lock subscriber"}`, QueryFailed, 1001, false, ""},
		{"1032 cancelled", 200, `{"ResultCode":1032,"ResultDesc":"Request cancelled by user"}`, QueryFailed, 1032, false, ""},
		{"1032 cancelled as a string", 200, `{"ResultCode":"1032","ResultDesc":"Request cancelled by user"}`, QueryFailed, 1032, false, ""},
		{"1037 DS timeout", 200, `{"ResultCode":1037,"ResultDesc":"DS timeout user cannot be reached"}`, QueryFailed, 1037, false, ""},
		{"2001 wrong PIN", 200, `{"ResultCode":2001,"ResultDesc":"The initiator information is invalid."}`, QueryFailed, 2001, false, ""},

		// 5. A code this package does not recognise is not a failure.
		{"unknown ResultCode", 200, `{"ResultCode":9999,"ResultDesc":"Something new"}`, QueryUnresolved, 9999, false, ""},

		// 2. The disputed envelope, both flavours: the one the sources argue
		// about, and an arbitrary one. Both are answers, so nil error; neither is
		// terminal, so Unresolved. The HTTP status wrapped around them does not
		// matter.
		{"envelope 500.001.1001 at HTTP 500", 500, `{"requestId":"r1","errorCode":"500.001.1001","errorMessage":"The transaction is being processed"}`, QueryUnresolved, 0, false, ""},
		{"envelope with another code at HTTP 400", 400, `{"requestId":"r2","errorCode":"400.002.02","errorMessage":"Bad Request - Invalid CheckoutRequestID"}`, QueryUnresolved, 0, false, ""},
		{"envelope at HTTP 200", 200, `{"errorCode":"404.001.03","errorMessage":"Invalid Access Token"}`, QueryUnresolved, 0, false, ""},

		// 1. Daraja said nothing this package can read.
		{"HTTP 500, not JSON", 500, `Internal Server Error`, QueryUnresolved, 0, true, "undecodable"},
		{"HTTP 502, JSON with neither field", 502, `{}`, QueryUnresolved, 0, true, "no ResultCode or errorCode"},
		{"HTTP 200, not JSON", 200, `not json at all`, QueryUnresolved, 0, true, "undecodable"},
		{"HTTP 200, empty body", 200, ``, QueryUnresolved, 0, true, "undecodable"},
		{"ResultCode that is not a number", 200, `{"ResultCode":"zero"}`, QueryUnresolved, 0, true, "not a number"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := newStub(t)
			s.answer(c.status, c.body)

			out, err := s.client().STKQuery(context.Background(), "ws_CO_1")

			if c.wantErr {
				if err == nil {
					t.Fatalf("want an error, got %+v", out)
				}
				if !strings.Contains(err.Error(), c.wantErrHas) {
					t.Fatalf("error %q does not mention %q", err, c.wantErrHas)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Status != c.want {
				t.Fatalf("status %s, want %s (body %s)", out.Status, c.want, c.body)
			}
			if out.ResultCode != c.wantCode {
				t.Fatalf("ResultCode %d, want %d", out.ResultCode, c.wantCode)
			}
		})
	}
}

// The allowlist is the safety property. If a code is not on it, it must not
// classify as failed — there is no test for "a code that should be terminal but
// is missing", because that is exactly the case the design refuses to guess at.
func TestTerminalFailureCodesAreExactlyTheAllowlist(t *testing.T) {
	want := map[int]bool{1: true, 1001: true, 1032: true, 1037: true, 2001: true}
	if len(TerminalFailureCodes) != len(want) {
		t.Fatalf("allowlist has %d codes, want %d: %v", len(TerminalFailureCodes), len(want), TerminalFailureCodes)
	}
	for code := range want {
		if !TerminalFailureCodes[code] {
			t.Fatalf("%d is missing from the allowlist", code)
		}
	}
	if TerminalFailureCodes[0] {
		t.Fatal("0 is success, not a terminal failure")
	}
}

// What goes over the wire: the same four things the push derives, under the
// same token. Verified against Safaricom's own SDK in stk-query-step2-findings.md §1.
func TestSTKQuerySendsWhatThePushDerives(t *testing.T) {
	s := newStub(t)
	c := s.client()

	if _, err := c.STKQuery(context.Background(), "ws_CO_abc123"); err != nil {
		t.Fatal(err)
	}

	if got := s.lastAuth.Load().(string); got != "Bearer tok-1" {
		t.Fatalf("Authorization %q, want the bearer token the stub issued", got)
	}
	req := s.lastReq.Load().(map[string]any)
	for _, k := range []string{"BusinessShortCode", "Password", "Timestamp", "CheckoutRequestID"} {
		if _, ok := req[k]; !ok {
			t.Fatalf("request is missing %s: %v", k, req)
		}
	}
	if len(req) != 4 {
		t.Fatalf("request carries %d fields, want exactly the four Daraja's SDK sends: %v", len(req), req)
	}
	if req["BusinessShortCode"] != "174379" || req["CheckoutRequestID"] != "ws_CO_abc123" {
		t.Fatalf("shortcode/checkout wrong: %v", req)
	}
	ts := req["Timestamp"].(string)
	if len(ts) != 14 {
		t.Fatalf("Timestamp %q is not yyyyMMddHHmmss", ts)
	}
	raw, err := base64.StdEncoding.DecodeString(req["Password"].(string))
	if err != nil {
		t.Fatalf("Password is not base64: %v", err)
	}
	if string(raw) != "174379"+"passkey"+ts {
		t.Fatalf("Password decodes to %q, want shortcode+passkey+timestamp", raw)
	}
}

// One token serves a push and then a query: the cache the push already had is
// the one the query uses.
func TestSTKQueryReusesThePushToken(t *testing.T) {
	s := newStub(t)
	c := s.client()
	ctx := context.Background()

	if _, err := c.STKPush(ctx, STKPushRequest{PhoneNumber: "254712345678", Amount: 10, AccountRef: "x", Description: "y"}); err != nil {
		t.Fatalf("push: %v", err)
	}
	if _, err := c.STKQuery(ctx, "ws_CO_1"); err != nil {
		t.Fatalf("query: %v", err)
	}
	if _, err := c.STKQuery(ctx, "ws_CO_1"); err != nil {
		t.Fatalf("second query: %v", err)
	}
	if n := s.authHits.Load(); n != 1 {
		t.Fatalf("authenticated %d times across a push and two queries, want 1", n)
	}
}

// A 401 means the token stopped being good before its cached expiry. It is an
// error to the caller — Daraja said nothing about the deposit — and it drops the
// cache, so the next call re-authenticates rather than repeating the 401 for
// the rest of the hour.
func TestSTKQueryDropsTheTokenOn401(t *testing.T) {
	s := newStub(t)
	c := s.client()
	ctx := context.Background()

	s.answer(401, `{}`)
	if _, err := c.STKQuery(ctx, "ws_CO_1"); err == nil {
		t.Fatal("a 401 with no envelope should be an error")
	}
	if n := s.authHits.Load(); n != 1 {
		t.Fatalf("auth hits %d after the first call, want 1", n)
	}

	s.answer(200, `{"ResultCode":0}`)
	out, err := c.STKQuery(ctx, "ws_CO_1")
	if err != nil {
		t.Fatalf("query after the 401: %v", err)
	}
	if out.Status != QuerySucceeded {
		t.Fatalf("status %s after re-auth, want succeeded", out.Status)
	}
	if n := s.authHits.Load(); n != 2 {
		t.Fatalf("auth hits %d after a 401, want 2 — the cached token was not dropped", n)
	}
}

// Transport failure is an error, never an outcome. The token is warmed first,
// so what dies is the query itself — the reconciler's case, where a cached
// token outlives the network — rather than the auth call in front of it.
func TestSTKQueryTransportFailureIsAnError(t *testing.T) {
	s := newStub(t)
	c := s.client()
	if _, err := c.STKQuery(context.Background(), "ws_CO_1"); err != nil {
		t.Fatalf("warm-up: %v", err)
	}
	s.srv.Close()

	out, err := c.STKQuery(context.Background(), "ws_CO_1")
	if err == nil {
		t.Fatalf("a closed server should be an error, got %+v", out)
	}
	if !strings.Contains(err.Error(), "stk query:") {
		t.Fatalf("transport failure is not named as one: %v", err)
	}
}
