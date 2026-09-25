package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
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

type MpesaPayoutService struct {
	store  *repository.Store
	client *mpesaclient.Client
	sender payoutSender
	cfg    *config.Config
	log    *slog.Logger

	onResult func(ctx context.Context, s PayoutSettlement)

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
		})
		s.sender = s.client
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
