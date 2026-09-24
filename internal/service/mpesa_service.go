package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
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
	SettledByQuery    = "query"
)

// What the reconciliation pass found for one row; the label on
// mpesa_reconcile_total. One of these per row examined, exactly.
const (
	ReconcileSucceeded      = "succeeded"       // the pass settled the deposit
	ReconcileUnresolved     = "unresolved"      // Daraja: still processing; backoff advanced
	ReconcileFailedIgnored  = "failed_ignored"  // Daraja: a terminal failure; NOT written (D2); backoff advanced
	ReconcileError          = "error"           // Daraja said nothing, or settle failed; not an attempt
	ReconcileGaveUp         = "gave_up"         // past the horizon; taken off the schedule for an operator
	ReconcileAlreadySettled = "already_settled" // Daraja said paid, but a callback got there during the query
)

// reconcileBackoff is how long the pass waits after the n-th answered query
// that did not resolve a deposit. PROVISIONAL, like the rest of the schedule
// (plan §2.5): the shape — widen, then hold — stands; the numbers wait on
// scripts/daraja_probe.py. The last entry repeats.
var reconcileBackoff = []time.Duration{3 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour}

// backoffAfter returns the wait once attempts answered queries have been made.
func backoffAfter(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(reconcileBackoff) {
		attempts = len(reconcileBackoff)
	}
	return reconcileBackoff[attempts-1]
}

// reconcileBatch bounds one pass. At one query per row, a full batch is a
// minute or two of Daraja round trips, which is the pass interval.
const reconcileBatch = 100

// reconcileQueryTimeout bounds one query so a hung Daraja cannot hold a pass —
// and the rows behind it — for the client's full 30s per row.
const reconcileQueryTimeout = 15 * time.Second

// stkQuerier is the one thing the reconciliation pass needs from Daraja. The
// real client satisfies it directly; mock mode gets a stub that never resolves
// anything, so the pass runs everywhere and settles only where it is real.
type stkQuerier interface {
	STKQuery(ctx context.Context, checkoutID string) (mpesaclient.QueryOutcome, error)
}

// unresolvedQuerier is mock mode's Daraja: every deposit is still processing.
// Settlement in mock mode belongs to simulateCallback, and this stub cannot
// take it — an Unresolved outcome advances the backoff and nothing else. What
// mock mode exercises is the listing, the backoff and MarkQueried, the parts of
// the pass most likely to rot unseen.
type unresolvedQuerier struct{}

func (unresolvedQuerier) STKQuery(context.Context, string) (mpesaclient.QueryOutcome, error) {
	return mpesaclient.QueryOutcome{
		Status:     mpesaclient.QueryUnresolved,
		ResultDesc: "mock: the callback is on its way",
	}, nil
}

// MpesaService owns the STK push lifecycle and the mpesa_requests table.
// It knows nothing about wallets; it reports outcomes through OnResult.
type MpesaService struct {
	store   *repository.Store
	client  *mpesaclient.Client
	querier stkQuerier
	cfg     *config.Config
	log     *slog.Logger

	onResult    func(ctx context.Context, r DepositResult)
	onReconcile func(outcome string)

	// passMu keeps ticker passes from stacking: a pass slowed by Daraja is
	// skipped over, not run alongside. Reconcile itself does not take it, so
	// tests can drive concurrent passes on purpose.
	passMu sync.Mutex

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

// SetQuerierForTest replaces what the reconciliation pass asks. Tests script
// it per checkout id; production never calls this.
func (s *MpesaService) SetQuerierForTest(q stkQuerier) { s.querier = q }

func NewMpesaService(store *repository.Store, cfg *config.Config, log *slog.Logger) *MpesaService {
	s := &MpesaService{store: store, cfg: cfg, log: log, querier: unresolvedQuerier{}}
	if !cfg.UseMockMpesa {
		s.client = mpesaclient.New(mpesaclient.Options{
			BaseURL:        cfg.MpesaBaseURL,
			ConsumerKey:    cfg.MpesaConsumerKey,
			ConsumerSecret: cfg.MpesaConsumerSecret,
			Shortcode:      cfg.MpesaShortcode,
			Passkey:        cfg.MpesaPasskey,
			CallbackURL:    cfg.MpesaCallbackURL,
		})
		s.querier = s.client
	}
	return s
}

// OnResult registers the settlement handler. The wallet service supplies it.
func (s *MpesaService) OnResult(fn func(ctx context.Context, r DepositResult)) {
	s.onResult = fn
}

// OnReconcile registers the metrics hook, called once per row the pass examines.
func (s *MpesaService) OnReconcile(fn func(outcome string)) { s.onReconcile = fn }

func (s *MpesaService) recordReconcile(outcome string) {
	if s.onReconcile != nil {
		s.onReconcile(outcome)
	}
}

// Start runs the reconciliation pass: once now, for whatever a restart
// stranded, then on every tick until ctx is cancelled. In the shape of
// DigitService.Start, with one difference — the boot pass is in the goroutine
// too, because a pass talks to Daraja and a boot that waits on a hundred
// network round trips is a boot that fails its health check.
//
// Must be called after the wallet service has registered OnResult. A pass
// before that refuses to settle (see settle), which is safe but pointless.
func (s *MpesaService) Start(ctx context.Context) {
	go func() {
		s.tick(ctx)
		t := time.NewTicker(s.cfg.MpesaReconcileEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *MpesaService) tick(ctx context.Context) {
	if !s.passMu.TryLock() {
		s.log.Warn("m-pesa reconcile: previous pass still running, skipping this tick")
		return
	}
	defer s.passMu.Unlock()
	s.Reconcile(ctx)
}

// Reconcile runs one pass over the pending deposits that are due a look.
//
// For each: past the give-up horizon, it is taken off the schedule and left
// for an operator. Otherwise Daraja is asked, and only a plain "the customer
// paid" settles anything — through settle, the same function a callback uses,
// so the guards and the credit are the ones every path shares. A terminal
// failure from a query is NOT written (plan D2): wrong-Failed costs a paying
// customer, so the row waits for the callback or the horizon. "Still
// processing" and "failed" both advance the backoff. An error is not an
// attempt: Daraja said nothing, so the row is left exactly as it was, due, and
// the next pass asks again.
//
// Exported so tests drive it directly; production calls it from Start.
func (s *MpesaService) Reconcile(ctx context.Context) {
	now := time.Now()
	rows, err := s.store.Mpesa.ListPending(ctx, now, reconcileBatch)
	if err != nil {
		s.log.Warn("m-pesa reconcile: list pending", "err", err)
		return
	}
	for i := range rows {
		if ctx.Err() != nil {
			return
		}
		s.reconcileOne(ctx, &rows[i], now)
	}
}

func (s *MpesaService) reconcileOne(ctx context.Context, row *models.MpesaRequest, now time.Time) {
	age := now.Sub(row.CreatedAt)
	if age > s.cfg.MpesaReconcileGiveUp {
		if err := s.store.Mpesa.MarkGivenUp(ctx, row.CheckoutRequestID); err != nil {
			s.log.Warn("m-pesa reconcile: mark given up", "checkout_id", row.CheckoutRequestID, "err", err)
			s.recordReconcile(ReconcileError)
			return
		}
		s.log.Warn("m-pesa reconcile: giving up on a pending deposit — needs an operator",
			"checkout_id", row.CheckoutRequestID, "transaction", row.TransactionID,
			"age", age.Round(time.Minute), "attempts", row.ReconcileAttempts)
		s.recordReconcile(ReconcileGaveUp)
		return
	}

	qctx, cancel := context.WithTimeout(ctx, reconcileQueryTimeout)
	out, err := s.querier.STKQuery(qctx, row.CheckoutRequestID)
	cancel()
	if err != nil {
		// Not an attempt. The row keeps its schedule, so it is still due and the
		// next pass asks again; a Daraja outage must not walk rows toward the
		// horizon.
		s.log.Warn("m-pesa reconcile: query failed", "checkout_id", row.CheckoutRequestID, "err", err)
		s.recordReconcile(ReconcileError)
		return
	}

	switch out.Status {
	case mpesaclient.QuerySucceeded:
		won, err := s.settle(ctx, row.CheckoutRequestID, 0, out.ResultDesc, "", SettledByQuery)
		if err != nil {
			s.log.Error("m-pesa reconcile: settle", "checkout_id", row.CheckoutRequestID, "err", err)
			s.recordReconcile(ReconcileError)
			return
		}
		if !won {
			// A callback landed between our read and our write. It credited; we
			// did not. That is the guard doing its job, not a failure.
			s.recordReconcile(ReconcileAlreadySettled)
			return
		}
		s.log.Info("m-pesa reconcile: settled a deposit by query",
			"checkout_id", row.CheckoutRequestID, "transaction", row.TransactionID, "attempts", row.ReconcileAttempts)
		s.recordReconcile(ReconcileSucceeded)

	case mpesaclient.QueryFailed:
		// Daraja names a terminal failure and the pass does not write it. The
		// callback may still say otherwise; if it never comes, the horizon does.
		s.log.Info("m-pesa reconcile: daraja reports a failure; leaving the row for the callback",
			"checkout_id", row.CheckoutRequestID, "result_code", out.ResultCode, "result_desc", out.ResultDesc)
		s.advance(ctx, row, now, ReconcileFailedIgnored)

	default:
		s.advance(ctx, row, now, ReconcileUnresolved)
	}
}

// advance records an answered-but-unresolved query and schedules the next.
func (s *MpesaService) advance(ctx context.Context, row *models.MpesaRequest, now time.Time, outcome string) {
	attempts := row.ReconcileAttempts + 1
	if err := s.store.Mpesa.MarkQueried(ctx, row.CheckoutRequestID, attempts, now.Add(backoffAfter(attempts))); err != nil {
		s.log.Warn("m-pesa reconcile: mark queried", "checkout_id", row.CheckoutRequestID, "err", err)
		s.recordReconcile(ReconcileError)
		return
	}
	s.recordReconcile(outcome)
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

	// The first query is scheduled at insert, min-age out, so the row is never
	// eligible before the callback has had its chance. Nil here would mean the
	// pass never looks at it.
	nextQuery := time.Now().Add(s.cfg.MpesaReconcileMinAge)
	req := &models.MpesaRequest{
		TransactionID:     transactionID,
		CheckoutRequestID: checkoutID,
		MerchantRequestID: merchantID,
		PhoneNumber:       phone,
		Amount:            amountKES,
		Status:            models.StatusPending,
		NextQueryAt:       &nextQuery,
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
