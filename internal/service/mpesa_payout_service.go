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

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// MpesaPayoutService owns the outbound half of M-Pesa: it sends B2C requests and
// turns Daraja's callbacks into outcomes. A sibling of MpesaService rather than a
// parameter on it — the two speak different APIs, correlate on different ids and
// have opposite consequences when they are wrong — but the same shape, and
// deliberately so: one function writes every outcome, the guard inside the UPDATE
// decides who won, and the money itself moves in a handler registered by the
// wallet service, which this file knows nothing about.

// PayoutSettlement is what this service hands to whoever registered OnResult.
type PayoutSettlement struct {
	TransactionID uuid.UUID
	// Paid is true only when Daraja said the customer has the money.
	Paid bool
	// Receipt is the M-PESA transaction id, when there is one. Never synthesised.
	Receipt string
	// Reason is customer-facing copy, already translated out of Daraja's
	// vocabulary by humanPayoutResult.
	Reason string
	// Source is how the outcome was established.
	Source string
	// AdminID is set only when an operator established it.
	AdminID *uuid.UUID
}

// How a payout outcome was established, as written to mpesa_payouts.settled_by
// and the ledger row's metadata.
const (
	PayoutSettledByCallback = "callback"
	PayoutSettledByQuery    = "query"
	PayoutSettledByAdmin    = "admin"
	// PayoutSettledByRejection: Daraja refused the request at the door. Distinct
	// from a callback failure because no payout was ever queued — which is what
	// makes refunding safe without asking anything further.
	PayoutSettledByRejection = "rejection"
)

// payoutSender is the one thing this service needs from Daraja. The real client
// satisfies it; mock mode gets a simulator. Same seam as MpesaService's querier.
type payoutSender interface {
	SendB2C(ctx context.Context, r mpesaclient.B2CRequest) (*mpesaclient.B2CAccepted, error)
}

// payoutQuerier is the status query, for the reconciliation pass that arrives in
// the next checkpoint. Declared here so the seam exists before the pass does.
type payoutQuerier interface {
	QueryTransactionStatus(ctx context.Context, r mpesaclient.StatusRequest) (*mpesaclient.StatusAccepted, error)
}

// firstQueryDelay is how long after a send the reconciler may first ask about a
// payout. PROVISIONAL, like the deposit schedule: long enough that the ordinary
// callback has had its chance, short enough that an ambiguous send is not left
// all day.
const firstQueryDelay = 3 * time.Minute

// payoutBackoff is how long to wait after the n-th status query that did not
// resolve a payout. PROVISIONAL; the shape — widen, then hold — is not.
//
// Longer at the top than the deposit ladder because each rung here costs two
// round trips rather than one: the query is asynchronous, so asking again before
// the previous answer has had time to arrive asks about something already in
// flight.
var payoutBackoff = []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour}

func payoutBackoffAfter(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	if attempts > len(payoutBackoff) {
		attempts = len(payoutBackoff)
	}
	return payoutBackoff[attempts-1]
}

// payoutBatch bounds one pass.
const payoutBatch = 100

// payoutQueryTimeout bounds one status request so a hung Daraja cannot hold a
// pass, and the payouts behind it, for the client's full timeout per row.
const payoutQueryTimeout = 15 * time.Second

// What the pass did with one payout; the label on mpesa_payout_queries_total.
const (
	PayoutQueryAsked          = "queried"         // a status query was issued and the schedule advanced
	PayoutQueryGaveUp         = "gave_up"         // past the horizon; taken off the schedule for an operator
	PayoutQueryError          = "error"           // we could not ask; NOT an attempt, the row keeps its schedule
	PayoutQueryAlreadySettled = "already_settled" // settled between the pass reading it and writing to it
)

type MpesaPayoutService struct {
	store  *repository.Store
	client *mpesaclient.Client
	sender payoutSender
	cfg    *config.Config
	log    *slog.Logger

	onResult  func(ctx context.Context, s PayoutSettlement)
	onSettled func(outcome string)
	onQueried func(outcome string)
	onFloat   func(availableKES string)

	// querier is the status query. Nil in mock mode, where nothing is asked: the
	// simulator answers on its own and a pass with nothing to ask would only
	// advance backoffs. The pass still runs, so the listing, the schedule and the
	// give-up horizon are exercised continuously.
	querier payoutQuerier

	// passMu keeps ticker passes from stacking. Reconcile itself does not take it,
	// so tests can run passes concurrently on purpose.
	passMu sync.Mutex

	// mockDelay is how long the simulator waits before reporting a result.
	mockDelay time.Duration
}

func NewMpesaPayoutService(store *repository.Store, cfg *config.Config, log *slog.Logger) *MpesaPayoutService {
	s := &MpesaPayoutService{store: store, cfg: cfg, log: log, mockDelay: 3 * time.Second}
	if cfg.UseMockMpesa {
		s.sender = &mockPayoutSender{svc: s}
	} else {
		s.client = mpesaclient.New(mpesaclient.Options{
			BaseURL:            cfg.MpesaBaseURL,
			ConsumerKey:        cfg.MpesaConsumerKey,
			ConsumerSecret:     cfg.MpesaConsumerSecret,
			Shortcode:          cfg.MpesaShortcode,
			Passkey:            cfg.MpesaPasskey,
			CallbackURL:        cfg.MpesaCallbackURL,
			InitiatorName:      cfg.MpesaInitiatorName,
			SecurityCredential: cfg.MpesaSecurityCredential,
			B2CResultURL:       cfg.MpesaB2CResultURL,
			B2CTimeoutURL:      cfg.MpesaB2CTimeoutURL,
			B2CStatusResultURL: cfg.MpesaB2CStatusResultURL,
		})
		s.sender = s.client
		s.querier = s.client
	}
	return s
}

// OnResult registers the settlement handler. The wallet service supplies it, and
// it is the only thing that moves money.
func (s *MpesaPayoutService) OnResult(fn func(ctx context.Context, st PayoutSettlement)) {
	s.onResult = fn
}

// SetSenderForTest replaces what a payout is sent through.
func (s *MpesaPayoutService) SetSenderForTest(p payoutSender) { s.sender = p }

// SetQuerierForTest replaces what the pass asks. Production wires the client.
func (s *MpesaPayoutService) SetQuerierForTest(q payoutQuerier) { s.querier = q }

// OnSettled, OnQueried and OnFloat are the metrics hooks, so this service stays
// unaware of Prometheus. main wires them.
func (s *MpesaPayoutService) OnSettled(fn func(outcome string))    { s.onSettled = fn }
func (s *MpesaPayoutService) OnQueried(fn func(outcome string))    { s.onQueried = fn }
func (s *MpesaPayoutService) OnFloat(fn func(availableKES string)) { s.onFloat = fn }

func (s *MpesaPayoutService) recordSettled(outcome string) {
	if s.onSettled != nil {
		s.onSettled(outcome)
	}
}

func (s *MpesaPayoutService) recordQueried(outcome string) {
	if s.onQueried != nil {
		s.onQueried(outcome)
	}
}

// Start runs the reconciliation pass: once at boot, for whatever a restart left
// open, then on every tick until ctx is cancelled.
//
// The boot pass is inside the goroutine, as the deposit pass learned to do: a
// pass issues network requests and a boot that waits on a hundred of them is a
// boot that fails its health check.
//
// Must be called after the wallet service has registered OnResult. A pass before
// that refuses to settle, which is safe but pointless.
func (s *MpesaPayoutService) Start(ctx context.Context) {
	go func() {
		s.tick(ctx)
		t := time.NewTicker(s.cfg.MpesaPayoutQueryEvery)
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

func (s *MpesaPayoutService) tick(ctx context.Context) {
	if !s.passMu.TryLock() {
		s.log.Warn("payout reconcile: previous pass still running, skipping this tick")
		return
	}
	defer s.passMu.Unlock()
	s.Reconcile(ctx)
}

// Reconcile runs one pass over the payouts whose outcome is still open and whose
// next look has come.
//
// It asks; it does not conclude, and it never resends. Asking is all it can do:
// Daraja's Transaction Status API is asynchronous, so the answer arrives later on
// the status channel and HandleStatusResult classifies it there. And it does not
// resend because whether Safaricom deduplicates a repeated request id is unproven
// — there is deliberately no code here that could.
//
// Past the give-up horizon a payout is taken off the schedule and left for a
// person. That is NOT a refund: the customer's money is already out and nothing
// has been learned, so refunding would be the same guess 'unknown' exists to
// refuse.
//
// Exported so tests drive it directly; production calls it from Start.
func (s *MpesaPayoutService) Reconcile(ctx context.Context) {
	now := time.Now()
	rows, err := s.store.Payouts.ListDue(ctx, now, payoutBatch)
	if err != nil {
		s.log.Warn("payout reconcile: list due", "err", err)
		return
	}
	for i := range rows {
		if ctx.Err() != nil {
			return
		}
		s.reconcileOne(ctx, &rows[i], now)
	}
}

func (s *MpesaPayoutService) reconcileOne(ctx context.Context, row *models.MpesaPayout, now time.Time) {
	age := now.Sub(row.CreatedAt)
	if age > s.cfg.MpesaPayoutGiveUp {
		reason := "gave up asking after " + age.Round(time.Minute).String() + "; needs an operator"
		won, err := s.store.Payouts.MarkGaveUp(ctx, row.OriginatorConversationID, reason)
		if err != nil {
			s.log.Warn("payout reconcile: mark gave up", "originator", row.OriginatorConversationID, "err", err)
			s.recordQueried(PayoutQueryError)
			return
		}
		if !won {
			s.recordQueried(PayoutQueryAlreadySettled)
			return
		}
		// Loud, because the customer's money is out and nobody automated is
		// looking any more.
		s.log.Error("payout reconcile: GIVING UP; a debited customer has no answer and no process left",
			"originator", row.OriginatorConversationID, "transaction", row.TransactionID,
			"status", row.Status, "age", age.Round(time.Minute), "attempts", row.ReconcileAttempts)
		s.recordQueried(PayoutQueryGaveUp)
		return
	}

	if s.querier == nil {
		// Mock mode. Nothing to ask, so the schedule advances and the listing,
		// the backoff and the horizon stay exercised.
		s.advanceQuery(ctx, row, now)
		return
	}

	qctx, cancel := context.WithTimeout(ctx, payoutQueryTimeout)
	_, err := s.querier.QueryTransactionStatus(qctx, mpesaclient.StatusRequest{
		OriginatorConversationID: row.OriginatorConversationID,
		Remarks:                  "withdrawal status check",
	})
	cancel()
	if err != nil {
		// We could not ask. Not an attempt: the row keeps its schedule, so it is
		// still due and the next pass asks again. A Daraja outage must not walk
		// payouts toward the horizon while nobody is able to look at them.
		s.log.Warn("payout reconcile: could not ask", "originator", row.OriginatorConversationID, "err", err)
		s.recordQueried(PayoutQueryError)
		return
	}
	s.advanceQuery(ctx, row, now)
}

// advanceQuery records that a question was asked and when to ask again. The
// answer, if one comes, arrives on the status channel.
func (s *MpesaPayoutService) advanceQuery(ctx context.Context, row *models.MpesaPayout, now time.Time) {
	attempts := row.ReconcileAttempts + 1
	won, err := s.store.Payouts.MarkQueried(ctx, row.OriginatorConversationID,
		attempts, now.Add(payoutBackoffAfter(attempts)))
	if err != nil {
		s.log.Warn("payout reconcile: mark queried", "originator", row.OriginatorConversationID, "err", err)
		s.recordQueried(PayoutQueryError)
		return
	}
	if !won {
		// Settled between the read and this write. The question may still be in
		// flight; its answer will lose the settle guard, which is correct.
		s.recordQueried(PayoutQueryAlreadySettled)
		return
	}
	s.recordQueried(PayoutQueryAsked)
}

// HandleStatusResult is the status channel's handler: Daraja's answer to a
// question the pass asked.
//
// Its own route, because a Transaction Status result is a different document from
// a payout result and the rule that separates them matters: a non-zero result
// code HERE means the QUERY failed, not the payout. Nothing about the customer's
// money follows from a failed question.
func (s *MpesaPayoutService) HandleStatusResult(ctx context.Context, body []byte, arrival mpesaclient.PayoutArrival) error {
	result, err := mpesaclient.ParseStatusCallback(body, arrival)
	if err != nil {
		return err
	}

	originator := result.OriginatorConversationID
	if originator == "" {
		row, err := s.store.Payouts.GetByConversationID(ctx, result.ConversationID)
		if err != nil {
			return fmt.Errorf("status result: no payout for conversation %q: %w", result.ConversationID, err)
		}
		originator = row.OriginatorConversationID
	}

	switch result.Status {
	case mpesaclient.PayoutSucceeded:
		s.log.Info("payout status query: daraja reports the customer was paid",
			"originator", originator, "receipt", result.Receipt)
		_, err := s.settle(ctx, originator, models.PayoutCompleted, result.ResultCode,
			result.ResultDesc, result.Receipt, PayoutSettledByQuery, nil)
		return err

	case mpesaclient.PayoutFailed:
		s.log.Info("payout status query: daraja reports the payout did not happen",
			"originator", originator, "result_desc", result.ResultDesc)
		_, err := s.settle(ctx, originator, models.PayoutFailed, result.ResultCode,
			result.ResultDesc, "", PayoutSettledByQuery, nil)
		return err

	default:
		// The query answered and did not resolve it — including the case where the
		// QUERY itself failed. Either way nothing is concluded; the schedule the
		// pass already set stands.
		s.log.Info("payout status query: still unresolved",
			"originator", originator, "result_code", result.ResultCode, "arrival_timeout", arrival == mpesaclient.ArrivedAtTimeoutURL)
		return nil
	}
}

// SetMockDelayForTest shortens the simulated callback so a test need not wait.
func (s *MpesaPayoutService) SetMockDelayForTest(d time.Duration) { s.mockDelay = d }

// ValidateConfig reports whether real payouts can be sent. Called at boot so a
// collapsed or incomplete payout configuration is a failed start rather than a
// failed withdrawal — in particular a result URL equal to the timeout URL, which
// would make an ambiguous answer indistinguishable from a failure.
func (s *MpesaPayoutService) ValidateConfig() error {
	if s.cfg.UseMockMpesa {
		return nil
	}
	if s.client == nil {
		return errors.New("payouts: no Daraja client")
	}
	return s.client.ValidateB2CConfig()
}

// NewOriginatorConversationID mints the id a payout is tracked by. Generated
// here, but persisted by the caller before anything is sent: the whole value of
// the id is that it survives the send.
func NewOriginatorConversationID() string {
	return "wd-" + uuid.NewString()
}

// Send reserves nothing and validates nothing about the withdrawal — the caller
// has already committed the debit, the ledger row and the payout row. It takes
// the one-send guard, dispatches, and records which of the three answers came
// back.
//
// It never returns an error the caller is expected to act on: every outcome is
// recorded on the payout row, and a failure that owes the customer a refund goes
// through settle like every other outcome. What the caller gets back is whether
// the money is still out.
func (s *MpesaPayoutService) Send(ctx context.Context, p *models.MpesaPayout) {
	// One attempt per payout, decided by the database. A caller that loses here
	// must not send: something else already has, and two requests for one
	// withdrawal is two real payouts that no API can reverse.
	won, err := s.store.Payouts.MarkRequested(ctx, p.OriginatorConversationID, time.Now())
	if err != nil {
		s.log.Error("payout: could not take the send guard", "originator", p.OriginatorConversationID, "err", err)
		return
	}
	if !won {
		s.log.Warn("payout: send guard already taken, not sending again",
			"originator", p.OriginatorConversationID)
		return
	}

	accepted, err := s.sender.SendB2C(ctx, mpesaclient.B2CRequest{
		OriginatorConversationID: p.OriginatorConversationID,
		PhoneNumber:              p.PhoneNumber,
		Amount:                   p.Amount.Ceil().IntPart(),
		Remarks:                  "TradeDerivPlus withdrawal",
	})

	switch {
	case err == nil:
		if _, err := s.store.Payouts.MarkSent(ctx, p.OriginatorConversationID,
			accepted.ConversationID, time.Now().Add(firstQueryDelay)); err != nil {
			// The request is out and Daraja has it; failing to write that down is
			// bad but not money-losing, and the callback still matches on our id.
			s.log.Error("payout: accepted but could not record it",
				"originator", p.OriginatorConversationID, "conversation", accepted.ConversationID, "err", err)
			return
		}
		s.log.Info("payout sent", "originator", p.OriginatorConversationID,
			"conversation", accepted.ConversationID, "transaction", p.TransactionID)

	case errors.Is(err, mpesaclient.ErrPayoutRejected):
		// Daraja refused to queue it, so nothing can pay. This is the one failure
		// that is safe to act on immediately, and the reason the client
		// distinguishes it from not knowing.
		var rej *mpesaclient.PayoutRejection
		code := ""
		if errors.As(err, &rej) {
			code = rej.Code
		}
		s.log.Warn("payout refused by daraja", "originator", p.OriginatorConversationID,
			"code", code, "err", err)
		if _, serr := s.settle(ctx, p.OriginatorConversationID, models.PayoutFailed,
			code, err.Error(), "", PayoutSettledByRejection, nil); serr != nil {
			s.log.Error("payout: could not settle a rejection", "originator", p.OriginatorConversationID, "err", serr)
		}

	default:
		// Ambiguous. Daraja may hold this request; it may not. Nothing is
		// settled, nothing is refunded, and the fact of the ambiguity is written
		// down so an operator can see it and the reconciler can ask.
		s.log.Error("payout: OUTCOME UNKNOWN, needs reconciling or an operator",
			"originator", p.OriginatorConversationID, "transaction", p.TransactionID, "err", err)
		if _, merr := s.store.Payouts.MarkUnknown(ctx, p.OriginatorConversationID,
			"send was ambiguous: "+err.Error(), time.Now().Add(firstQueryDelay)); merr != nil {
			s.log.Error("payout: could not record an ambiguous send",
				"originator", p.OriginatorConversationID, "err", merr)
		}
	}
}

// HandleResult is the callback adapter, and `arrival` must come from the route
// that received the payload rather than from the payload itself.
//
// The rule it enforces, which is the reason there are two routes: anything that
// arrived on the queue-timeout channel is recorded and asked about, never
// settled. A timeout means Daraja's queue gave up before it knew the outcome, so
// refunding on it can refund a customer who was paid, and settling on it can
// mark paid a payout that never happened.
func (s *MpesaPayoutService) HandleResult(ctx context.Context, body []byte, arrival mpesaclient.PayoutArrival) error {
	result, err := mpesaclient.ParsePayoutCallback(body, arrival)
	if err != nil {
		return err
	}

	originator := result.OriginatorConversationID
	if originator == "" {
		// Only Safaricom's id came back. Find ours from it, because a result that
		// cannot be matched is a payout nobody can account for.
		row, err := s.store.Payouts.GetByConversationID(ctx, result.ConversationID)
		if err != nil {
			return fmt.Errorf("payout callback: no payout for conversation %q: %w", result.ConversationID, err)
		}
		originator = row.OriginatorConversationID
	}

	if arrival == mpesaclient.ArrivedAtTimeoutURL {
		s.log.Warn("payout: daraja's queue timed out; outcome unknown",
			"originator", originator, "result_code", result.ResultCode)
		if _, err := s.store.Payouts.MarkUnknown(ctx, originator,
			"daraja queue timeout (code "+result.ResultCode+")", time.Now().Add(firstQueryDelay)); err != nil {
			return err
		}
		return nil
	}

	switch result.Status {
	case mpesaclient.PayoutSucceeded:
		// Daraja reports the remaining utility float on every successful payout.
		// Free, and the difference between noticing an empty account before
		// customers do or after.
		if s.onFloat != nil && result.UtilityBalance != "" {
			s.onFloat(result.UtilityBalance)
		}
		_, err := s.settle(ctx, originator, models.PayoutCompleted, result.ResultCode,
			result.ResultDesc, result.Receipt, PayoutSettledByCallback, nil)
		return err

	case mpesaclient.PayoutFailed:
		if result.OursToFix {
			s.log.Error("payout failed for a reason at our end; the next one will fail the same way",
				"originator", originator, "result_code", result.ResultCode, "result_desc", result.ResultDesc)
		}
		_, err := s.settle(ctx, originator, models.PayoutFailed, result.ResultCode,
			result.ResultDesc, "", PayoutSettledByCallback, nil)
		return err

	default:
		// Daraja answered with something this package will not call terminal.
		// Recorded, not acted on.
		s.log.Warn("payout: daraja reported a code that is not terminal; leaving it open",
			"originator", originator, "result_code", result.ResultCode, "result_desc", result.ResultDesc)
		if _, err := s.store.Payouts.MarkUnknown(ctx, originator,
			"unrecognised result code "+result.ResultCode, time.Now().Add(firstQueryDelay)); err != nil {
			return err
		}
		return nil
	}
}

// MarkNeverReceived settles a payout that Safaricom never had.
//
// This is the state PayoutUnknown is deliberately NOT: a refund here is provably
// safe, because nothing was ever queued that could pay. Nothing reaches it
// automatically yet — establishing it needs a status query answer this codebase
// cannot yet recognise — so today it is an operator's conclusion, reached by
// checking the M-PESA portal. It exists as its own state so that the day the
// status query can say it, the transition is already here and already refunds
// correctly, and so that a resend path (if a repeated request id is ever proven
// safe to send) has exactly one state it may start from.
func (s *MpesaPayoutService) MarkNeverReceived(ctx context.Context, originatorID, reason string, adminID *uuid.UUID) (bool, error) {
	source := PayoutSettledByQuery
	if adminID != nil {
		source = PayoutSettledByAdmin
	}
	return s.settle(ctx, originatorID, models.PayoutNeverReceived, "", reason, "", source, adminID)
}

// SettleByAdmin is the console's adapter: an operator states the outcome of a
// payout Daraja never resolved.
func (s *MpesaPayoutService) SettleByAdmin(ctx context.Context, originatorID string, paid bool, reason string, adminID uuid.UUID) (bool, error) {
	status := models.PayoutFailed
	if paid {
		status = models.PayoutCompleted
	}
	return s.settle(ctx, originatorID, status, "", reason, "", PayoutSettledByAdmin, &adminID)
}

// settle is the only way a payout's outcome is written.
//
// Every source — the result callback, a rejection at the door, the status query,
// the console — comes through here, so the guarded write and the single call to
// onResult are the ones every path shares. It reports whether this call wrote the
// outcome; the write decides, not a read, because a result and its retry can both
// see an open row and only one of them may move money.
func (s *MpesaPayoutService) settle(ctx context.Context, originatorID, status, resultCode, resultDesc, receipt, source string, adminID *uuid.UUID) (bool, error) {
	// Refuse rather than consume the guard with nowhere to send the result: a
	// payout row gone terminal with the ledger still pending is unrepairable,
	// because every later attempt loses the guard this one burned. The same window
	// the deposit side has at boot.
	if s.onResult == nil {
		return false, errors.New("payout settle: no result handler registered")
	}

	row, err := s.store.Payouts.GetByOriginatorID(ctx, originatorID)
	if err != nil {
		return false, err
	}

	won, err := s.store.Payouts.Settle(ctx, originatorID, status, resultCode, resultDesc, receipt, source)
	if err != nil {
		return false, err
	}
	if !won {
		current, rerr := s.store.Payouts.GetByOriginatorID(ctx, originatorID)
		if rerr == nil && status == models.PayoutCompleted && current.Status != models.PayoutCompleted {
			// Daraja now says the customer was paid, and the row is already closed
			// as something else — so the customer may hold both the payout and a
			// refund. Only a person can reconcile that, and B2C cannot be reversed
			// by API, so it is an error and not a housekeeping line.
			s.log.Error("payout reported paid on a row already settled otherwise; the customer may have been refunded as well",
				"originator", originatorID, "row_status", current.Status, "receipt", receipt,
				"transaction", current.TransactionID)
			return false, nil
		}
		s.log.Info("duplicate payout settlement ignored", "originator", originatorID, "source", source)
		return false, nil
	}

	s.recordSettled(status)
	s.onResult(ctx, PayoutSettlement{
		TransactionID: row.TransactionID,
		Paid:          status == models.PayoutCompleted,
		Receipt:       receipt,
		Reason:        humanPayoutResult(resultCode, resultDesc),
		Source:        source,
		AdminID:       adminID,
	})
	return true, nil
}

// --- Mock mode --------------------------------------------------------------

// mockPayoutSender stands in for Daraja so the withdrawal flow is exercisable
// without credentials. It accepts the request and then, after a short delay,
// delivers a result to HandleResult on the RESULT channel — the same entry point
// a real callback uses, so mock mode exercises the real classification rather
// than a shortcut around it.
//
// One payout in six fails, cycling the B2C failure codes a customer can actually
// cause, so the refund path and its copy are exercisable too. A payout to
// MockStuckPhone is accepted and never reported on, which is how a stuck payout
// is produced on demand.
type mockPayoutSender struct{ svc *MpesaPayoutService }

func (m *mockPayoutSender) SendB2C(_ context.Context, r mpesaclient.B2CRequest) (*mpesaclient.B2CAccepted, error) {
	accepted := &mpesaclient.B2CAccepted{
		ConversationID:           "AG_MOCK_" + uuid.NewString()[:18],
		OriginatorConversationID: r.OriginatorConversationID,
		ResponseDescription:      "Accept the service request successfully.",
	}
	if mpesaclient.NormalizePhone(r.PhoneNumber) == MockStuckPhone {
		return accepted, nil
	}
	go m.deliver(r.OriginatorConversationID, r.Amount)
	return accepted, nil
}

func (m *mockPayoutSender) deliver(originatorID string, amount int64) {
	time.Sleep(m.svc.mockDelay)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	code, desc := "0", "The service request is processed successfully."
	receipt := "MOCK" + uuid.NewString()[:8]
	if rand.Intn(6) == 0 {
		receipt = ""
		switch rand.Intn(3) {
		case 0:
			code, desc = "2040", "The customer is not registered on M-PESA"
		case 1:
			code, desc = "SFC_IC0003", "The operator does not exist"
		default:
			code, desc = "1", "The balance is insufficient for the transaction"
		}
	}

	body := mockPayoutResultBody(originatorID, code, desc, receipt, amount)
	if err := m.svc.HandleResult(ctx, body, mpesaclient.ArrivedAtResultURL); err != nil {
		m.svc.log.Error("mock payout result failed", "originator", originatorID, "err", err)
	}
}

// mockPayoutResultBody builds the JSON Daraja would post, so the mock goes
// through the same parser as production rather than constructing a result
// directly.
func mockPayoutResultBody(originatorID, code, desc, receipt string, amount int64) []byte {
	params := fmt.Sprintf(`{"Key":"TransactionAmount","Value":%d}`, amount)
	if receipt != "" {
		params += fmt.Sprintf(`,{"Key":"TransactionReceipt","Value":%q},`+
			`{"Key":"ReceiverPartyPublicName","Value":"254712345678 - MOCK CUSTOMER"},`+
			`{"Key":"B2CUtilityAccountAvailableFunds","Value":425000.00}`, receipt)
	}
	return []byte(fmt.Sprintf(
		`{"Result":{"ResultType":0,"ResultCode":%q,"ResultDesc":%q,`+
			`"OriginatorConversationID":%q,"ConversationID":"AG_MOCK","TransactionID":%q,`+
			`"ResultParameters":{"ResultParameter":[%s]}}}`,
		code, desc, originatorID, receipt, params))
}
