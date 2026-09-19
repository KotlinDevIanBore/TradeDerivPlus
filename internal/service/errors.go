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
	ErrAccountFrozen      = errors.New("This account is frozen. Contact support")
	ErrAdminExists        = errors.New("That username is taken")
	ErrPasswordUnchanged  = errors.New("Choose a password you have not used here before")
	ErrMustChangePassword = errors.New("You must change the default password before continuing")
	ErrAlreadyReviewed    = errors.New("This submission has already been reviewed")
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
