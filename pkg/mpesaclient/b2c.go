package mpesaclient

// Daraja's B2C ("business to customer") API, which pays a customer from the
// business's shortcode — the opposite direction from STK push, and a different
// API in every dimension that matters. It authenticates as an "API user" created
// on the M-PESA organisation portal rather than with the push's passkey, answers
// asynchronously on two callback URLs rather than one, and reuses several of
// STK's result-code numbers with entirely different meanings.
//
// Nothing here shares a type with the STK side on purpose. See
// docs/investigation-reports/b2c-withdrawals-investigation.md §1.4 for the
// vocabulary collision, and §1 generally for what is confirmed first-party
// versus what is a documentation mirror.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// --- Result codes -----------------------------------------------------------

// flexCode is a Daraja result code, which is not reliably a number.
//
// B2C's documented codes include SFC_IC0003 ("the operator does not exist"),
// and the codes that are numeric are variously delivered as JSON numbers and as
// JSON strings. So this keeps the code's canonical TEXT and records whether one
// was present at all.
//
// The thing it refuses to do is default to zero. Zero is success — it is the
// code that releases money — so a parser that returns zero for "absent" or for
// "not a number I recognise" turns every shape surprise into a payout marked
// paid. A neighbouring production codebase does exactly that with an
// int-returning helper whose default branch is 0; this type exists so that
// cannot happen here.
type flexCode struct {
	text    string
	present bool
}

func (f *flexCode) UnmarshalJSON(b []byte) error {
	raw := strings.TrimSpace(string(b))
	if raw == "null" || raw == "" {
		return nil
	}
	raw = strings.TrimSpace(strings.Trim(raw, `"`))
	if raw == "" {
		return nil
	}
	f.text, f.present = raw, true
	return nil
}

// Code returns the code as text, and whether there was one.
func (f flexCode) Code() (string, bool) { return f.text, f.present }

// PayoutFailureCodes are the B2C result codes this package treats as final: the
// payout definitely did not happen and will not. Keyed by the code's text, not
// by an integer, because SFC_IC0003 is one of them.
//
// An allowlist, for the same reason TerminalFailureCodes is one: everything not
// here and not "0" classifies as Unresolved, which the caller must read as "ask
// again", never as "refund". Marking a payout failed wrongly means refunding a
// customer who was in fact paid, and Safaricom's own documentation says B2C
// reversals are performed manually on the M-PESA portal — there is no API to
// take it back.
//
// Note how many of these are the BUSINESS's fault rather than the customer's:
// 1, 11, 21, 2001, 2006, 2028 and 8006 all mean something is wrong at our end.
// That is what service.humanPayoutResult exists to keep out of customer-facing
// copy, and it is why this list is separate from it: whether a code is final and
// what we say about it are different questions, and treating a code as final
// must never wait on someone writing user copy for it.
//
// Source: the Daraja documentation mirror described in the investigation's §1.6.
// First-party confirmation was not obtainable; the probe
// (scripts/daraja_b2c_probe.py) is what will replace this footing with observed
// behaviour.
var PayoutFailureCodes = map[string]string{
	"1":          "the business utility account has insufficient funds",
	"2":          "declined by a limit rule: below the minimum amount",
	"3":          "declined by a limit rule: above the maximum per transaction",
	"4":          "declined by a limit rule: would exceed the daily limit",
	"8":          "declined by a limit rule: would exceed the recipient's maximum balance",
	"11":         "the B2C account is not in a state that permits this",
	"21":         "the initiator is not authorised for B2C",
	"2001":       "the initiator name or encrypted password is wrong",
	"2006":       "the B2C account's status prohibits this transaction",
	"2028":       "the shortcode is not permitted to send B2C",
	"2040":       "the recipient is not registered on M-PESA",
	"8006":       "the API user's password is locked",
	"SFC_IC0003": "the recipient's number does not exist",
}

// PayoutOursCodes are the subset of PayoutFailureCodes that mean the fault is
// ours, not the customer's. A refund makes the customer whole; these also mean
// the next payout fails the same way, so they are worth waking someone for.
var PayoutOursCodes = map[string]bool{
	"1": true, "11": true, "21": true, "2001": true, "2006": true, "2028": true, "8006": true,
}

// --- Outcomes ---------------------------------------------------------------

// PayoutStatus is the three-way reading of a B2C result. As with QueryStatus,
// it is deliberately not the raw code: a caller that has to know which codes
// mean what has to know Daraja.
type PayoutStatus int

const (
	// PayoutUnresolved: Daraja answered, but not with anything final. A queue
	// timeout, a code this package does not recognise as terminal, or a status
	// query that says the transaction is still in progress. The only safe
	// reading is "ask again later" — never "refund".
	PayoutUnresolved PayoutStatus = iota
	// PayoutSucceeded: result code 0. The customer has been paid.
	PayoutSucceeded
	// PayoutFailed: a code in PayoutFailureCodes. No money left the business,
	// and none will for this request.
	PayoutFailed
)

func (p PayoutStatus) String() string {
	switch p {
	case PayoutSucceeded:
		return "succeeded"
	case PayoutFailed:
		return "failed"
	default:
		return "unresolved"
	}
}

// PayoutResult is one B2C outcome, however it arrived.
type PayoutResult struct {
	Status PayoutStatus
	// OriginatorConversationID is the id WE chose and sent, and is how this
	// result is matched to a withdrawal. Present on every result Daraja sends.
	OriginatorConversationID string
	// ConversationID is Safaricom's own id for the request.
	ConversationID string
	// ResultCode as text, exactly as delivered, and ResultDesc as sent.
	ResultCode string
	ResultDesc string
	// Receipt is the M-PESA transaction id, present only on success.
	Receipt string
	// RecipientName is "2547XXXXXXXX - JOHN DOE" when Daraja supplies it. Useful
	// to an operator reconciling by hand; never shown to the payee.
	RecipientName string
	// CompletedAt is Daraja's own timestamp text, unparsed: its format is
	// "03.01.2019 17:48:32", which is not one Go parses by default and not one
	// worth guessing a timezone for.
	CompletedAt string
	// UtilityBalance is the business's remaining B2C utility float as reported
	// on a successful result. Empty when absent. This is the float gauge's only
	// free source of truth.
	UtilityBalance string
	// ArrivedAtTimeoutURL records that this came in on the queue-timeout channel
	// rather than the result channel. Such a payload is never terminal — see
	// ParsePayoutCallback.
	ArrivedAtTimeoutURL bool
	// OursToFix is true when the failure is the business's rather than the
	// customer's.
	OursToFix bool
}

// PayoutArrival says which of the two callback URLs a payload arrived on. The
// distinction is load-bearing and the caller must not guess it from the body.
type PayoutArrival int

const (
	// ArrivedAtResultURL: Daraja processed the request and is reporting what
	// happened.
	ArrivedAtResultURL PayoutArrival = iota
	// ArrivedAtTimeoutURL: Daraja's queue gave up before processing completed.
	// This says "we do not know", not "it failed".
	ArrivedAtTimeoutURL
)

// --- Sending ----------------------------------------------------------------

// B2CRequest is one payout.
type B2CRequest struct {
	// OriginatorConversationID must be generated and PERSISTED by the caller
	// before the send, and never regenerated on a retry. It is Safaricom's own
	// mechanism for avoiding double disbursement, the only handle that exists
	// before Daraja answers, and the key a status query can use for a payout
	// that never returned a receipt. This package will not invent one: a
	// generated-here id would be lost exactly when it is needed, which is when
	// the process dies between the send and the record.
	OriginatorConversationID string
	PhoneNumber              string
	// Amount in whole KES. Daraja rejects fractions.
	Amount   int64
	Remarks  string
	Occasion string
}

// B2CAccepted is the synchronous answer to a payout request: an acknowledgement
// that it was queued, not that it was paid.
type B2CAccepted struct {
	ConversationID           string
	OriginatorConversationID string
	ResponseDescription      string
}

// ErrPayoutRejected means Daraja refused to queue the request. No payout will
// happen for it.
//
// It is separated from a transport error on purpose, and the difference is the
// difference between refunding and waiting. A rejection carries no
// ConversationID, so there is nothing queued that could pay — the caller may
// safely return the money. A transport failure means the request may or may not
// have reached Safaricom, so the caller must NOT refund and must reconcile by
// status query instead.
var ErrPayoutRejected = errors.New("daraja refused the payout request")

// PayoutRejection carries why, for the audit trail and the operator alert.
type PayoutRejection struct {
	Code        string
	Description string
}

func (e *PayoutRejection) Error() string {
	return fmt.Sprintf("daraja refused the payout request: %s %s", e.Code, e.Description)
}
func (e *PayoutRejection) Unwrap() error { return ErrPayoutRejected }

// ValidateB2CConfig reports whether this client is configured to send payouts.
// Call it at boot so a misconfiguration is a failed start rather than a failed
// withdrawal.
//
// The check that matters most is the last one: the result and timeout URLs must
// differ. Pointing both at one handler loses the only signal that distinguishes
// "Daraja processed this and here is the outcome" from "Daraja's queue gave up
// and nobody knows" — and a handler that cannot tell them apart will read a
// timeout's non-zero code as a failure and refund a customer who may have been
// paid. A neighbouring production codebase is wired exactly that way; this
// refuses to be.
func (c *Client) ValidateB2CConfig() error {
	var missing []string
	if c.initiatorName == "" {
		missing = append(missing, "initiator name")
	}
	if c.securityCredential == "" {
		missing = append(missing, "security credential")
	}
	if c.b2cResultURL == "" {
		missing = append(missing, "B2C result URL")
	}
	if c.b2cTimeoutURL == "" {
		missing = append(missing, "B2C queue-timeout URL")
	}
	if c.b2cStatusResultURL == "" {
		missing = append(missing, "B2C status-result URL")
	}
	if len(missing) > 0 {
		return fmt.Errorf("b2c is not configured: missing %s", strings.Join(missing, ", "))
	}
	if c.b2cResultURL == c.b2cTimeoutURL {
		return errors.New("b2c result and queue-timeout URLs must differ: a timeout means " +
			"'we do not know', and one handler for both cannot tell it from a failure")
	}
	// The same argument, one step along: a status-query answer and a payout result
	// are different documents, and a handler that has to guess which arrived is a
	// handler that can classify a query's own failure as the payout's.
	if c.b2cStatusResultURL == c.b2cResultURL || c.b2cStatusResultURL == c.b2cTimeoutURL {
		return errors.New("b2c status-result URL must differ from the payout result and " +
			"queue-timeout URLs: a status answer is a different document from a payout result")
	}
	return nil
}

// SendB2C requests a payout and reports that Daraja queued it.
//
// v3 only. v3 is what takes OriginatorConversationID, which the whole design
// rests on, and it is what is already in production use next door; there is no
// v1 path here to fall back to and no code that could choose one.
//
// Three outcomes, and the caller owes each a different response:
//
//   - accepted, nil error → queued. Wait for the result callback. Nothing is
//     known about the payment yet.
//   - a *PayoutRejection (errors.Is ErrPayoutRejected) → Daraja refused it.
//     Nothing was queued, so nothing can pay; the caller may refund.
//   - any other error → UNKNOWN. The request may have reached Safaricom. The
//     caller must not refund and must reconcile by status query.
func (c *Client) SendB2C(ctx context.Context, r B2CRequest) (*B2CAccepted, error) {
	if err := c.ValidateB2CConfig(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.OriginatorConversationID) == "" {
		return nil, errors.New("b2c: OriginatorConversationID must be set and persisted by the caller before sending")
	}
	if r.Amount <= 0 {
		return nil, fmt.Errorf("b2c: amount must be a positive whole number of KES, got %d", r.Amount)
	}
	phone := NormalizePhone(r.PhoneNumber)
	if !ValidPhone(phone) {
		return nil, fmt.Errorf("b2c: %q is not a Safaricom-shaped number", r.PhoneNumber)
	}
	remarks := r.Remarks
	if remarks == "" {
		remarks = "Withdrawal"
	}

	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	payload := map[string]any{
		"OriginatorConversationID": r.OriginatorConversationID,
		"InitiatorName":            c.initiatorName,
		"SecurityCredential":       c.securityCredential,
		"CommandID":                "BusinessPayment",
		"Amount":                   r.Amount,
		"PartyA":                   c.shortcode,
		"PartyB":                   phone,
		"Remarks":                  remarks,
		"QueueTimeOutURL":          c.b2cTimeoutURL,
		"ResultURL":                c.b2cResultURL,
	}
	if r.Occasion != "" {
		payload["Occasion"] = r.Occasion
	}

	body, status, err := c.postJSON(ctx, "/mpesa/b2c/v3/paymentrequest", payload, token)
	if err != nil {
		return nil, fmt.Errorf("b2c send: %w", err)
	}

	var out struct {
		ConversationID           string `json:"ConversationID"`
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
		ErrorCode                string `json:"errorCode"`
		ErrorMessage             string `json:"errorMessage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("b2c send: status %d, undecodable body: %w (body %s)", status, err, truncate(body))
	}

	// An envelope is a refusal here, unlike on a status query: Daraja is saying
	// it would not take the request.
	if out.ErrorCode != "" {
		return nil, &PayoutRejection{Code: out.ErrorCode, Description: out.ErrorMessage}
	}
	if out.ResponseCode != "" && out.ResponseCode != "0" {
		return nil, &PayoutRejection{Code: out.ResponseCode, Description: out.ResponseDescription}
	}
	// Accepted means queued, and queued means correlatable. A 200 with neither an
	// error nor a ConversationID is a shape we do not understand, and guessing
	// "accepted" would leave a payout in flight that no callback could ever be
	// matched to.
	if out.ConversationID == "" {
		return nil, fmt.Errorf("b2c send: status %d accepted with no ConversationID (body %s)", status, truncate(body))
	}
	acc := &B2CAccepted{
		ConversationID:           out.ConversationID,
		OriginatorConversationID: out.OriginatorConversationID,
		ResponseDescription:      out.ResponseDescription,
	}
	if acc.OriginatorConversationID == "" {
		// Daraja echoes it; if it did not, ours is still the id we sent and the
		// one every later lookup will use.
		acc.OriginatorConversationID = r.OriginatorConversationID
	}
	return acc, nil
}

// --- Receiving --------------------------------------------------------------

// resultEnvelope is the shape Daraja posts to ResultURL and QueueTimeOutURL for
// B2C, Transaction Status and Account Balance alike.
type resultEnvelope struct {
	Result struct {
		ResultType               int      `json:"ResultType"`
		ResultCode               flexCode `json:"ResultCode"`
		ResultDesc               string   `json:"ResultDesc"`
		OriginatorConversationID string   `json:"OriginatorConversationID"`
		ConversationID           string   `json:"ConversationID"`
		TransactionID            string   `json:"TransactionID"`
		ResultParameters         struct {
			ResultParameter json.RawMessage `json:"ResultParameter"`
		} `json:"ResultParameters"`
	} `json:"Result"`
}

// resultParams flattens ResultParameter, which Daraja sends as an array of
// {Key, Value} but has been observed to send as a single object when there is
// one entry. Both are accepted; anything else is ignored rather than fatal,
// because a parameter this package does not need must never fail a payout.
func resultParams(raw json.RawMessage) map[string]string {
	out := map[string]string{}
	if len(raw) == 0 {
		return out
	}
	type kv struct {
		Key   string          `json:"Key"`
		Value json.RawMessage `json:"Value"`
	}
	add := func(e kv) {
		if e.Key == "" {
			return
		}
		v := strings.TrimSpace(strings.Trim(strings.TrimSpace(string(e.Value)), `"`))
		out[e.Key] = v
	}
	var list []kv
	if err := json.Unmarshal(raw, &list); err == nil {
		for _, e := range list {
			add(e)
		}
		return out
	}
	var one kv
	if err := json.Unmarshal(raw, &one); err == nil {
		add(one)
	}
	return out
}

// ParsePayoutCallback decodes what Daraja posted about a payout.
//
// `arrival` says which URL it came in on, and the caller must supply it from the
// route rather than infer it from the body — the body does not reliably say.
//
// The rule that matters: a payload that arrived on the queue-timeout URL is
// ALWAYS Unresolved, whatever code it carries, including 0. The timeout channel
// means Daraja's queue gave up before it knew the outcome, so nothing arriving
// there is evidence of anything. Reading its non-zero code as a failure would
// refund a customer who may have been paid; reading a 0 there as success would
// settle a payout that may never have happened. Both are worse than waiting, and
// the status query exists for exactly this case.
//
// Otherwise: 0 is success, a code in PayoutFailureCodes is failure, everything
// else is Unresolved. An absent result code is an error, never a zero.
func ParsePayoutCallback(body []byte, arrival PayoutArrival) (PayoutResult, error) {
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return PayoutResult{}, fmt.Errorf("b2c callback: undecodable body: %w (body %s)", err, truncate(body))
	}
	r := env.Result
	if r.OriginatorConversationID == "" && r.ConversationID == "" {
		return PayoutResult{}, fmt.Errorf("b2c callback: no conversation id to match a withdrawal to (body %s)", truncate(body))
	}

	params := resultParams(r.ResultParameters.ResultParameter)
	out := PayoutResult{
		OriginatorConversationID: r.OriginatorConversationID,
		ConversationID:           r.ConversationID,
		ResultDesc:               r.ResultDesc,
		Receipt:                  firstNonEmpty(params["TransactionReceipt"], r.TransactionID),
		RecipientName:            params["ReceiverPartyPublicName"],
		CompletedAt:              params["TransactionCompletedDateTime"],
		UtilityBalance:           params["B2CUtilityAccountAvailableFunds"],
		ArrivedAtTimeoutURL:      arrival == ArrivedAtTimeoutURL,
	}

	code, present := r.ResultCode.Code()
	if !present {
		return PayoutResult{}, fmt.Errorf("b2c callback: no ResultCode (body %s)", truncate(body))
	}
	out.ResultCode = code

	if arrival == ArrivedAtTimeoutURL {
		out.Status = PayoutUnresolved
		return out, nil
	}
	switch {
	case code == "0":
		// A success with no receipt is still a success; it is simply one we
		// cannot evidence. Nothing downstream may synthesise a receipt a customer
		// could be told to check — the deposit side settled that in STK CP0.
		out.Status = PayoutSucceeded
	case PayoutFailureCodes[code] != "":
		out.Status = PayoutFailed
		out.OursToFix = PayoutOursCodes[code]
	default:
		out.Status = PayoutUnresolved
	}
	return out, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// --- Transaction Status -----------------------------------------------------

// StatusRequest asks what became of a payout. Exactly one of the two ids is
// needed; OriginatorConversationID is the one to prefer, because a payout that
// never returned a receipt has no TransactionID and it is precisely those that
// need asking about.
type StatusRequest struct {
	OriginatorConversationID string
	TransactionID            string
	Remarks                  string
	Occasion                 string
}

// StatusAccepted is the synchronous acknowledgement. The answer itself arrives
// later on the result URL — this API is asynchronous, unlike STK query, so a
// reconciler built on it asks and then waits rather than asking and reading.
type StatusAccepted struct {
	ConversationID           string
	OriginatorConversationID string
	ResponseDescription      string
}

// QueryTransactionStatus asks Daraja to report on a transaction. It returns when
// Daraja has accepted the question, not when it has answered it.
func (c *Client) QueryTransactionStatus(ctx context.Context, r StatusRequest) (*StatusAccepted, error) {
	if err := c.ValidateB2CConfig(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(r.OriginatorConversationID) == "" && strings.TrimSpace(r.TransactionID) == "" {
		return nil, errors.New("transaction status: one of OriginatorConversationID or TransactionID is required")
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}

	remarks := r.Remarks
	if remarks == "" {
		remarks = "Withdrawal status check"
	}
	payload := map[string]any{
		"Initiator":          c.initiatorName,
		"SecurityCredential": c.securityCredential,
		"CommandID":          "TransactionStatusQuery",
		"PartyA":             c.shortcode,
		"IdentifierType":     "4", // 4 = organisation shortcode
		"ResultURL":          c.b2cStatusResultURL,
		"QueueTimeOutURL":    c.b2cTimeoutURL,
		"Remarks":            remarks,
	}
	if r.TransactionID != "" {
		payload["TransactionID"] = r.TransactionID
	}
	if r.OriginatorConversationID != "" {
		// The mirror spells this OriginalConversationID on this endpoint, not
		// OriginatorConversationID as on the B2C request. Spelled as documented;
		// the probe is what will confirm it.
		payload["OriginalConversationID"] = r.OriginatorConversationID
	}
	if r.Occasion != "" {
		payload["Occasion"] = r.Occasion
	}

	body, status, err := c.postJSON(ctx, "/mpesa/transactionstatus/v1/query", payload, token)
	if err != nil {
		return nil, fmt.Errorf("transaction status: %w", err)
	}
	acc, err := decodeAck(body, status)
	if err != nil {
		return nil, fmt.Errorf("transaction status: %w", err)
	}
	return &StatusAccepted{
		ConversationID:           acc.conversationID,
		OriginatorConversationID: acc.originatorConversationID,
		ResponseDescription:      acc.description,
	}, nil
}

// TransactionStatusCompleted and friends are the values Daraja is documented to
// report in the TransactionStatus result parameter. Matched case-insensitively,
// and anything unrecognised classifies Unresolved rather than failed.
var statusMeansPaid = map[string]bool{"completed": true, "success": true}
var statusMeansNotPaid = map[string]bool{"cancelled": true, "declined": true, "failed": true, "expired": true}

// ParseStatusCallback decodes a Transaction Status result and classifies it the
// same three ways a payout result classifies, so a reconciler has one
// vocabulary. The timeout rule applies here too.
func ParseStatusCallback(body []byte, arrival PayoutArrival) (PayoutResult, error) {
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return PayoutResult{}, fmt.Errorf("transaction status callback: undecodable body: %w (body %s)", err, truncate(body))
	}
	r := env.Result
	params := resultParams(r.ResultParameters.ResultParameter)
	out := PayoutResult{
		OriginatorConversationID: firstNonEmpty(r.OriginatorConversationID, params["OriginatorConversationID"], params["Originator Conversation ID"]),
		ConversationID:           firstNonEmpty(r.ConversationID, params["ConversationID"], params["Conversation ID"]),
		ResultDesc:               r.ResultDesc,
		Receipt:                  firstNonEmpty(params["ReceiptNo"], r.TransactionID),
		RecipientName:            params["CreditPartyName"],
		CompletedAt:              params["FinalisedTime"],
		ArrivedAtTimeoutURL:      arrival == ArrivedAtTimeoutURL,
	}
	if out.OriginatorConversationID == "" && out.ConversationID == "" {
		return PayoutResult{}, fmt.Errorf("transaction status callback: no conversation id (body %s)", truncate(body))
	}

	code, present := r.ResultCode.Code()
	if !present {
		return PayoutResult{}, fmt.Errorf("transaction status callback: no ResultCode (body %s)", truncate(body))
	}
	out.ResultCode = code

	if arrival == ArrivedAtTimeoutURL {
		return out, nil
	}
	// A non-zero result code on a STATUS query means the query failed, not that
	// the payout failed. Nothing about the payout is known, so it stays
	// unresolved — this is the distinction a single shared classifier would lose.
	if code != "0" {
		out.Status = PayoutUnresolved
		return out, nil
	}
	switch txStatus := strings.ToLower(strings.TrimSpace(params["TransactionStatus"])); {
	case statusMeansPaid[txStatus]:
		out.Status = PayoutSucceeded
	case statusMeansNotPaid[txStatus]:
		out.Status = PayoutFailed
	default:
		// Including "Pending", and including absent: the query succeeded and told
		// us the payout has not resolved.
		out.Status = PayoutUnresolved
	}
	return out, nil
}

// --- Account Balance --------------------------------------------------------

// BalanceAccount is one line of the Account Balance result.
type BalanceAccount struct {
	Name      string
	Currency  string
	Available string
}

// QueryAccountBalance asks for the shortcode's balances. Asynchronous, like the
// status query: the answer arrives on the result URL.
func (c *Client) QueryAccountBalance(ctx context.Context) (*StatusAccepted, error) {
	if err := c.ValidateB2CConfig(); err != nil {
		return nil, err
	}
	token, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"Initiator":          c.initiatorName,
		"SecurityCredential": c.securityCredential,
		"CommandID":          "AccountBalance",
		"PartyA":             c.shortcode,
		"IdentifierType":     "4",
		"Remarks":            "Utility float check",
		"ResultURL":          c.b2cResultURL,
		"QueueTimeOutURL":    c.b2cTimeoutURL,
	}
	body, status, err := c.postJSON(ctx, "/mpesa/accountbalance/v1/query", payload, token)
	if err != nil {
		return nil, fmt.Errorf("account balance: %w", err)
	}
	acc, err := decodeAck(body, status)
	if err != nil {
		return nil, fmt.Errorf("account balance: %w", err)
	}
	return &StatusAccepted{
		ConversationID:           acc.conversationID,
		OriginatorConversationID: acc.originatorConversationID,
		ResponseDescription:      acc.description,
	}, nil
}

// ParseBalanceCallback decodes an Account Balance result into its accounts.
//
// Daraja delivers them as one pipe-and-ampersand delimited string:
//
//	Working Account|KES|700000.00|700000.00|0.00|0.00&Utility Account|KES|228037.00|…
//
// The Utility account is the one B2C pays from, so it is the one worth a gauge.
func ParseBalanceCallback(body []byte) ([]BalanceAccount, error) {
	var env resultEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("account balance callback: undecodable body: %w (body %s)", err, truncate(body))
	}
	code, present := env.Result.ResultCode.Code()
	if !present {
		return nil, fmt.Errorf("account balance callback: no ResultCode (body %s)", truncate(body))
	}
	if code != "0" {
		return nil, fmt.Errorf("account balance callback: result code %s: %s", code, env.Result.ResultDesc)
	}
	params := resultParams(env.Result.ResultParameters.ResultParameter)
	raw := firstNonEmpty(params["AccountBalance"], params["Account Balance"])
	if raw == "" {
		return nil, fmt.Errorf("account balance callback: no AccountBalance parameter (body %s)", truncate(body))
	}

	out := []BalanceAccount{}
	for _, entry := range strings.Split(raw, "&") {
		fields := strings.Split(entry, "|")
		if len(fields) < 3 || strings.TrimSpace(fields[0]) == "" {
			continue
		}
		out = append(out, BalanceAccount{
			Name:      strings.TrimSpace(fields[0]),
			Currency:  strings.TrimSpace(fields[1]),
			Available: strings.TrimSpace(fields[2]),
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("account balance callback: no accounts in %q", raw)
	}
	return out, nil
}

// UtilityAvailable picks the account B2C pays from.
func UtilityAvailable(accounts []BalanceAccount) (string, bool) {
	for _, a := range accounts {
		if strings.EqualFold(a.Name, "Utility Account") {
			return a.Available, true
		}
	}
	return "", false
}

// --- Shared plumbing --------------------------------------------------------

type ackFields struct {
	conversationID           string
	originatorConversationID string
	description              string
}

// decodeAck reads the synchronous acknowledgement the three initiator-credential
// endpoints share. A rejection is a *PayoutRejection so callers can tell "Daraja
// refused" from "we never reached Daraja".
func decodeAck(body []byte, status int) (ackFields, error) {
	var out struct {
		ConversationID           string `json:"ConversationID"`
		OriginatorConversationID string `json:"OriginatorConversationID"`
		ResponseCode             string `json:"ResponseCode"`
		ResponseDescription      string `json:"ResponseDescription"`
		ErrorCode                string `json:"errorCode"`
		ErrorMessage             string `json:"errorMessage"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ackFields{}, fmt.Errorf("status %d, undecodable body: %w (body %s)", status, err, truncate(body))
	}
	if out.ErrorCode != "" {
		return ackFields{}, &PayoutRejection{Code: out.ErrorCode, Description: out.ErrorMessage}
	}
	if out.ResponseCode != "" && out.ResponseCode != "0" {
		return ackFields{}, &PayoutRejection{Code: out.ResponseCode, Description: out.ResponseDescription}
	}
	if out.ConversationID == "" {
		return ackFields{}, fmt.Errorf("status %d accepted with no ConversationID (body %s)", status, truncate(body))
	}
	return ackFields{
		conversationID:           out.ConversationID,
		originatorConversationID: out.OriginatorConversationID,
		description:              out.ResponseDescription,
	}, nil
}

// postJSON sends an authenticated JSON POST and returns the body and status. A
// 401 drops the cached token, as on the STK query: these are long-running
// callers and a token that stops being good mid-hour must not be retried for the
// rest of the cache window.
func (c *Client) postJSON(ctx context.Context, path string, payload any, token string) ([]byte, int, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		c.mu.Lock()
		c.token, c.tokenExpiry = "", time.Time{}
		c.mu.Unlock()
	}
	return body, resp.StatusCode, nil
}
