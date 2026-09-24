package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// DepositResult is what the M-Pesa service hands back to whoever registered
// OnResult. It carries no Daraja vocabulary beyond the human-readable reason.
type DepositResult struct {
	TransactionID uuid.UUID
	Success       bool
	Receipt       string
	Reason        string
	// Source is who established the outcome: "callback", or later "query" and
	// "admin". The ledger records it, because a deposit settled without a
	// receipt should say how rather than look like a callback that lost one.
	Source string
}

// Settlement sources, as written to transactions.metadata.settled_by.
const (
	SettledByCallback = "callback"
)

// MpesaService owns the STK push lifecycle and the mpesa_requests table.
// It knows nothing about wallets; it reports outcomes through OnResult.
type MpesaService struct {
	store  *repository.Store
	client *mpesaclient.Client
	cfg    *config.Config
	log    *slog.Logger

	onResult func(ctx context.Context, r DepositResult)

	// afterInitiate runs synchronously at the end of InitiateSTK, before it
	// returns to Deposit. Tests use it to land a settlement inside the window
	// between the push being accepted and Deposit's follow-up write — the window
	// a real callback can hit and the mock, with its four-second delay, never
	// does. Nil in production.
	afterInitiate func(checkoutID string)
}

// SetAfterInitiateForTest is for tests, which need to act inside InitiateSTK's
// return path rather than after it.
func (s *MpesaService) SetAfterInitiateForTest(fn func(checkoutID string)) { s.afterInitiate = fn }

func NewMpesaService(store *repository.Store, cfg *config.Config, log *slog.Logger) *MpesaService {
	var client *mpesaclient.Client
	if !cfg.UseMockMpesa {
		client = mpesaclient.New(mpesaclient.Options{
			BaseURL:        cfg.MpesaBaseURL,
			ConsumerKey:    cfg.MpesaConsumerKey,
			ConsumerSecret: cfg.MpesaConsumerSecret,
			Shortcode:      cfg.MpesaShortcode,
			Passkey:        cfg.MpesaPasskey,
			CallbackURL:    cfg.MpesaCallbackURL,
		})
	}
	return &MpesaService{store: store, client: client, cfg: cfg, log: log}
}

// OnResult registers the settlement handler. The wallet service supplies it.
func (s *MpesaService) OnResult(fn func(ctx context.Context, r DepositResult)) {
	s.onResult = fn
}

// InitiateSTK sends the push and records the request. amountKES is what the
// user is charged; transactionID links back to the pending ledger row.
func (s *MpesaService) InitiateSTK(ctx context.Context, transactionID uuid.UUID, phone string, amountKES decimal.Decimal, accountRef string) (string, error) {
	var checkoutID, merchantID string

	if s.cfg.UseMockMpesa {
		checkoutID = "ws_CO_MOCK_" + uuid.NewString()[:18]
		merchantID = "MOCK-" + uuid.NewString()[:12]
	} else {
		resp, err := s.client.STKPush(ctx, mpesaclient.STKPushRequest{
			PhoneNumber: phone,
			Amount:      amountKES.Ceil().IntPart(),
			AccountRef:  accountRef,
			Description: "TradeDerivPlus deposit",
		})
		if err != nil {
			return "", fmt.Errorf("m-pesa did not accept the request: %w", err)
		}
		checkoutID, merchantID = resp.CheckoutRequestID, resp.MerchantRequestID
	}

	req := &models.MpesaRequest{
		TransactionID:     transactionID,
		CheckoutRequestID: checkoutID,
		MerchantRequestID: merchantID,
		PhoneNumber:       phone,
		Amount:            amountKES,
		Status:            models.StatusPending,
	}
	if err := s.store.Mpesa.Insert(ctx, req); err != nil {
		return "", err
	}

	if s.cfg.UseMockMpesa {
		go s.simulateCallback(checkoutID)
	}
	if s.afterInitiate != nil {
		s.afterInitiate(checkoutID)
	}
	return checkoutID, nil
}

// HandleCallback processes a Daraja callback. It is idempotent: a repeated
// callback for an already-settled request is acknowledged and ignored, which
// matters because Daraja retries.
//
// It is an adapter over settle, and deliberately nothing more: the callback is
// one of the ways a deposit's outcome arrives, not the definition of settling.
func (s *MpesaService) HandleCallback(ctx context.Context, cb mpesaclient.Callback) error {
	stk := cb.Body.STKCallback
	if stk.CheckoutRequestID == "" {
		return invalid("CheckoutRequestID", "missing CheckoutRequestID")
	}
	_, err := s.settle(ctx, stk.CheckoutRequestID, stk.ResultCode, stk.ResultDesc,
		cb.ReceiptNumber(), SettledByCallback)
	return err
}

// settle is the only way a deposit's outcome is written.
//
// Every source of an outcome — the callback today, a status query and the
// console later — comes through here, so the guarded write at UpdateResult and
// the credit at settleDeposit are the ones every path shares. "No second way to
// settle a deposit" is enforced by there being one function, not by convention.
//
// It reports whether this call was the one that wrote the outcome. The write
// decides, not a read: checking the row's status first and then writing would
// be two statements with a gap between them, and Daraja retries — two deliveries
// for one push can both read 'pending' and both go on to credit. The guard is
// inside the UPDATE, so exactly one caller is told it won, and only that one
// credits anything.
func (s *MpesaService) settle(ctx context.Context, checkoutID string, resultCode int, resultDesc, receipt, source string) (bool, error) {
	// Refuse, rather than win the guard with nowhere to send the result. With no
	// handler the mpesa_requests row would go terminal and the ledger would stay
	// pending for ever — every later attempt loses the guard this one consumed.
	// The window is real at boot, between this service being built and the
	// wallet service registering itself.
	if s.onResult == nil {
		return false, errors.New("m-pesa settle: no result handler registered")
	}

	req, err := s.store.Mpesa.GetByCheckoutID(ctx, checkoutID)
	if err != nil {
		return false, err
	}

	success := resultCode == 0
	status := models.StatusFailed
	if success {
		status = models.StatusCompleted
	}

	won, err := s.store.Mpesa.UpdateResult(ctx, checkoutID, status, resultCode, resultDesc)
	if err != nil {
		return false, err
	}
	if !won {
		// Usually a retry of a callback already handled. The one case that is
		// not routine: Safaricom now says the customer paid, and the row was
		// already closed as failed — by an operator, or by an earlier delivery
		// that said otherwise. Money moved and the ledger says it did not, and
		// only a person can reconcile that, so it is an error and not a log line
		// that reads like housekeeping.
		if success {
			if current, err := s.store.Mpesa.GetByCheckoutID(ctx, checkoutID); err == nil &&
				current.Status == models.StatusFailed {
				s.log.Error("m-pesa reports a paid deposit that was already recorded as failed",
					"checkout_id", checkoutID, "receipt", receipt, "source", source,
					"transaction", current.TransactionID)
				return false, nil
			}
		}
		s.log.Info("duplicate m-pesa settlement ignored", "checkout_id", checkoutID, "source", source)
		return false, nil
	}

	s.onResult(ctx, DepositResult{
		TransactionID: req.TransactionID,
		Success:       success,
		Receipt:       receipt,
		Reason:        humanResult(resultCode, resultDesc),
		Source:        source,
	})
	return true, nil
}

// simulateCallback drives the mock deposit lifecycle so the four-step deposit
// UI (send -> pending -> result) is exercisable without Daraja credentials.
// One in six attempts fails, cycling through the real-world failure reasons.
func (s *MpesaService) simulateCallback(checkoutID string) {
	delay := time.Duration(4+rand.Intn(5)) * time.Second
	time.Sleep(delay)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	code, desc := 0, "The service request is processed successfully."
	if rand.Intn(6) == 0 {
		switch rand.Intn(3) {
		case 0:
			code, desc = 1032, "Request cancelled by user"
		case 1:
			code, desc = 1, "The balance is insufficient for the transaction"
		default:
			code, desc = 1037, "DS timeout user cannot be reached"
		}
	}

	var cb mpesaclient.Callback
	cb.Body.STKCallback.CheckoutRequestID = checkoutID
	cb.Body.STKCallback.ResultCode = code
	cb.Body.STKCallback.ResultDesc = desc
	if code == 0 {
		cb.Body.STKCallback.CallbackMetadata.Item = []struct {
			Name  string `json:"Name"`
			Value any    `json:"Value"`
		}{{Name: "MpesaReceiptNumber", Value: "MOCK" + uuid.NewString()[:8]}}
	}

	if err := s.HandleCallback(ctx, cb); err != nil {
		s.log.Error("mock callback failed", "err", err)
	}
}

// humanResult maps Daraja result codes to copy the deposit screen can show
// directly, instead of leaking "DS timeout user cannot be reached" to a user.
func humanResult(code int, desc string) string {
	switch code {
	case 0:
		return "Deposit received"
	case 1:
		return "Your M-Pesa balance was too low to complete this deposit"
	case 1032:
		return "You cancelled the request"
	case 1037:
		return "We couldn't reach your phone. Check it is on and try again"
	case 1001:
		return "You have another M-Pesa transaction in progress. Wait a moment and retry"
	case 2001:
		return "The M-Pesa PIN you entered was wrong"
	default:
		if desc != "" {
			return desc
		}
		return "The deposit did not go through"
	}
}
