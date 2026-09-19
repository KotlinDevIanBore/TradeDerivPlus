package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Enum-ish string constants. Kept as typed strings so handlers can validate cheaply.

const (
	KYCPending  = "pending"
	KYCVerified = "verified"
	KYCRejected = "rejected"
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
)

const (
	TargetUser        = "user"
	TargetAccount     = "account"
	TargetTransaction = "transaction"
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
	ID           uuid.UUID  `json:"id"`
	UserID       uuid.UUID  `json:"user_id"`
	IDNumber     string     `json:"id_number"`
	DocumentURL  string     `json:"document_url"`
	Status       string     `json:"status"`
	ReviewedBy   *uuid.UUID `json:"reviewed_by"`
	ReviewReason *string    `json:"review_reason"`
	SubmittedAt  time.Time  `json:"submitted_at"`
	ReviewedAt   *time.Time `json:"reviewed_at"`

	UserPhone    string  `json:"user_phone"`
	UserFullName *string `json:"user_full_name"`
	ReviewerName *string `json:"reviewer_name"`
}
