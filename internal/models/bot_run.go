package models

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Bot run status. A run is live while it is running or stopping; 'stopping'
// means a stop has been requested and one contract is still in flight, which is
// a state the database has to know about — a second run must not start into that
// gap, and the partial unique index on bot_runs enforces it.
const (
	BotRunning  = "running"
	BotStopping = "stopping"
	BotStopped  = "stopped"
)

// Why a run ended. Everything that is not a user or admin decision is either a
// limit the user asked for, a condition the platform imposed, or a fault.
const (
	// Asked for.
	BotStopUser                 = "user"
	BotStopStopLoss             = "stop_loss"
	BotStopTakeProfit           = "take_profit"
	BotStopMaxConsecutiveLosses = "max_consecutive_losses"
	BotStopMaxContracts         = "max_contracts"
	BotStopMaxDuration          = "max_duration"

	// Imposed.
	BotStopAdminKill = "admin_kill"
	// One run stopped from the console, as opposed to the platform-wide kill.
	// Separate because they mean different things to the person whose run it
	// was: automated trading is still on, and only theirs was stopped.
	BotStopAdminStop         = "admin_stop"
	BotStopDisabled          = "disabled"
	BotStopInsufficientFunds = "insufficient_funds"
	BotStopAccountFrozen     = "account_frozen"
	BotStopMarketClosed      = "market_closed"

	// Faults. `restart` and `shutdown` are the process's doing, not the run's.
	BotStopFeedDown = "feed_down"
	BotStopRestart  = "restart"
	BotStopShutdown = "shutdown"
	BotStopError    = "error"
)

// BotLimitsConfig is the stop conditions a user set when starting a run, stored
// as the run's `limits` JSONB. A nil pointer means the condition was not set;
// StopLoss is always set, because a run with no floor is not something this
// product offers.
//
// Money is a decimal rather than a float even inside JSON: these figures are
// compared against realised P/L, which is ledger money.
type BotLimitsConfig struct {
	StopLoss             decimal.Decimal  `json:"stop_loss"`
	TakeProfit           *decimal.Decimal `json:"take_profit,omitempty"`
	MaxConsecutiveLosses *int             `json:"max_consecutive_losses,omitempty"`
	MaxContracts         *int             `json:"max_contracts,omitempty"`
	MaxDurationSeconds   *int             `json:"max_duration_s,omitempty"`
}

// BotRun is one durable AUTO session on one account.
//
// The counters are maintained inside the same transactions that move the money —
// Open increments ContractsPlaced, settle and void write the outcome — so they
// cannot disagree with the contracts they summarise, and a contract that settles
// after its run has stopped still lands on the right row.
type BotRun struct {
	ID        uuid.UUID `json:"id"`
	AccountID uuid.UUID `json:"account_id"`
	SymbolID  uuid.UUID `json:"symbol_id"`

	Status     string  `json:"status"`
	StopReason *string `json:"stop_reason"`
	LastError  *string `json:"last_error"`

	ContractType string          `json:"contract_type"`
	Selection    DigitSelection  `json:"selection"`
	Stake        decimal.Decimal `json:"stake"`
	IntervalMS   int             `json:"interval_ms"`
	Limits       BotLimitsConfig `json:"limits"`

	StartingBalance decimal.Decimal `json:"starting_balance"`

	ContractsPlaced   int             `json:"contracts_placed"`
	Wins              int             `json:"wins"`
	Losses            int             `json:"losses"`
	Voids             int             `json:"voids"`
	ConsecutiveLosses int             `json:"consecutive_losses"`
	ConsecutiveVoids  int             `json:"consecutive_voids"`
	RealisedPnL       decimal.Decimal `json:"realised_pnl"`

	StartedAt time.Time  `json:"started_at"`
	StoppedAt *time.Time `json:"stopped_at"`
	UpdatedAt time.Time  `json:"updated_at"`

	// Denormalised from symbols so a run renders without a second query, the
	// same way a digit contract carries its symbol.
	SymbolCode string `json:"symbol_code"`
	SymbolName string `json:"symbol_name"`
}

// IsLive reports whether the run still owns the account's one live-run slot.
func (r *BotRun) IsLive() bool {
	return r.Status == BotRunning || r.Status == BotStopping
}
