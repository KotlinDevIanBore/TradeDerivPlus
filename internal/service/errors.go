package service

import "errors"

// Sentinel errors the handler layer maps to HTTP statuses. Services never
// import gin, so this is the whole vocabulary they use to say what went wrong.
//
// These strings are shown to users verbatim: the API contract says `message`
// is safe to render directly, and screens like the order ticket do exactly
// that. So they are written as sentences rather than following Go's
// lowercase-error convention, which exists for strings that get wrapped into
// larger ones. Nothing here is ever wrapped.
var (
	ErrInvalidCredentials = errors.New("Phone number or password is incorrect")
	ErrPhoneTaken         = errors.New("This number is already registered")
	ErrEmailTaken         = errors.New("This email is already registered")
	ErrNotFound           = errors.New("Not found")
	ErrForbidden          = errors.New("That is not yours")
	ErrInsufficientFunds  = errors.New("Insufficient balance")
	ErrInvalidToken       = errors.New("Your session has expired")
	ErrValidation         = errors.New("Validation failed")
	ErrDemoNotAllowed     = errors.New("Not available on a demo account")
	ErrRealOnly           = errors.New("Switch to your real account to do this")
	ErrKYCRequired        = errors.New("Verify your identity first")
	ErrNoPrice            = errors.New("No live price for this symbol yet")
	ErrMarketClosed       = errors.New("This market is not available right now")
	ErrAccountExists      = errors.New("You already have an account of this type")
	ErrTradeNotOpen       = errors.New("This position is already closed")
	ErrTooManyOpen        = errors.New("Wait for your open contracts to settle before opening another")

	ErrAccountFrozen      = errors.New("This account is frozen. Contact support")
	ErrAdminExists        = errors.New("That username is taken")
	ErrPasswordUnchanged  = errors.New("Choose a password you have not used here before")
	ErrMustChangePassword = errors.New("You must change the default password before continuing")
	ErrAlreadyReviewed    = errors.New("This submission has already been reviewed")
	ErrDepositSettled     = errors.New("This deposit has already been settled")

	// Withdrawals. Disabled is a platform state, in flight is this account's.
	ErrWithdrawalsDisabled = errors.New("Withdrawals are not available yet")
	ErrWithdrawalInFlight  = errors.New("A withdrawal is already on its way. Wait for it to finish before starting another")
	ErrPayoutSettled       = errors.New("This payout has already been settled")

	// Automated runs.
	ErrBotDisabled          = errors.New("Automated trading is not available on your account yet")
	ErrBotRealMoneyDisabled = errors.New("Automated runs are available on demo accounts for now")
	ErrBotRunActive         = errors.New("A run is already live on this account")
	ErrBotCapacity          = errors.New("Too many automated runs are live right now. Try again shortly")
	// Never reaches a person: only BotService sets BotRunID, and it is what a
	// placement gets when its run was stopped while the placement was in flight.
	ErrBotRunNotLive = errors.New("This automated run has already stopped")
)

// ValidationError carries a field name so the UI can put the message under the
// right input rather than in a generic banner.
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string { return e.Message }
func (e *ValidationError) Unwrap() error { return ErrValidation }

func invalid(field, msg string) error { return &ValidationError{Field: field, Message: msg} }

// ValidationFor is the exported form, for the few validations a handler has to
// make itself because they are about the request's shape rather than the
// domain — a money field that is not a number, say. The message still reaches
// the user verbatim, so it is written as a sentence like the rest.
func ValidationFor(field, msg string) error { return invalid(field, msg) }
