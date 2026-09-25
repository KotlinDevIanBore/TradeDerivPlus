package mpesaclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// The payout side's contract is its classification, and the two places it can
// go wrong are the ones a neighbouring production system gets wrong: collapsing
// the queue-timeout channel into the result channel, and reading a result code
// that is not a number as zero. Both have their own tests below.

type b2cStub struct {
	srv       *httptest.Server
	authHits  atomic.Int32
	status    atomic.Int32
	body      atomic.Value // string
	lastPath  atomic.Value // string
	lastReq   atomic.Value // map[string]any
	lastAuth  atomic.Value // string
	callCount atomic.Int32
}

func newB2CStub(t *testing.T) *b2cStub {
	t.Helper()
	s := &b2cStub{}
	s.status.Store(200)
	s.body.Store(`{"ConversationID":"AG_1","OriginatorConversationID":"ours-1","ResponseCode":"0","ResponseDescription":"Accept the service request successfully."}`)

	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/v1/generate", func(w http.ResponseWriter, r *http.Request) {
		s.authHits.Add(1)
		_, _ = io.WriteString(w, `{"access_token":"tok-1","expires_in":"3599"}`)
	})
	record := func(w http.ResponseWriter, r *http.Request) {
		s.callCount.Add(1)
		s.lastPath.Store(r.URL.Path)
		s.lastAuth.Store(r.Header.Get("Authorization"))
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		s.lastReq.Store(req)
		w.WriteHeader(int(s.status.Load()))
		_, _ = io.WriteString(w, s.body.Load().(string))
	}
	mux.HandleFunc("/mpesa/b2c/v3/paymentrequest", record)
	mux.HandleFunc("/mpesa/transactionstatus/v1/query", record)
	mux.HandleFunc("/mpesa/accountbalance/v1/query", record)
	// Deliberately NOT registered: /mpesa/b2c/v1/paymentrequest. A v1 request
	// would 404 here, which is how this suite proves there is no v1 path.
	s.srv = httptest.NewServer(mux)
	t.Cleanup(s.srv.Close)
	return s
}

func (s *b2cStub) answer(status int, body string) {
	s.status.Store(int32(status))
	s.body.Store(body)
}

func (s *b2cStub) client() *Client {
	return New(Options{
		BaseURL:            s.srv.URL,
		ConsumerKey:        "key",
		ConsumerSecret:     "secret",
		Shortcode:          "174379",
		Passkey:            "passkey",
		CallbackURL:        "https://example.invalid/mpesa/callback",
		InitiatorName:      "testapi",
		SecurityCredential: "already-encrypted-credential",
		B2CResultURL:       "https://example.invalid/mpesa/b2c/result",
		B2CTimeoutURL:      "https://example.invalid/mpesa/b2c/timeout",
		B2CStatusResultURL: "https://example.invalid/mpesa/b2c/status",
	})
}

func (s *b2cStub) req(t *testing.T) map[string]any {
	t.Helper()
	v := s.lastReq.Load()
	if v == nil {
		t.Fatal("nothing was sent")
	}
	return v.(map[string]any)
}

// --- Sending ----------------------------------------------------------------

func TestSendB2CSendsV3WithOurOwnRequestID(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()

	acc, err := c.SendB2C(context.Background(), B2CRequest{
		OriginatorConversationID: "wd-abc-123",
		PhoneNumber:              "0712345678",
		Amount:                   1500,
		Remarks:                  "TradeDerivPlus withdrawal",
	})
	if err != nil {
		t.Fatal(err)
	}
	if acc.ConversationID != "AG_1" {
		t.Fatalf("ConversationID %q", acc.ConversationID)
	}

	if got := s.lastPath.Load().(string); got != "/mpesa/b2c/v3/paymentrequest" {
		t.Fatalf("posted to %q, want the v3 endpoint — there is deliberately no v1 path", got)
	}
	if got := s.lastAuth.Load().(string); got != "Bearer tok-1" {
		t.Fatalf("Authorization %q", got)
	}

	req := s.req(t)
	want := map[string]any{
		"OriginatorConversationID": "wd-abc-123",
		"InitiatorName":            "testapi",
		"SecurityCredential":       "already-encrypted-credential",
		"CommandID":                "BusinessPayment",
		"PartyA":                   "174379",
		"PartyB":                   "254712345678", // normalised from 07…
		"Remarks":                  "TradeDerivPlus withdrawal",
		"QueueTimeOutURL":          "https://example.invalid/mpesa/b2c/timeout",
		"ResultURL":                "https://example.invalid/mpesa/b2c/result",
	}
	for k, v := range want {
		if req[k] != v {
			t.Errorf("%s = %v, want %v", k, req[k], v)
		}
	}
	if req["Amount"] != float64(1500) {
		t.Errorf("Amount = %v, want 1500", req["Amount"])
	}
	// The two URLs must go out as two different values; one handler for both is
	// the failure this package refuses to be configured into.
	if req["ResultURL"] == req["QueueTimeOutURL"] {
		t.Fatal("ResultURL and QueueTimeOutURL went out identical")
	}
	if _, has := req["Occasion"]; has {
		t.Errorf("an empty Occasion was sent anyway: %v", req["Occasion"])
	}
}

func TestSendB2CRefusesWhatItCannotSafelySend(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()
	ctx := context.Background()

	cases := []struct {
		name string
		req  B2CRequest
		want string
	}{
		{"no originator id", B2CRequest{PhoneNumber: "0712345678", Amount: 100}, "OriginatorConversationID"},
		{"blank originator id", B2CRequest{OriginatorConversationID: "  ", PhoneNumber: "0712345678", Amount: 100}, "OriginatorConversationID"},
		{"zero amount", B2CRequest{OriginatorConversationID: "x", PhoneNumber: "0712345678", Amount: 0}, "positive whole number"},
		{"negative amount", B2CRequest{OriginatorConversationID: "x", PhoneNumber: "0712345678", Amount: -5}, "positive whole number"},
		{"not a Safaricom number", B2CRequest{OriginatorConversationID: "x", PhoneNumber: "12345", Amount: 100}, "Safaricom-shaped"},
	}
	for _, c2 := range cases {
		t.Run(c2.name, func(t *testing.T) {
			before := s.callCount.Load()
			if _, err := c.SendB2C(ctx, c2.req); err == nil {
				t.Fatal("accepted")
			} else if !strings.Contains(err.Error(), c2.want) {
				t.Fatalf("error %q does not mention %q", err, c2.want)
			}
			if s.callCount.Load() != before {
				t.Fatal("a refused request still reached Daraja")
			}
		})
	}
}

// A rejection and a transport failure are not the same thing, and the difference
// decides whether the caller may refund. A rejection carries no ConversationID,
// so nothing is queued that could pay; a transport failure means the request may
// have arrived.
func TestSendB2CRejectionIsDistinctFromNotKnowing(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()
	ctx := context.Background()
	ok := B2CRequest{OriginatorConversationID: "x", PhoneNumber: "0712345678", Amount: 100}

	rejections := []struct {
		name, body string
		status     int
		wantCode   string
	}{
		{"error envelope", `{"requestId":"r","errorCode":"400.002.02","errorMessage":"Bad Request - Invalid Amount"}`, 400, "400.002.02"},
		{"envelope at HTTP 200", `{"errorCode":"500.001.1001","errorMessage":"Initiator information is invalid"}`, 200, "500.001.1001"},
		{"non-zero ResponseCode", `{"ConversationID":"AG_2","ResponseCode":"1","ResponseDescription":"Refused"}`, 200, "1"},
	}
	for _, r := range rejections {
		t.Run(r.name, func(t *testing.T) {
			s.answer(r.status, r.body)
			_, err := c.SendB2C(ctx, ok)
			if !errors.Is(err, ErrPayoutRejected) {
				t.Fatalf("error %v is not a rejection — the caller cannot tell it may refund", err)
			}
			var rej *PayoutRejection
			if !errors.As(err, &rej) || rej.Code != r.wantCode {
				t.Fatalf("rejection %+v, want code %s", rej, r.wantCode)
			}
		})
	}

	t.Run("accepted with no ConversationID is not an acceptance", func(t *testing.T) {
		s.answer(200, `{"ResponseCode":"0","ResponseDescription":"ok"}`)
		_, err := c.SendB2C(ctx, ok)
		if err == nil {
			t.Fatal("accepted a queued payout with nothing to correlate a callback to")
		}
		if errors.Is(err, ErrPayoutRejected) {
			t.Fatal("classified as a rejection; nothing is known, so refunding would be wrong")
		}
	})

	t.Run("transport failure is not a rejection", func(t *testing.T) {
		s2 := newB2CStub(t)
		c2 := s2.client()
		if _, err := c2.SendB2C(ctx, ok); err != nil { // warm the token
			t.Fatal(err)
		}
		s2.srv.Close()
		_, err := c2.SendB2C(ctx, ok)
		if err == nil {
			t.Fatal("a closed server was treated as success")
		}
		if errors.Is(err, ErrPayoutRejected) {
			t.Fatal("a transport failure classified as a rejection — a caller would refund a payout that may have been sent")
		}
		if !strings.Contains(err.Error(), "b2c send:") {
			t.Fatalf("transport failure is not named as one: %v", err)
		}
	})
}

// The configuration check that refuses the collapse.
func TestValidateB2CConfigRefusesOneURLForBothChannels(t *testing.T) {
	base := Options{
		BaseURL: "https://sandbox.invalid", ConsumerKey: "k", ConsumerSecret: "s",
		Shortcode: "174379", InitiatorName: "testapi", SecurityCredential: "cred",
	}

	same := base
	same.B2CResultURL = "https://example.invalid/mpesa/b2c/result"
	same.B2CTimeoutURL = "https://example.invalid/mpesa/b2c/result"
	same.B2CStatusResultURL = "https://example.invalid/mpesa/b2c/status"
	err := New(same).ValidateB2CConfig()
	if err == nil {
		t.Fatal("one URL for both channels was accepted")
	}
	if !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("error %q does not explain why", err)
	}

	// The same rule one step along: a status answer must not land where a payout
	// result lands, or the handler has to guess which document it is holding.
	statusClash := base
	statusClash.B2CResultURL = "https://example.invalid/mpesa/b2c/result"
	statusClash.B2CTimeoutURL = "https://example.invalid/mpesa/b2c/timeout"
	statusClash.B2CStatusResultURL = "https://example.invalid/mpesa/b2c/result"
	if err := New(statusClash).ValidateB2CConfig(); err == nil {
		t.Fatal("the status-result URL was allowed to equal the payout result URL")
	}
	statusClash.B2CStatusResultURL = "https://example.invalid/mpesa/b2c/timeout"
	if err := New(statusClash).ValidateB2CConfig(); err == nil {
		t.Fatal("the status-result URL was allowed to equal the queue-timeout URL")
	}

	// And a send refuses too, not just the boot check.
	if _, err := New(same).SendB2C(context.Background(), B2CRequest{
		OriginatorConversationID: "x", PhoneNumber: "0712345678", Amount: 100,
	}); err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("SendB2C with collapsed URLs returned %v", err)
	}

	for _, missing := range []string{"InitiatorName", "SecurityCredential", "B2CResultURL", "B2CTimeoutURL", "B2CStatusResultURL"} {
		o := base
		o.B2CResultURL = "https://example.invalid/r"
		o.B2CTimeoutURL = "https://example.invalid/t"
		o.B2CStatusResultURL = "https://example.invalid/s"
		switch missing {
		case "InitiatorName":
			o.InitiatorName = ""
		case "SecurityCredential":
			o.SecurityCredential = ""
		case "B2CResultURL":
			o.B2CResultURL = ""
		case "B2CTimeoutURL":
			o.B2CTimeoutURL = ""
		case "B2CStatusResultURL":
			o.B2CStatusResultURL = ""
		}
		if err := New(o).ValidateB2CConfig(); err == nil {
			t.Fatalf("a client with no %s claimed to be configured for payouts", missing)
		}
	}

	good := base
	good.B2CResultURL = "https://example.invalid/r"
	good.B2CTimeoutURL = "https://example.invalid/t"
	good.B2CStatusResultURL = "https://example.invalid/s"
	if err := New(good).ValidateB2CConfig(); err != nil {
		t.Fatalf("a correctly configured client was refused: %v", err)
	}
}

// --- Receiving --------------------------------------------------------------

func payoutBody(code any, extra string) string {
	codeJSON, _ := json.Marshal(code)
	params := `"ResultParameters":{"ResultParameter":[{"Key":"TransactionReceipt","Value":"NA30XKHVCW"}` + extra + `]},`
	return `{"Result":{"ResultType":0,"ResultCode":` + string(codeJSON) +
		`,"ResultDesc":"desc","OriginatorConversationID":"wd-1","ConversationID":"AG_1","TransactionID":"NA30XPKKVCW",` +
		params + `"ReferenceData":{}}}`
}

func TestParsePayoutCallbackClassification(t *testing.T) {
	cases := []struct {
		name     string
		code     any
		want     PayoutStatus
		wantOurs bool
	}{
		{"0 as a number", 0, PayoutSucceeded, false},
		{"0 as a string", "0", PayoutSucceeded, false},
		{"1 insufficient float, ours", 1, PayoutFailed, true},
		{"1 as a string", "1", PayoutFailed, true},
		{"2 below minimum", 2, PayoutFailed, false},
		{"3 above maximum", 3, PayoutFailed, false},
		{"4 daily limit", 4, PayoutFailed, false},
		{"8 recipient balance", 8, PayoutFailed, false},
		{"11 account state, ours", 11, PayoutFailed, true},
		{"21 initiator not authorised, ours", 21, PayoutFailed, true},
		{"2001 bad credentials, ours", 2001, PayoutFailed, true},
		{"2006 account status, ours", 2006, PayoutFailed, true},
		{"2028 shortcode not permitted, ours", 2028, PayoutFailed, true},
		{"2040 recipient unregistered", 2040, PayoutFailed, false},
		{"8006 credential locked, ours", 8006, PayoutFailed, true},
		{"SFC_IC0003, a code that is not a number at all", "SFC_IC0003", PayoutFailed, false},
		{"an unknown numeric code is not a failure", 9999, PayoutUnresolved, false},
		{"an unknown string code is not a failure", "TP40153", PayoutUnresolved, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ParsePayoutCallback([]byte(payoutBody(c.code, "")), ArrivedAtResultURL)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if out.Status != c.want {
				t.Fatalf("status %s, want %s", out.Status, c.want)
			}
			if out.OursToFix != c.wantOurs {
				t.Fatalf("OursToFix %v, want %v", out.OursToFix, c.wantOurs)
			}
			if out.OriginatorConversationID != "wd-1" {
				t.Fatalf("lost the id that matches this to a withdrawal: %q", out.OriginatorConversationID)
			}
		})
	}
}

// The rule with the most money behind it: nothing arriving on the queue-timeout
// channel is terminal, whatever it says. Daraja's queue gave up before it knew.
func TestTimeoutArrivalIsNeverTerminal(t *testing.T) {
	for _, code := range []any{0, "0", 1, 2040, "SFC_IC0003", 9999} {
		body := []byte(payoutBody(code, ""))

		onResult, err := ParsePayoutCallback(body, ArrivedAtResultURL)
		if err != nil {
			t.Fatal(err)
		}
		onTimeout, err := ParsePayoutCallback(body, ArrivedAtTimeoutURL)
		if err != nil {
			t.Fatal(err)
		}

		if onTimeout.Status != PayoutUnresolved {
			t.Fatalf("code %v on the timeout channel classified %s — a timeout means 'we do not know', "+
				"and settling or refunding on it is how a paid customer gets refunded too", code, onTimeout.Status)
		}
		if !onTimeout.ArrivedAtTimeoutURL {
			t.Fatal("the result does not record which channel it came in on")
		}
		// The same body on the result channel does classify, which is what makes
		// the distinction load-bearing rather than cosmetic.
		if code == 0 && onResult.Status != PayoutSucceeded {
			t.Fatalf("0 on the result channel classified %s", onResult.Status)
		}
		if code == 2040 && onResult.Status != PayoutFailed {
			t.Fatalf("2040 on the result channel classified %s", onResult.Status)
		}
	}
}

// The other production bug this package refuses to have: a result code that is
// absent, or of a shape the parser does not expect, must never read as 0.
func TestResultCodeNeverDefaultsToZero(t *testing.T) {
	bad := []struct{ name, body string }{
		{"no ResultCode at all", `{"Result":{"ResultDesc":"d","OriginatorConversationID":"wd-1","ConversationID":"AG_1"}}`},
		{"ResultCode null", `{"Result":{"ResultCode":null,"OriginatorConversationID":"wd-1","ConversationID":"AG_1"}}`},
		{"ResultCode empty string", `{"Result":{"ResultCode":"","OriginatorConversationID":"wd-1","ConversationID":"AG_1"}}`},
		{"no Result object", `{"Body":{"stkCallback":{"ResultCode":0}}}`},
		{"not JSON", `<html>502 Bad Gateway</html>`},
		{"empty body", ``},
	}
	for _, c := range bad {
		t.Run(c.name, func(t *testing.T) {
			out, err := ParsePayoutCallback([]byte(c.body), ArrivedAtResultURL)
			if err == nil {
				t.Fatalf("accepted, as status %s — an unreadable result must never be a success", out.Status)
			}
			if out.Status == PayoutSucceeded {
				t.Fatal("returned Succeeded alongside an error")
			}
		})
	}
}

func TestParsePayoutCallbackExtractsWhatAnOperatorNeeds(t *testing.T) {
	extra := `,{"Key":"ReceiverPartyPublicName","Value":"254712345678 - JOHN DOE"},` +
		`{"Key":"TransactionCompletedDateTime","Value":"03.01.2019 17:48:32"},` +
		`{"Key":"B2CUtilityAccountAvailableFunds","Value":4425.00},` +
		`{"Key":"B2CRecipientIsRegisteredCustomer","Value":"Y"}`
	out, err := ParsePayoutCallback([]byte(payoutBody(0, extra)), ArrivedAtResultURL)
	if err != nil {
		t.Fatal(err)
	}
	if out.Receipt != "NA30XKHVCW" {
		t.Fatalf("receipt %q", out.Receipt)
	}
	if out.RecipientName != "254712345678 - JOHN DOE" {
		t.Fatalf("recipient %q", out.RecipientName)
	}
	if out.CompletedAt != "03.01.2019 17:48:32" {
		t.Fatalf("completed at %q", out.CompletedAt)
	}
	if out.UtilityBalance != "4425" && out.UtilityBalance != "4425.00" {
		t.Fatalf("utility balance %q — the float gauge's only free source", out.UtilityBalance)
	}

	// ResultParameter as a single object rather than an array, which Daraja has
	// been observed to send when there is one entry.
	single := `{"Result":{"ResultCode":0,"OriginatorConversationID":"wd-1","ConversationID":"AG_1",` +
		`"ResultParameters":{"ResultParameter":{"Key":"TransactionReceipt","Value":"SOLO123"}}}}`
	out, err = ParsePayoutCallback([]byte(single), ArrivedAtResultURL)
	if err != nil {
		t.Fatal(err)
	}
	if out.Receipt != "SOLO123" {
		t.Fatalf("receipt from a single-object ResultParameter: %q", out.Receipt)
	}

	// A success with no receipt is still a success, and nothing invents one.
	none := `{"Result":{"ResultCode":0,"OriginatorConversationID":"wd-1","ConversationID":"AG_1"}}`
	out, err = ParsePayoutCallback([]byte(none), ArrivedAtResultURL)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PayoutSucceeded || out.Receipt != "" {
		t.Fatalf("status %s receipt %q, want succeeded with no receipt", out.Status, out.Receipt)
	}
}

func TestPayoutFailureCodesAreTheDocumentedSet(t *testing.T) {
	want := []string{"1", "2", "3", "4", "8", "11", "21", "2001", "2006", "2028", "2040", "8006", "SFC_IC0003"}
	if len(PayoutFailureCodes) != len(want) {
		t.Fatalf("allowlist has %d entries, want %d: %v", len(PayoutFailureCodes), len(want), PayoutFailureCodes)
	}
	for _, code := range want {
		if PayoutFailureCodes[code] == "" {
			t.Errorf("%s is missing, or has no description", code)
		}
	}
	if PayoutFailureCodes["0"] != "" {
		t.Fatal("0 is success, not a terminal failure")
	}
	// Every "ours" code must itself be a failure code; a code that is ours to fix
	// but not terminal would alert without refunding.
	for code := range PayoutOursCodes {
		if PayoutFailureCodes[code] == "" {
			t.Errorf("%s is marked ours to fix but is not a failure code", code)
		}
	}
}

// --- Transaction Status -----------------------------------------------------

func TestQueryTransactionStatusAsksByTheIDWeChose(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()

	if _, err := c.QueryTransactionStatus(context.Background(), StatusRequest{
		OriginatorConversationID: "wd-abc-123",
	}); err != nil {
		t.Fatal(err)
	}
	if got := s.lastPath.Load().(string); got != "/mpesa/transactionstatus/v1/query" {
		t.Fatalf("posted to %q", got)
	}
	req := s.req(t)
	if req["OriginalConversationID"] != "wd-abc-123" {
		t.Fatalf("did not ask by our own id: %v", req)
	}
	if req["CommandID"] != "TransactionStatusQuery" || req["IdentifierType"] != "4" || req["PartyA"] != "174379" {
		t.Fatalf("wrong command shape: %v", req)
	}
	if req["Initiator"] != "testapi" || req["SecurityCredential"] != "already-encrypted-credential" {
		t.Fatalf("status query does not authenticate as the API user: %v", req)
	}
	if req["ResultURL"] == req["QueueTimeOutURL"] {
		t.Fatal("the status query collapsed its two URLs")
	}
	// A status answer must come back on its own channel, not where payout results
	// land: the two are different documents.
	if req["ResultURL"] != "https://example.invalid/mpesa/b2c/status" {
		t.Fatalf("the status query asks for its answer at %v, want the status channel", req["ResultURL"])
	}
	if _, has := req["TransactionID"]; has {
		t.Fatal("sent an empty TransactionID alongside the conversation id")
	}

	// A receipt works too, for a payout that did return one.
	if _, err := c.QueryTransactionStatus(context.Background(), StatusRequest{TransactionID: "NA30XKHVCW"}); err != nil {
		t.Fatal(err)
	}
	if s.req(t)["TransactionID"] != "NA30XKHVCW" {
		t.Fatalf("asking by receipt: %v", s.req(t))
	}

	// Neither is an error: there would be nothing to ask about.
	if _, err := c.QueryTransactionStatus(context.Background(), StatusRequest{}); err == nil {
		t.Fatal("a status query with no id was accepted")
	}
}

func TestParseStatusCallbackSeparatesQueryFailureFromPayoutFailure(t *testing.T) {
	body := func(code any, txStatus string) string {
		codeJSON, _ := json.Marshal(code)
		params := ""
		if txStatus != "" {
			params = `"ResultParameters":{"ResultParameter":[{"Key":"ReceiptNo","Value":"NA30XKHVCW"},{"Key":"TransactionStatus","Value":"` + txStatus + `"}]},`
		}
		return `{"Result":{"ResultCode":` + string(codeJSON) + `,"ResultDesc":"d",` +
			`"OriginatorConversationID":"wd-1","ConversationID":"AG_1",` + params + `"ResultType":0}}`
	}

	cases := []struct {
		name     string
		code     any
		txStatus string
		want     PayoutStatus
	}{
		{"query ok, transaction completed", 0, "Completed", PayoutSucceeded},
		{"query ok, completed lowercase", 0, "completed", PayoutSucceeded},
		{"query ok, transaction cancelled", 0, "Cancelled", PayoutFailed},
		{"query ok, transaction declined", 0, "Declined", PayoutFailed},
		{"query ok, transaction pending", 0, "Pending", PayoutUnresolved},
		{"query ok, status word we do not know", 0, "Reticulating", PayoutUnresolved},
		{"query ok, no status parameter at all", 0, "", PayoutUnresolved},
		{"THE QUERY ITSELF FAILED", "SFC_IC0003", "", PayoutUnresolved},
		{"query failed with a code that is terminal for a payout", 2001, "", PayoutUnresolved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := ParseStatusCallback([]byte(body(c.code, c.txStatus)), ArrivedAtResultURL)
			if err != nil {
				t.Fatal(err)
			}
			if out.Status != c.want {
				t.Fatalf("status %s, want %s — a failed QUERY says nothing about the payout", out.Status, c.want)
			}
		})
	}

	// The timeout rule applies here too.
	out, err := ParseStatusCallback([]byte(body(0, "Completed")), ArrivedAtTimeoutURL)
	if err != nil {
		t.Fatal(err)
	}
	if out.Status != PayoutUnresolved {
		t.Fatalf("a completed status on the timeout channel classified %s", out.Status)
	}

	if _, err := ParseStatusCallback([]byte(`{"Result":{"OriginatorConversationID":"wd-1"}}`), ArrivedAtResultURL); err == nil {
		t.Fatal("a status result with no ResultCode was accepted")
	}
}

// --- Account Balance --------------------------------------------------------

func TestAccountBalanceAsksAndParses(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()

	if _, err := c.QueryAccountBalance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := s.lastPath.Load().(string); got != "/mpesa/accountbalance/v1/query" {
		t.Fatalf("posted to %q", got)
	}
	if req := s.req(t); req["CommandID"] != "AccountBalance" || req["PartyA"] != "174379" {
		t.Fatalf("wrong shape: %v", req)
	}

	raw := "Working Account|KES|700000.00|700000.00|0.00|0.00&" +
		"Float Account|KES|0.00|0.00|0.00|0.00&" +
		"Utility Account|KES|228037.00|228037.00|0.00|0.00&" +
		"Charges Paid Account|KES|-1540.00|-1540.00|0.00|0.00"
	body := `{"Result":{"ResultCode":0,"ResultDesc":"ok","ResultParameters":{"ResultParameter":[{"Key":"AccountBalance","Value":"` + raw + `"}]}}}`

	accounts, err := ParseBalanceCallback([]byte(body))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 4 {
		t.Fatalf("%d accounts, want 4: %+v", len(accounts), accounts)
	}
	util, ok := UtilityAvailable(accounts)
	if !ok || util != "228037.00" {
		t.Fatalf("utility available %q ok=%v — this is the float a payout draws on", util, ok)
	}

	if _, err := ParseBalanceCallback([]byte(`{"Result":{"ResultCode":1,"ResultDesc":"nope"}}`)); err == nil {
		t.Fatal("a failed balance query parsed as balances")
	}
	if _, err := ParseBalanceCallback([]byte(`{"Result":{"ResultDesc":"d"}}`)); err == nil {
		t.Fatal("a balance result with no ResultCode was accepted")
	}
}

// --- Token handling ---------------------------------------------------------

func TestPayoutsShareThePushTokenAndDropItOn401(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()
	ctx := context.Background()
	send := B2CRequest{OriginatorConversationID: "x", PhoneNumber: "0712345678", Amount: 100}

	if _, err := c.SendB2C(ctx, send); err != nil {
		t.Fatal(err)
	}
	if _, err := c.QueryAccountBalance(ctx); err != nil {
		t.Fatal(err)
	}
	if n := s.authHits.Load(); n != 1 {
		t.Fatalf("authenticated %d times across two payout calls, want 1", n)
	}

	s.answer(401, `{}`)
	if _, err := c.SendB2C(ctx, send); err == nil {
		t.Fatal("a 401 was treated as success")
	}
	s.answer(200, `{"ConversationID":"AG_9","ResponseCode":"0"}`)
	if _, err := c.SendB2C(ctx, send); err != nil {
		t.Fatalf("after the 401: %v", err)
	}
	if n := s.authHits.Load(); n != 2 {
		t.Fatalf("auth hits %d after a 401, want 2 — the stale token was not dropped", n)
	}
}

// The shared acknowledgement decoder, through the status query: the same three
// readings SendB2C has, so a reconciler cannot mistake "Daraja refused the
// question" for "Daraja answered it".
func TestStatusAndBalanceAcknowledgementsClassifyTheSameWay(t *testing.T) {
	s := newB2CStub(t)
	c := s.client()
	ctx := context.Background()

	s.answer(400, `{"requestId":"r","errorCode":"400.002.02","errorMessage":"Bad Request"}`)
	if _, err := c.QueryTransactionStatus(ctx, StatusRequest{OriginatorConversationID: "wd-1"}); !errors.Is(err, ErrPayoutRejected) {
		t.Fatalf("a refused status query returned %v, want a rejection", err)
	}
	if _, err := c.QueryAccountBalance(ctx); !errors.Is(err, ErrPayoutRejected) {
		t.Fatalf("a refused balance query returned %v, want a rejection", err)
	}

	s.answer(200, `{"ResponseCode":"1","ResponseDescription":"Refused"}`)
	if _, err := c.QueryTransactionStatus(ctx, StatusRequest{OriginatorConversationID: "wd-1"}); !errors.Is(err, ErrPayoutRejected) {
		t.Fatalf("a non-zero ResponseCode returned %v, want a rejection", err)
	}

	s.answer(200, `{"ResponseCode":"0","ResponseDescription":"ok"}`)
	if _, err := c.QueryTransactionStatus(ctx, StatusRequest{OriginatorConversationID: "wd-1"}); err == nil {
		t.Fatal("an acknowledgement with no ConversationID was accepted; nothing could be correlated to it")
	}

	s.answer(200, `<html>502</html>`)
	if _, err := c.QueryAccountBalance(ctx); err == nil {
		t.Fatal("an undecodable acknowledgement was accepted")
	} else if errors.Is(err, ErrPayoutRejected) {
		t.Fatal("an undecodable acknowledgement classified as a rejection")
	}
}

// The strings that only ever appear in failure messages and operator logs. Worth
// a line each so a diagnostic is never the thing that is broken.
func TestPayoutDiagnosticStrings(t *testing.T) {
	for status, want := range map[PayoutStatus]string{
		PayoutSucceeded:  "succeeded",
		PayoutFailed:     "failed",
		PayoutUnresolved: "unresolved",
		PayoutStatus(99): "unresolved",
	} {
		if got := status.String(); got != want {
			t.Errorf("PayoutStatus(%d).String() = %q, want %q", status, got, want)
		}
	}

	rej := &PayoutRejection{Code: "400.002.02", Description: "Bad Request"}
	msg := rej.Error()
	if !strings.Contains(msg, "400.002.02") || !strings.Contains(msg, "Bad Request") {
		t.Fatalf("a rejection's message hides what Daraja said: %q", msg)
	}
	if !errors.Is(rej, ErrPayoutRejected) {
		t.Fatal("a PayoutRejection does not match ErrPayoutRejected")
	}
}
