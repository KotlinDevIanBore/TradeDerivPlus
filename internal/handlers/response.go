package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/observability"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// APIError is the single error shape the frontend parses. `field` is set for
// validation failures so the UI can put the message under the right input;
// `code` lets the client branch without matching on English text.
type APIError struct {
	Message string `json:"message"`
	Code    string `json:"code"`
	Field   string `json:"field,omitempty"`
}

type errorEnvelope struct {
	Error APIError `json:"error"`
}

// fail maps a service error to a status and a message safe to show a user.
// Anything unrecognised becomes a generic 500 so internal detail never leaks.
func fail(c *gin.Context, err error) {
	var ve *service.ValidationError
	if errors.As(err, &ve) {
		respondErr(c, http.StatusUnprocessableEntity, ve.Message, "validation_failed", ve.Field)
		return
	}

	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		respondErr(c, http.StatusUnauthorized, err.Error(), "invalid_credentials", "")
	case errors.Is(err, service.ErrInvalidToken):
		respondErr(c, http.StatusUnauthorized, err.Error(), "invalid_token", "")
	case errors.Is(err, service.ErrPhoneTaken):
		respondErr(c, http.StatusConflict, err.Error(), "phone_taken", "phone_number")
	case errors.Is(err, service.ErrEmailTaken):
		respondErr(c, http.StatusConflict, err.Error(), "email_taken", "email")
	case errors.Is(err, service.ErrAccountExists):
		respondErr(c, http.StatusConflict, err.Error(), "account_exists", "")
	case errors.Is(err, service.ErrForbidden):
		respondErr(c, http.StatusForbidden, err.Error(), "forbidden", "")
	case errors.Is(err, service.ErrKYCRequired):
		respondErr(c, http.StatusForbidden, err.Error(), "kyc_required", "")
	case errors.Is(err, service.ErrNotFound):
		respondErr(c, http.StatusNotFound, err.Error(), "not_found", "")
	case errors.Is(err, service.ErrInsufficientFunds):
		respondErr(c, http.StatusBadRequest, err.Error(), "insufficient_funds", "amount")
	case errors.Is(err, service.ErrDemoNotAllowed):
		respondErr(c, http.StatusBadRequest, err.Error(), "demo_not_allowed", "")
	case errors.Is(err, service.ErrRealOnly):
		respondErr(c, http.StatusBadRequest, err.Error(), "real_account_required", "")
	case errors.Is(err, service.ErrNoPrice):
		respondErr(c, http.StatusServiceUnavailable, err.Error(), "no_price", "")
	case errors.Is(err, service.ErrMarketClosed):
		respondErr(c, http.StatusBadRequest, err.Error(), "market_closed", "")
	case errors.Is(err, service.ErrTradeNotOpen):
		respondErr(c, http.StatusConflict, err.Error(), "trade_not_open", "")
	case errors.Is(err, service.ErrDigitNotOpen):
		respondErr(c, http.StatusConflict, err.Error(), "digit_not_open", "")
	case errors.Is(err, service.ErrKYCStorageUnavailable):
		// 503, not 500: the deployment has no object store configured, which is a
		// missing capability rather than a fault, and a client may retry later.
		respondErr(c, http.StatusServiceUnavailable, err.Error(), "kyc_storage_unavailable", "")
	case errors.Is(err, service.ErrKYCReviewPending):
		respondErr(c, http.StatusConflict, err.Error(), "kyc_review_pending", "")
	case errors.Is(err, service.ErrKYCAlreadyVerified):
		respondErr(c, http.StatusConflict, err.Error(), "kyc_already_verified", "")
	case errors.Is(err, service.ErrKYCUploadNotFound):
		respondErr(c, http.StatusNotFound, err.Error(), "kyc_upload_not_found", "")
	case errors.Is(err, service.ErrKYCDocumentMissing):
		respondErr(c, http.StatusBadRequest, err.Error(), "kyc_document_missing", "")
	case errors.Is(err, service.ErrKYCDocumentRejected):
		respondErr(c, http.StatusUnprocessableEntity, err.Error(), "kyc_document_rejected", "file")
	case errors.Is(err, service.ErrWithdrawalsDisabled):
		respondErr(c, http.StatusForbidden, err.Error(), "withdrawals_disabled", "")
	case errors.Is(err, service.ErrWithdrawalInFlight):
		respondErr(c, http.StatusConflict, err.Error(), "withdrawal_in_flight", "")
	case errors.Is(err, service.ErrPayoutSettled):
		respondErr(c, http.StatusConflict, err.Error(), "payout_settled", "")
	case errors.Is(err, service.ErrDepositSettled):
		respondErr(c, http.StatusConflict, err.Error(), "deposit_settled", "")
	case errors.Is(err, service.ErrTooManyOpen):
		respondErr(c, http.StatusConflict, err.Error(), "too_many_open", "")
	case errors.Is(err, service.ErrBotDisabled):
		respondErr(c, http.StatusForbidden, err.Error(), "bot_disabled", "")
	case errors.Is(err, service.ErrBotRealMoneyDisabled):
		respondErr(c, http.StatusForbidden, err.Error(), "bot_real_money_disabled", "")
	case errors.Is(err, service.ErrBotRunActive):
		respondErr(c, http.StatusConflict, err.Error(), "bot_run_active", "")
	case errors.Is(err, service.ErrBotCapacity):
		respondErr(c, http.StatusServiceUnavailable, err.Error(), "bot_capacity", "")
	case errors.Is(err, service.ErrBotRunNotLive):
		respondErr(c, http.StatusConflict, err.Error(), "bot_run_not_live", "")
	case errors.Is(err, service.ErrAccountFrozen):
		respondErr(c, http.StatusForbidden, err.Error(), "account_frozen", "")
	case errors.Is(err, service.ErrAdminExists):
		respondErr(c, http.StatusConflict, err.Error(), "admin_exists", "username")
	case errors.Is(err, service.ErrPasswordUnchanged):
		respondErr(c, http.StatusUnprocessableEntity, err.Error(), "password_unchanged", "new_password")
	case errors.Is(err, service.ErrMustChangePassword):
		respondErr(c, http.StatusForbidden, err.Error(), "password_change_required", "")
	case errors.Is(err, service.ErrAlreadyReviewed):
		respondErr(c, http.StatusConflict, err.Error(), "already_reviewed", "")
	default:
		// Anything not in the sentinel set is a defect, not a rule the user
		// broke. It is reported as a bug and its detail never reaches the
		// response body.
		_ = c.Error(err) // picked up by the logging middleware
		observability.CaptureBug(err, routeOf(c), middleware.UserID(c).String())
		respondErr(c, http.StatusInternalServerError, "Something went wrong on our side", "internal", "")
	}
}

func routeOf(c *gin.Context) string {
	if route := c.FullPath(); route != "" {
		return route
	}
	return c.Request.URL.Path
}

func respondErr(c *gin.Context, status int, msg, code, field string) {
	c.AbortWithStatusJSON(status, errorEnvelope{Error: APIError{Message: msg, Code: code, Field: field}})
}

func badRequest(c *gin.Context, msg, field string) {
	respondErr(c, http.StatusBadRequest, msg, "bad_request", field)
}

// pageCount is the number of pages a total spans at a given page size, so the
// client can render "Page 2 of 7" without arithmetic of its own.
func pageCount(total, limit int) int {
	if limit <= 0 || total <= 0 {
		return 0
	}
	return (total + limit - 1) / limit
}
