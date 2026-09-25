package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Enum-ish string constants. Kept as typed strings so handlers can validate cheaply.

const (
	// KYCUnverified is the default for a new user: nothing has been submitted.
	// Distinct from KYCPending, which now means exactly "documents are in, a
	// person has not looked yet" rather than doubling as "never started".
	KYCUnverified = "unverified"
	KYCPending    = "pending"
	KYCVerified   = "verified"
	KYCRejected   = "rejected"
)

const (
	AccountDemo = "demo"
	AccountReal = "real"
)

const (
	TxDeposit     = "deposit"
	TxWithdrawal  = "withdrawal"
	TxTradeOpen   = "trade_open"
	TxTradeClose  = "trade_close"
	TxDigitOpen   = "digit_open"
	TxDigitSettle = "digit_settle"
	TxBonusCredit = "bonus_credit"
	// TxAdminAdjustment is a manual balance correction. It is an ordinary
	// ledger row so the ledger stays the single source of truth; there is no
	// side-channel that edits a balance without recording why.
	TxAdminAdjustment = "admin_adjustment"
)

const (
	BalanceReal  = "real"
	BalanceBonus = "bonus"
)

const (
	StatusPending   = "pending"
	StatusCompleted = "completed"
	StatusFailed    = "failed"
)

const (
	DirectionBuy  = "buy"
	DirectionSell = "sell"
)

const (
	TradeOpen   = "open"
	TradeClosed = "closed"
)

const (
	CategoryVolatility = "volatility"
	CategoryBoomCrash  = "boom_crash"
	CategoryForex      = "forex"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	PhoneNumber  string    `json:"phone_number"`
	Email        *string   `json:"email"`
	PasswordHash string    `json:"-"`
	FullName     *string   `json:"full_name"`
	KYCStatus    string    `json:"kyc_status"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

type Account struct {
	ID            uuid.UUID `json:"id"`
	UserID        uuid.UUID `json:"user_id"`
	AccountNumber string    `json:"account_number"`
	AccountType   string    `json:"account_type"`
	Currency      string    `json:"currency"`
	Status        string    `json:"status"`
	FrozenReason  *string   `json:"frozen_reason"`
	CreatedAt     time.Time `json:"created_at"`
}

// IsFrozen reports whether the account may move on its own. A frozen account
// still allows closing an open position and still allows an admin adjustment;
// see AdminService.Adjust for why.
func (a Account) IsFrozen() bool { return a.Status == AccountFrozen }

type Wallet struct {
	AccountID    uuid.UUID       `json:"account_id"`
	RealBalance  decimal.Decimal `json:"real_balance"`
	BonusBalance decimal.Decimal `json:"bonus_balance"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// Total is what the UI shows as the hero balance.
func (w Wallet) Total() decimal.Decimal { return w.RealBalance.Add(w.BonusBalance) }

// MarshalJSON adds the derived total to every serialised wallet, so no caller
// has to remember to compute it and no screen can render a balance of zero
// beside a real and bonus that plainly are not. Deriving it here rather than
// storing a field means it cannot drift from its parts.
func (w Wallet) MarshalJSON() ([]byte, error) {
	type wire Wallet // avoids recursing into this method
	return json.Marshal(struct {
		wire
		Total decimal.Decimal `json:"total"`
	}{wire(w), w.Total()})
}

type Transaction struct {
	ID          uuid.UUID       `json:"id"`
	AccountID   uuid.UUID       `json:"account_id"`
	Type        string          `json:"type"`
	BalanceType string          `json:"balance_type"`
	Amount      decimal.Decimal `json:"amount"`
	Status      string          `json:"status"`
	Reference   *string         `json:"reference"`
	Metadata    map[string]any  `json:"metadata"`
	CreatedAt   time.Time       `json:"created_at"`
}

type MpesaRequest struct {
	ID                uuid.UUID       `json:"id"`
	TransactionID     uuid.UUID       `json:"transaction_id"`
	CheckoutRequestID string          `json:"checkout_request_id"`
	MerchantRequestID string          `json:"merchant_request_id"`
	PhoneNumber       string          `json:"phone_number"`
	Amount            decimal.Decimal `json:"amount"`
	Status            string          `json:"status"`
	ResultCode        *int            `json:"result_code"`
	ResultDesc        *string         `json:"result_desc"`
	CreatedAt         time.Time       `json:"created_at"`

	// Reconciliation state (migration 0007), for a pending row whose callback
	// has not arrived. ReconcileAttempts counts the queries Daraja answered
	// without resolving it; NextQueryAt is when the pass may next ask. Nil only
	// on rows that were already terminal when 0007 ran.
	ReconcileAttempts int        `json:"reconcile_attempts"`
	NextQueryAt       *time.Time `json:"next_query_at"`
}

// Payout lifecycle, on mpesa_payouts.status. The two middle states are the
// point of the table: they are what "we do not know" and "we know it never
// happened" look like when they have to be told apart by code rather than by a
// human reading a log.
const (
	// PayoutPending: reserved in the same transaction as the debit, nothing sent.
	PayoutPending = "pending"
	// PayoutSent: Daraja accepted the request and returned a ConversationID.
	PayoutSent = "sent"
	// PayoutUnknown: the send was ambiguous — a transport failure or a timeout
	// after dispatch. Daraja may be holding the request. Nothing may be assumed
	// in either direction from here, and in particular the customer must not be
	// refunded: a refund plus a payout Safaricom did accept is money gone twice,
	// and B2C payouts cannot be reversed through any API.
	PayoutUnknown = "unknown"
	// PayoutNeverReceived: established that Safaricom never had the request.
	// Distinct from PayoutUnknown deliberately — it is the only state in which a
	// refund is provably safe without Daraja having said anything about the
	// payment, because there is nothing queued that could pay.
	PayoutNeverReceived = "never_received"
	// PayoutCompleted: result code 0. The customer has the money.
	PayoutCompleted = "completed"
	// PayoutFailed: refused at the door, or a terminal failure code.
	PayoutFailed = "failed"
)

// PayoutIsOpen reports whether a payout's outcome is still undecided, which is
// what the reconciliation pass and the operator view both key on.
func PayoutIsOpen(status string) bool {
	return status == PayoutPending || status == PayoutSent || status == PayoutUnknown
}

// MpesaPayout is one outbound payout request.
type MpesaPayout struct {
	ID            uuid.UUID `json:"id"`
	TransactionID uuid.UUID `json:"transaction_id"`

	OriginatorConversationID string  `json:"originator_conversation_id"`
	ConversationID           *string `json:"conversation_id"`

	PhoneNumber string          `json:"phone_number"`
	Amount      decimal.Decimal `json:"amount"`
	Status      string          `json:"status"`

	RequestedAt *time.Time `json:"requested_at"`
	ResultCode  *string    `json:"result_code"`
	ResultDesc  *string    `json:"result_desc"`
	Receipt     *string    `json:"receipt"`
	SettledBy   *string    `json:"settled_by"`

	ReconcileAttempts int        `json:"reconcile_attempts"`
	NextQueryAt       *time.Time `json:"next_query_at"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Symbol struct {
	ID          uuid.UUID `json:"id"`
	Code        string    `json:"code"`
	DisplayName string    `json:"display_name"`
	Category    string    `json:"category"`
	PipSize     int       `json:"pip_size"`
	IsActive    bool      `json:"is_active"`
}

type Trade struct {
	ID         uuid.UUID        `json:"id"`
	AccountID  uuid.UUID        `json:"account_id"`
	SymbolID   uuid.UUID        `json:"symbol_id"`
	Direction  string           `json:"direction"`
	EntryPrice decimal.Decimal  `json:"entry_price"`
	ExitPrice  *decimal.Decimal `json:"exit_price"`
	Stake      decimal.Decimal  `json:"stake"`
	StopLoss   *decimal.Decimal `json:"stop_loss"`
	TakeProfit *decimal.Decimal `json:"take_profit"`
	Status     string           `json:"status"`
	PnL        *decimal.Decimal `json:"pnl"`
	OpenedAt   time.Time        `json:"opened_at"`
	ClosedAt   *time.Time       `json:"closed_at"`

	// Denormalised for the UI so it never has to join symbols client-side.
	SymbolCode string `json:"symbol_code"`
	SymbolName string `json:"symbol_name"`
	PipSize    int    `json:"pip_size"`
}

type Signal struct {
	ID         uuid.UUID        `json:"id"`
	SymbolID   uuid.UUID        `json:"symbol_id"`
	Direction  string           `json:"direction"`
	Confidence decimal.Decimal  `json:"confidence"`
	EntryPrice decimal.Decimal  `json:"entry_price"`
	StopLoss   *decimal.Decimal `json:"stop_loss"`
	TakeProfit *decimal.Decimal `json:"take_profit"`
	Strategy   string           `json:"strategy"`
	CreatedAt  time.Time        `json:"created_at"`

	SymbolCode string `json:"symbol_code"`
	SymbolName string `json:"symbol_name"`
	PipSize    int    `json:"pip_size"`
}

// Tick is one price update fanned out over the price WebSocket.
type Tick struct {
	Symbol string          `json:"symbol"`
	Price  decimal.Decimal `json:"price"`
	Epoch  int64           `json:"epoch"`
}

// --- Admin -----------------------------------------------------------------

const (
	RoleAdmin   = "admin"
	RoleSupport = "support"
)

const (
	AccountActive = "active"
	AccountFrozen = "frozen"
)

const (
	ActionFreeze     = "freeze_account"
	ActionUnfreeze   = "unfreeze_account"
	ActionAdjustment = "manual_adjustment"
	ActionKYCApprove = "kyc_approve"
	ActionKYCReject  = "kyc_reject"
	// ActionKYCDocumentView records that an operator looked at someone's identity
	// document. Viewing is not a change, but it is access to the most sensitive
	// data this system holds, and an approval trail that cannot say who looked is
	// only half a trail.
	ActionKYCDocumentView = "kyc_document_view"
	// ActionBotKill is the automated-trading kill switch: it stops every live run
	// and turns the feature off, so it is the most consequential single button in
	// the console and is audited like any other money action.
	ActionBotKill = "bot_kill"
	// The way back from the kill switch. Its own action, because an audit log
	// that recorded the stop and not the restart would describe an outage that
	// never ended.
	ActionBotEnable = "bot_enable"
	// Stopping one run is its own action, not a smaller bot_kill: the kill
	// switch also turns the feature off for everyone, and an audit log that
	// filed both under one name could not tell the two apart.
	ActionBotRunStop = "bot_run_stop"
	// ActionDepositResolve settles a pending deposit from the console — the
	// original ledger row, through the same path a callback takes, so a late
	// callback afterwards credits nothing.
	ActionDepositResolve = "deposit_resolve"
	// ActionPayoutResolve settles a stuck withdrawal's payout from the console —
	// the original row, through the same settle every other path uses, so a
	// result arriving afterwards credits or refunds nothing.
	ActionPayoutResolve = "payout_resolve"
)

const (
	TargetUser        = "user"
	TargetAccount     = "account"
	TargetTransaction = "transaction"
	// TargetPlatform is for actions that are not about one user's row —
	// the bot kill switch stops every live run at once.
	TargetPlatform = "platform"
	TargetBotRun   = "bot_run"
)

const (
	KYCSubmissionPending  = "pending"
	KYCSubmissionApproved = "approved"
	KYCSubmissionRejected = "rejected"
)

type AdminUser struct {
	ID                 uuid.UUID `json:"id"`
	Username           string    `json:"username"`
	PasswordHash       string    `json:"-"`
	Role               string    `json:"role"`
	MustChangePassword bool      `json:"must_change_password"`
	CreatedAt          time.Time `json:"created_at"`
}

type AuditEntry struct {
	ID         uuid.UUID      `json:"id"`
	AdminID    uuid.UUID      `json:"admin_id"`
	Action     string         `json:"action"`
	TargetType string         `json:"target_type"`
	TargetID   uuid.UUID      `json:"target_id"`
	Reason     string         `json:"reason"`
	Metadata   map[string]any `json:"metadata"`
	CreatedAt  time.Time      `json:"created_at"`

	// Denormalised so the audit table renders without a join client-side.
	AdminUsername string `json:"admin_username"`
}

type KYCSubmission struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"user_id"`
	IDNumber string    `json:"id_number"`
	// DocumentKey is a Cloudflare R2 object key, not a URL. A reader wanting to
	// show the document asks for a short-lived presigned URL; it is never stored.
	// Empty once document retention has deleted the object.
	DocumentKey  string     `json:"document_key"`
	ContentType  string     `json:"content_type"`
	SizeBytes    int64      `json:"size_bytes"`
	Status       string     `json:"status"`
	ReviewedBy   *uuid.UUID `json:"reviewed_by"`
	ReviewReason *string    `json:"review_reason"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`

	UserPhone    string  `json:"user_phone"`
	UserFullName *string `json:"user_full_name"`
	ReviewerName *string `json:"reviewer_name"`
}

// KYCUploadAttempt is a presigned upload that may or may not become a
// submission. It exists so an abandoned upload is discoverable by two parties who
// would otherwise never learn of it: the user, who is offered "continue" instead
// of "start", and the orphan sweep, which has an object to delete and a row to
// find it by.
//
// ID is minted at presign, embedded in the object key, and becomes the
// submission's own id at confirm — one identifier for the whole lifecycle.
//
// ConsumedAt is set in the same transaction that inserts the submission.
type KYCUploadAttempt struct {
	ID          uuid.UUID  `json:"id"`
	UserID      uuid.UUID  `json:"user_id"`
	ObjectKey   string     `json:"object_key"`
	ContentType string     `json:"content_type"`
	SizeBytes   int64      `json:"size_bytes"`
	CreatedAt   time.Time  `json:"created_at"`
	ConsumedAt  *time.Time `json:"consumed_at"`
}
