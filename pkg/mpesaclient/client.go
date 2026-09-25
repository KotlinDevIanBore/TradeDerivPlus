// Package mpesaclient wraps the raw Safaricom Daraja HTTP protocol.
// It has no knowledge of our database or domain types.
package mpesaclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Client struct {
	baseURL        string
	consumerKey    string
	consumerSecret string
	shortcode      string
	passkey        string
	callbackURL    string
	http           *http.Client

	// B2C, and the two other endpoints that authenticate as the same API user.
	// The credential is the initiator's password already encrypted under
	// Safaricom's certificate — see EncryptInitiatorPassword and cmd/gencred. It
	// is generated once, offline, and supplied as a single value; nothing here
	// holds the plaintext password or reads a certificate at request time.
	initiatorName      string
	securityCredential string
	// Two distinct URLs, deliberately. See ValidateB2CConfig.
	b2cResultURL  string
	b2cTimeoutURL string

	mu          sync.Mutex
	token       string
	tokenExpiry time.Time
}

type Options struct {
	BaseURL        string
	ConsumerKey    string
	ConsumerSecret string
	Shortcode      string
	Passkey        string
	CallbackURL    string

	// Payouts. Optional: a client without these serves deposits perfectly well
	// and refuses payouts through ValidateB2CConfig.
	InitiatorName      string
	SecurityCredential string
	B2CResultURL       string
	B2CTimeoutURL      string
}

func New(o Options) *Client {
	return &Client{
		baseURL:        o.BaseURL,
		consumerKey:    o.ConsumerKey,
		consumerSecret: o.ConsumerSecret,
		shortcode:      o.Shortcode,
		passkey:        o.Passkey,
		callbackURL:    o.CallbackURL,
		http:           &http.Client{Timeout: 30 * time.Second},

		initiatorName:      o.InitiatorName,
		securityCredential: o.SecurityCredential,
		b2cResultURL:       o.B2CResultURL,
		b2cTimeoutURL:      o.B2CTimeoutURL,
	}
}

type STKPushRequest struct {
	PhoneNumber string
	Amount      int64
	AccountRef  string
	Description string
}

type STKPushResponse struct {
	MerchantRequestID   string `json:"MerchantRequestID"`
	CheckoutRequestID   string `json:"CheckoutRequestID"`
	ResponseCode        string `json:"ResponseCode"`
	ResponseDescription string `json:"ResponseDescription"`
	CustomerMessage     string `json:"CustomerMessage"`
	ErrorCode           string `json:"errorCode"`
	ErrorMessage        string `json:"errorMessage"`
}

// Callback is the payload Daraja POSTs to our callback URL.
type Callback struct {
	Body struct {
		STKCallback struct {
			MerchantRequestID string `json:"MerchantRequestID"`
			CheckoutRequestID string `json:"CheckoutRequestID"`
			ResultCode        int    `json:"ResultCode"`
			ResultDesc        string `json:"ResultDesc"`
			CallbackMetadata  struct {
				Item []struct {
					Name  string `json:"Name"`
					Value any    `json:"Value"`
				} `json:"Item"`
			} `json:"CallbackMetadata"`
		} `json:"stkCallback"`
	} `json:"Body"`
}

// ReceiptNumber pulls MpesaReceiptNumber out of the callback metadata.
func (c Callback) ReceiptNumber() string {
	for _, it := range c.Body.STKCallback.CallbackMetadata.Item {
		if it.Name == "MpesaReceiptNumber" {
			if s, ok := it.Value.(string); ok {
				return s
			}
		}
	}
	return ""
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Now().Before(c.tokenExpiry) {
		return c.token, nil
	}

	url := c.baseURL + "/oauth/v1/generate?grant_type=client_credentials"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.consumerKey, c.consumerSecret)

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("daraja auth: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("daraja auth status %d: %s", resp.StatusCode, string(body))
	}

	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   string `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", err
	}
	secs, _ := strconv.Atoi(out.ExpiresIn)
	if secs == 0 {
		secs = 3599
	}
	c.token = out.AccessToken
	// Refresh a minute early so a request never races the expiry.
	c.tokenExpiry = time.Now().Add(time.Duration(secs-60) * time.Second)
	return c.token, nil
}

func (c *Client) STKPush(ctx context.Context, r STKPushRequest) (*STKPushResponse, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	timestamp := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString([]byte(c.shortcode + c.passkey + timestamp))

	payload := map[string]any{
		"BusinessShortCode": c.shortcode,
		"Password":          password,
		"Timestamp":         timestamp,
		"TransactionType":   "CustomerPayBillOnline",
		"Amount":            r.Amount,
		"PartyA":            r.PhoneNumber,
		"PartyB":            c.shortcode,
		"PhoneNumber":       r.PhoneNumber,
		"CallBackURL":       c.callbackURL,
		"AccountReference":  r.AccountRef,
		"TransactionDesc":   r.Description,
	}
	b, _ := json.Marshal(payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/mpesa/stkpush/v1/processrequest", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("stk push: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var out STKPushResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("stk push decode: %w (body %s)", err, string(body))
	}
	if out.ErrorCode != "" {
		return &out, fmt.Errorf("daraja %s: %s", out.ErrorCode, out.ErrorMessage)
	}
	if out.ResponseCode != "0" {
		return &out, fmt.Errorf("daraja rejected: %s", out.ResponseDescription)
	}
	return &out, nil
}

// QueryStatus is the three-way answer a status query can give. It is
// deliberately not the raw ResultCode: a caller that has to know which codes
// mean what has to know Daraja, and the point of this package is that nothing
// else does.
type QueryStatus int

const (
	// QueryUnresolved: Daraja answered, but not with a terminal outcome. Still
	// processing, or a code this package does not recognise as final. The only
	// safe reading is "ask again later".
	QueryUnresolved QueryStatus = iota
	// QuerySucceeded: ResultCode 0. The customer paid.
	QuerySucceeded
	// QueryFailed: a ResultCode in TerminalFailureCodes. The customer did not
	// pay, and will not.
	QueryFailed
)

func (q QueryStatus) String() string {
	switch q {
	case QuerySucceeded:
		return "succeeded"
	case QueryFailed:
		return "failed"
	default:
		return "unresolved"
	}
}

// QueryOutcome is what STKQuery reports about one push.
type QueryOutcome struct {
	Status     QueryStatus
	ResultCode int
	ResultDesc string
}

// TerminalFailureCodes are the ResultCodes this package treats as final: the
// customer's payment definitely did not happen. Everything not here and not 0
// is Unresolved — including every errorCode envelope, whatever its code.
//
// An allowlist, not a denylist, on purpose. Daraja's in-flight response is an
// error envelope whose code the public sources disagree about (500.001.1001 is
// variously "being processed", "insufficient business funds" and "malformed
// password"). Treating anything unrecognised as "wait" is correct under all
// three readings; treating it as failure is correct under one and marks paid
// deposits failed under the others.
//
// This is a different list from the copy in service.humanResult, which maps
// codes Daraja has already told us are final to words a user can act on. That
// one answers "what do we say"; this one answers "is it over". They are kept
// apart so that treating a code as terminal never depends on having written
// user copy for it.
var TerminalFailureCodes = map[int]bool{
	1:    true, // insufficient balance
	1001: true, // unable to lock subscriber; another transaction in progress
	1032: true, // cancelled by the user
	1037: true, // DS timeout; the customer could not be reached
	2001: true, // wrong PIN
}

// flexInt decodes a JSON number or a JSON string holding a number. The push
// callback carries ResultCode as a number; the query response is reported to
// carry it as a string, and the response shape is not documented first-party
// (see stk-query-step2-findings.md §2). Accepting both costs nothing and means
// a shape surprise cannot turn a paid deposit into an unparseable body.
type flexInt struct {
	value int
	set   bool
}

func (f *flexInt) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" || raw == "" {
		return nil
	}
	raw = strings.Trim(raw, `"`)
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return fmt.Errorf("ResultCode %q is not a number", raw)
	}
	f.value, f.set = n, true
	return nil
}

// STKQuery asks Daraja what became of a push.
//
// It sends the same four things the push derived — shortcode, the base64
// password, the timestamp, and the CheckoutRequestID it returned — with the same
// bearer token, so it needs no credential or config the push does not have.
//
// The classification is the whole of the contract:
//
//  1. Transport failure, a non-2xx that is not a Daraja envelope, or a body that
//     is not JSON → error. Daraja said nothing, and the caller must not treat
//     silence as an attempt.
//  2. A Daraja errorCode/errorMessage envelope, at any HTTP status, whatever the
//     code → Unresolved, nil error. This is how Daraja reports "still
//     processing", and it is the disputed case: see TerminalFailureCodes.
//  3. ResultCode 0 → Succeeded.
//  4. ResultCode in TerminalFailureCodes → Failed.
//  5. Any other ResultCode → Unresolved.
//
// Note the deliberate asymmetry with STKPush, which returns error for an
// errorCode envelope: for a push, an envelope means the request was refused;
// for a query, the same envelope is an answer.
func (c *Client) STKQuery(ctx context.Context, checkoutID string) (QueryOutcome, error) {
	token, err := c.accessToken(ctx)
	if err != nil {
		return QueryOutcome{}, err
	}

	timestamp := time.Now().Format("20060102150405")
	password := base64.StdEncoding.EncodeToString([]byte(c.shortcode + c.passkey + timestamp))

	b, _ := json.Marshal(map[string]any{
		"BusinessShortCode": c.shortcode,
		"Password":          password,
		"Timestamp":         timestamp,
		"CheckoutRequestID": checkoutID,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/mpesa/stkpushquery/v1/query", bytes.NewReader(b))
	if err != nil {
		return QueryOutcome{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return QueryOutcome{}, fmt.Errorf("stk query: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		// The cached token has stopped being good before it was due to. Drop it
		// so the next call re-authenticates instead of repeating this for the
		// rest of the cache window.
		c.mu.Lock()
		c.token, c.tokenExpiry = "", time.Time{}
		c.mu.Unlock()
	}

	var out struct {
		ResultCode   *flexInt `json:"ResultCode"`
		ResultDesc   string   `json:"ResultDesc"`
		ErrorCode    string   `json:"errorCode"`
		ErrorMessage string   `json:"errorMessage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return QueryOutcome{}, fmt.Errorf("stk query: status %d, undecodable body: %w (body %s)",
			resp.StatusCode, err, truncate(body))
	}

	// An envelope is an answer, whatever the HTTP status wrapped around it.
	if out.ErrorCode != "" {
		return QueryOutcome{Status: QueryUnresolved, ResultDesc: out.ErrorMessage}, nil
	}
	if out.ResultCode == nil || !out.ResultCode.set {
		// JSON, but neither a ResultCode nor an envelope. Daraja said nothing
		// this package can read, and a non-2xx here is the ordinary meaning of
		// that status.
		return QueryOutcome{}, fmt.Errorf("stk query: status %d with no ResultCode or errorCode (body %s)",
			resp.StatusCode, truncate(body))
	}

	code := out.ResultCode.value
	o := QueryOutcome{ResultCode: code, ResultDesc: out.ResultDesc}
	switch {
	case code == 0:
		o.Status = QuerySucceeded
	case TerminalFailureCodes[code]:
		o.Status = QueryFailed
	default:
		o.Status = QueryUnresolved
	}
	return o, nil
}

// truncate keeps an error message readable when a body is not.
func truncate(b []byte) string {
	const max = 200
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…"
}

// NormalizePhone converts 07XXXXXXXX / +2547XXXXXXXX / 2547XXXXXXXX to 2547XXXXXXXX.
func NormalizePhone(p string) string {
	cleaned := make([]byte, 0, len(p))
	for i := 0; i < len(p); i++ {
		if p[i] >= '0' && p[i] <= '9' {
			cleaned = append(cleaned, p[i])
		}
	}
	s := string(cleaned)
	switch {
	case len(s) == 10 && s[0] == '0':
		return "254" + s[1:]
	case len(s) == 9 && (s[0] == '7' || s[0] == '1'):
		return "254" + s
	default:
		return s
	}
}

// ValidPhone reports whether p normalizes to a Safaricom-shaped MSISDN.
func ValidPhone(p string) bool {
	n := NormalizePhone(p)
	return len(n) == 12 && n[:3] == "254" && (n[3] == '7' || n[3] == '1')
}
