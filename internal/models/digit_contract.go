package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Digit contract constants — parallel to CFD trades, not an extension of them.

const (
	DigitEvenOdd        = "even_odd"
	DigitMatchesDiffers = "matches_differs"
	DigitOverUnder      = "over_under"
)

const (
	DigitParityEven = "even"
	DigitParityOdd  = "odd"
	DigitModeMatch  = "match"
	DigitModeDiffer = "differ"
	DigitSideOver   = "over"
	DigitSideUnder  = "under"
)

const (
	DigitOpen = "open"
	DigitWon  = "won"
	DigitLost = "lost"
	DigitVoid = "void"
)

// DigitSelection is the typed JSON stored in digit_contracts.selection.
type DigitSelection struct {
	Mode    string `json:"mode,omitempty"`    // match | differ
	Digit   *int   `json:"digit,omitempty"`   // 0–9
	Parity  string `json:"parity,omitempty"`  // even | odd
	Side    string `json:"side,omitempty"`    // over | under
	Barrier *int   `json:"barrier,omitempty"` // 0–9
}

func (s DigitSelection) Marshal() ([]byte, error) { return json.Marshal(s) }

func ParseDigitSelection(raw []byte) (DigitSelection, error) {
	var s DigitSelection
	err := json.Unmarshal(raw, &s)
	return s, err
}

// DigitContract is one binary last-digit contract.
type DigitContract struct {
	ID              uuid.UUID        `json:"id"`
	AccountID       uuid.UUID        `json:"account_id"`
	SymbolID        uuid.UUID        `json:"symbol_id"`
	ContractType    string           `json:"contract_type"`
	Selection       DigitSelection   `json:"selection"`
	Stake           decimal.Decimal  `json:"stake"`
	PayoutRatio     decimal.Decimal  `json:"payout_ratio"`
	PotentialPayout decimal.Decimal  `json:"potential_payout"`
	EntryPrice      decimal.Decimal  `json:"entry_price"`
	EntryDigit      int              `json:"entry_digit"`
	EntryTickEpoch  int64            `json:"entry_tick_epoch"`
	Status          string           `json:"status"`
	SettlementPrice *decimal.Decimal `json:"settlement_price"`
	SettlementDigit *int             `json:"settlement_digit"`
	SettledAt       *time.Time       `json:"settled_at"`
	PnL             *decimal.Decimal `json:"pnl"`
	OpenedAt        time.Time        `json:"opened_at"`
	VoidAfter       time.Time        `json:"void_after"`

	// BotRunID is the run that placed this contract, or nil when a human did.
	// Without it a bot's contract is indistinguishable from a person's, which
	// makes both support and accounting guesswork.
	BotRunID *uuid.UUID `json:"bot_run_id"`

	SymbolCode string `json:"symbol_code"`
	SymbolName string `json:"symbol_name"`
	PipSize    int    `json:"pip_size"`
}

// WinCredit is the total wallet credit on a win: stake × (1 + payout_ratio).
func WinCredit(stake, payoutRatio decimal.Decimal) decimal.Decimal {
	return stake.Mul(decimal.NewFromInt(1).Add(payoutRatio)).Round(2)
}

// DigitQuote is a read-only snapshot of the static payout table for a stake.
// Same numbers Open would persist — not a second pricing path.
type DigitQuote struct {
	ContractType    string          `json:"contract_type"`
	Selection       DigitSelection  `json:"selection"`
	Stake           decimal.Decimal `json:"stake"`
	PayoutRatio     decimal.Decimal `json:"payout_ratio"`
	PotentialPayout decimal.Decimal `json:"potential_payout"`

	// The economics of this selection, served rather than computed client-side so
	// there is one answer to "what does this contract cost on average" and the UI
	// cannot drift from the payout table. Expected return is negative for every
	// selection on the current table.
	WinProbability decimal.Decimal `json:"win_probability"`
	ExpectedReturn decimal.Decimal `json:"expected_return"`
}
