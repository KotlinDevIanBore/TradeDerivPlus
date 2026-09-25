//go:build integration

package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// CP3: the pass that asks about payouts nobody has answered for. It asks; it
// never concludes and never resends. The answer arrives on its own channel.

// duePayout brings a payout's next look forward so a pass will pick it up.
func (h *harness) duePayout(originatorID string) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx,
		`UPDATE mpesa_payouts SET next_query_at = now() - interval '1 second' WHERE originator_conversation_id = $1`,
		originatorID); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) agePayout(originatorID string, interval string) {
	h.t.Helper()
	if _, err := h.pool.Exec(h.ctx,
		`UPDATE mpesa_payouts SET created_at = now() - `+interval+` WHERE originator_conversation_id = $1`,
		originatorID); err != nil {
		h.t.Fatal(err)
	}
}

// A payout in flight is asked about, and the backoff widens. Nothing is concluded
// and no money moves: the answer is asynchronous and arrives elsewhere.
func TestPassAsksAndWidensTheBackoff(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	ladder := []time.Duration{5 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, time.Hour}
	for i, want := range ladder {
		h.duePayout(p.OriginatorConversationID)
		passAt := time.Now()
		h.payouts.Reconcile(h.ctx)

		row := h.payoutFor(tx.ID)
		if row.ReconcileAttempts != i+1 {
			t.Fatalf("pass %d: attempts %d, want %d", i+1, row.ReconcileAttempts, i+1)
		}
		if row.NextQueryAt == nil {
			t.Fatalf("pass %d: no schedule", i+1)
		}
		if gap := row.NextQueryAt.Sub(passAt); gap < want-2*time.Second || gap > want+2*time.Second {
			t.Fatalf("pass %d: next look %s out, want %s", i+1, gap.Round(time.Second), want)
		}
		if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != i+1 {
			t.Fatalf("pass %d: asked %d times, want %d", i+1, n, i+1)
		}

		// A pass inside the backoff asks nothing.
		h.payouts.Reconcile(h.ctx)
		if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != i+1 {
			t.Fatalf("pass %d: a pass inside the backoff asked again (%d)", i+1, n)
		}
	}

	// Still in flight, nothing decided, and the money is still out.
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q after five status queries, want pending — the pass must not conclude", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400 — asking about a payout moves no money", w.RealBalance)
	}
	if row := h.payoutFor(tx.ID); row.Status != models.PayoutSent {
		t.Fatalf("payout is %q, want still sent", row.Status)
	}
	h.assertReconciled(accountID)
}

// The pass never resends. There is deliberately no code that could, and this is
// the test that would fail if any appeared.
func TestPassNeverResendsThePayout(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)
	if n := h.payoutSender.count(); n != 1 {
		t.Fatalf("%d sends from one withdrawal", n)
	}

	// Ten passes, including from the 'unknown' state, which is where a resend
	// would be most tempting.
	if _, err := h.pool.Exec(h.ctx,
		`UPDATE mpesa_payouts SET status = 'unknown' WHERE originator_conversation_id = $1`,
		p.OriginatorConversationID); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		h.duePayout(p.OriginatorConversationID)
		h.payouts.Reconcile(h.ctx)
	}

	if n := h.payoutSender.count(); n != 1 {
		t.Fatalf("%d payout requests after ten passes, want 1 — the pass resent a payout, and "+
			"whether Safaricom deduplicates a repeated request id is unproven", n)
	}
	if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != 10 {
		t.Fatalf("asked %d times in ten passes, want 10", n)
	}
	h.assertReconciled(accountID)
}

// Being unable to ask is not an attempt: the row keeps its schedule, so it is
// still due and the next pass asks again. A Daraja outage must not walk payouts
// toward the horizon while nobody can look at them.
func TestPassFailureToAskIsNotAnAttempt(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)
	before := h.payoutFor(tx.ID)
	h.payoutQuerier.fail(errors.New("transaction status: connection refused"))

	h.duePayout(p.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)
	h.payouts.Reconcile(h.ctx)

	row := h.payoutFor(tx.ID)
	if row.ReconcileAttempts != before.ReconcileAttempts {
		t.Fatalf("attempts %d after two failed asks, want %d — a failure to ask counted as an attempt",
			row.ReconcileAttempts, before.ReconcileAttempts)
	}
	if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != 2 {
		t.Fatalf("asked %d times, want 2 — the row must stay due", n)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400", w.RealBalance)
	}

	// Daraja comes back and the ask lands.
	h.payoutQuerier.fail(nil)
	h.payouts.Reconcile(h.ctx)
	if row := h.payoutFor(tx.ID); row.ReconcileAttempts != before.ReconcileAttempts+1 {
		t.Fatalf("attempts %d once the ask succeeded", row.ReconcileAttempts)
	}
	h.assertReconciled(accountID)
}

// The status answer, on its own channel. A paid answer settles the withdrawal.
func TestStatusAnswerSettlesAPayout(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	if err := h.deliverStatusResult(p.OriginatorConversationID, "0", "Completed", "NA30XKHVCW", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}

	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q after a status answer of Completed, want completed", got.Status)
	}
	row := h.payoutFor(tx.ID)
	if row.Status != models.PayoutCompleted {
		t.Fatalf("payout is %q", row.Status)
	}
	if row.SettledBy == nil || *row.SettledBy != service.PayoutSettledByQuery {
		t.Fatalf("settled_by %v, want %q", row.SettledBy, service.PayoutSettledByQuery)
	}
	if row.Receipt == nil || *row.Receipt != "NA30XKHVCW" {
		t.Fatalf("receipt %v", row.Receipt)
	}
	// Completion moves no money.
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

func TestStatusAnswerOfDeclinedRefunds(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	if err := h.deliverStatusResult(p.OriginatorConversationID, "0", "Declined", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q after Declined, want failed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after a declined payout, want the refunded 500", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The distinction the separate channel exists for: a non-zero result code on the
// STATUS channel means the query failed, not the payout. Nothing follows from it.
func TestFailedStatusQueryConcludesNothing(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	// 2001 is "our credentials are wrong". On a payout result it is terminal; on a
	// status answer it says only that we failed to ask properly.
	for _, queryCode := range []string{"2001", "SFC_IC0003", "1"} {
		if err := h.deliverStatusResult(p.OriginatorConversationID, queryCode, "", "", mpesaclient.ArrivedAtResultURL); err != nil {
			t.Fatalf("query code %s: %v", queryCode, err)
		}
		if got := h.tx(tx.ID); got.Status != models.StatusPending {
			t.Fatalf("query code %s settled the withdrawal as %q — a failed QUERY says nothing about the payout",
				queryCode, got.Status)
		}
		if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
			t.Fatalf("query code %s moved the balance to %s", queryCode, w.RealBalance)
		}
	}

	// And "Pending" from a successful query is equally inconclusive.
	if err := h.deliverStatusResult(p.OriginatorConversationID, "0", "Pending", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("a Pending status settled the withdrawal as %q", got.Status)
	}
	h.assertReconciled(accountID)
}

// A status answer arriving on the timeout channel concludes nothing either, even
// when it says the payout completed.
func TestStatusAnswerOnTheTimeoutChannelConcludesNothing(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	if err := h.deliverStatusResult(p.OriginatorConversationID, "0", "Completed", "NA30X", mpesaclient.ArrivedAtTimeoutURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q after a Completed status on the timeout channel, want pending", got.Status)
	}
	if row := h.payoutFor(tx.ID); row.Status != models.PayoutSent {
		t.Fatalf("payout is %q", row.Status)
	}
	h.assertReconciled(accountID)
}

// Give-up: stop asking, hand it to a person, and — the point — do NOT refund. The
// customer's money is out and nothing has been learned.
func TestGiveUpStopsAskingWithoutRefunding(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	h.payoutSender.fail(errors.New("b2c send: connection reset"))
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	h.payoutSender.fail(nil)
	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q, want unknown", p.Status)
	}

	h.agePayout(p.OriginatorConversationID, "interval '25 hours'")
	h.duePayout(p.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)
	h.payouts.Reconcile(h.ctx)

	if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != 0 {
		t.Fatalf("asked %d times about a payout past the horizon, want 0", n)
	}
	row := h.payoutFor(tx.ID)
	if row.NextQueryAt != nil {
		t.Fatalf("a given-up payout is still scheduled for %v", row.NextQueryAt)
	}
	if row.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q after give-up, want still unknown — giving up is not learning something", row.Status)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q after give-up, want pending", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after give-up, want 400 — giving up must NOT refund: nothing was learned, "+
			"and a refund plus a payout Safaricom accepted is money gone twice", w.RealBalance)
	}

	// It is off the pass's list but still visible as open, and countable as
	// given up.
	due, err := h.store.Payouts.ListDue(h.ctx, time.Now(), 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range due {
		if d.OriginatorConversationID == p.OriginatorConversationID {
			t.Fatal("a given-up payout is still on the pass's list")
		}
	}
	open, err := h.store.Payouts.ListOpen(h.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].OriginatorConversationID != p.OriginatorConversationID {
		t.Fatalf("a given-up payout is not visible as open: %d rows", len(open))
	}
	n, err := h.store.Payouts.CountGivenUp(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("CountGivenUp is %d, want 1", n)
	}
	h.assertReconciled(accountID)
}

// A settled payout is never asked about, and a late answer for one loses.
func TestPassIgnoresSettledPayouts(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}

	h.duePayout(p.OriginatorConversationID) // even if something schedules it
	h.payouts.Reconcile(h.ctx)
	if n := h.payoutQuerier.countFor(p.OriginatorConversationID); n != 0 {
		t.Fatalf("asked %d times about a settled payout", n)
	}

	// A status answer arriving late for it changes nothing.
	if err := h.deliverStatusResult(p.OriginatorConversationID, "0", "Declined", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q after a late Declined status, want still completed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s — a late status answer refunded a paid payout", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The boot pass, over a fresh service on the same store, picks up what a restart
// left open.
func TestBootPassAsksAboutAStrandedPayout(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)
	h.duePayout(p.OriginatorConversationID)

	next := service.NewMpesaPayoutService(h.store, h.cfg, h.log)
	querier := &stubPayoutQuerier{}
	next.SetQuerierForTest(querier)
	_ = service.NewWalletService(h.store, h.mpesa, noopBroadcaster{}, h.cfg, h.log)
	// The wallet must be wired, or the pass cannot settle anything it learns.
	lone := service.NewWalletService(h.store, h.mpesa, noopBroadcaster{}, h.cfg, h.log)
	lone.SetPayouts(next)
	defer h.wallet.SetPayouts(h.payouts)

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	next.Start(ctx)

	waitFor(t, "the boot pass to ask", 5*time.Second, func() bool {
		return querier.countFor(p.OriginatorConversationID) > 0
	})
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("the boot pass settled the withdrawal as %q", got.Status)
	}
	h.assertReconciled(accountID)
}

// Mock mode has nothing to ask, so the pass still runs the parts most likely to
// rot — the listing, the schedule, the horizon — and settles nothing.
func TestPassRunsInMockModeWithoutAsking(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	// Mock mode's service: no querier at all.
	mock := service.NewMpesaPayoutService(h.store, h.cfg, h.log)
	lone := service.NewWalletService(h.store, h.mpesa, noopBroadcaster{}, h.cfg, h.log)
	lone.SetPayouts(mock)
	defer h.wallet.SetPayouts(h.payouts)

	h.duePayout(p.OriginatorConversationID)
	mock.Reconcile(h.ctx)

	row := h.payoutFor(tx.ID)
	if row.ReconcileAttempts != 1 {
		t.Fatalf("attempts %d in mock mode, want 1 — the schedule should still advance", row.ReconcileAttempts)
	}
	if row.Status != models.PayoutSent {
		t.Fatalf("payout is %q", row.Status)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("mock mode's pass settled the withdrawal as %q", got.Status)
	}
	h.assertReconciled(accountID)
}

// The metrics the pass and the settlements feed, including the float, which comes
// free on every successful payout.
func TestPayoutMetricsAndFloatAreReported(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	settled := map[string]int{}
	queried := map[string]int{}
	float := ""
	h.payouts.OnSettled(func(o string) { settled[o]++ })
	h.payouts.OnQueried(func(o string) { queried[o]++ })
	h.payouts.OnFloat(func(v string) { float = v })

	// A paid payout: one settlement, and the float from its result.
	paid, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	pp := h.payoutFor(paid.ID)
	if err := h.payouts.HandleResult(h.ctx, payoutResultBody(pp.OriginatorConversationID, "0", "ok", "QK1"),
		mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if settled[models.PayoutCompleted] != 1 {
		t.Fatalf("settled %v, want one completed", settled)
	}
	if float == "" {
		t.Fatal("no utility float reported from a successful payout; the gauge has no free source")
	}

	// A failed one.
	failedTx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	fp := h.payoutFor(failedTx.ID)
	if err := h.deliverPayoutResult(fp.OriginatorConversationID, "2040", "not registered", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if settled[models.PayoutFailed] != 1 {
		t.Fatalf("settled %v, want one failed", settled)
	}

	// An asked-about one, and then a given-up one.
	open, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	op := h.payoutFor(open.ID)
	h.duePayout(op.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)
	if queried[service.PayoutQueryAsked] != 1 {
		t.Fatalf("queried %v, want one asked", queried)
	}

	h.agePayout(op.OriginatorConversationID, "interval '25 hours'")
	h.duePayout(op.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)
	if queried[service.PayoutQueryGaveUp] != 1 {
		t.Fatalf("queried %v, want one gave_up", queried)
	}
	// Exactly one: a second pass must not re-count it.
	h.payouts.Reconcile(h.ctx)
	if queried[service.PayoutQueryGaveUp] != 1 {
		t.Fatalf("gave_up counted %d times; a given-up payout is off the list", queried[service.PayoutQueryGaveUp])
	}

	// never_received is its own outcome, not folded into failed.
	if _, err := h.payouts.MarkNeverReceived(h.ctx, op.OriginatorConversationID, "not on the portal", nil); err != nil {
		t.Fatal(err)
	}
	if settled[models.PayoutNeverReceived] != 1 {
		t.Fatalf("settled %v, want one never_received counted separately from failed", settled)
	}
	h.assertReconciled(accountID)
}

// The gauge worth alerting on, through the sampler the collector uses.
func TestUnknownAndGivenUpCountsAreSampleable(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	counts, err := h.store.Payouts.CountByStatus(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[models.PayoutUnknown] != 0 {
		t.Fatalf("a fresh database reports %d unknown payouts", counts[models.PayoutUnknown])
	}

	h.payoutSender.fail(errors.New("b2c send: connection reset"))
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	h.payoutSender.fail(nil)

	counts, err = h.store.Payouts.CountByStatus(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[models.PayoutUnknown] != 1 {
		t.Fatalf("unknown count %d, want 1 — this is the number an operator is alerted on", counts[models.PayoutUnknown])
	}
	if n, err := h.store.Payouts.CountGivenUp(h.ctx); err != nil || n != 0 {
		t.Fatalf("given-up count %d (err %v), want 0 — it is still being asked about", n, err)
	}

	// Once given up it counts in both: still unknown to the customer, and now a
	// person's.
	p := h.payoutFor(tx.ID)
	h.agePayout(p.OriginatorConversationID, "interval '25 hours'")
	h.duePayout(p.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)

	counts, err = h.store.Payouts.CountByStatus(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[models.PayoutUnknown] != 1 {
		t.Fatalf("unknown count %d after give-up, want still 1", counts[models.PayoutUnknown])
	}
	if n, err := h.store.Payouts.CountGivenUp(h.ctx); err != nil || n != 1 {
		t.Fatalf("given-up count %d (err %v), want 1", n, err)
	}
	h.assertReconciled(accountID)
}

// The two write guards the pass depends on, asserted directly.
//
// Both exist for a race the pass cannot reach on its own: a payout settling
// between ListDue reading it and the pass writing to it. Sequentially the pass
// never calls either on a settled row, because ListDue filters them out — so
// mutations that remove these guards survive every test that drives the pass, and
// the guards have to be pinned at the repository instead.
func TestPassWriteGuardsAreNoOpsOnSettledPayouts(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	// A settled payout: neither write may touch it.
	settledTx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	sp := h.payoutFor(settledTx.ID)
	if err := h.deliverPayoutResult(sp.OriginatorConversationID, "0", "ok", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	before := h.payoutFor(settledTx.ID)

	won, err := h.store.Payouts.MarkQueried(h.ctx, sp.OriginatorConversationID, 9, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("MarkQueried claimed a settled payout; a pass that lost this race would reschedule a finished payout")
	}
	won, err = h.store.Payouts.MarkGaveUp(h.ctx, sp.OriginatorConversationID, "gave up")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("MarkGaveUp claimed a settled payout")
	}

	after := h.payoutFor(settledTx.ID)
	if after.ReconcileAttempts != before.ReconcileAttempts {
		t.Fatalf("attempts %d → %d on a settled payout", before.ReconcileAttempts, after.ReconcileAttempts)
	}
	if after.NextQueryAt != nil {
		t.Fatalf("a settled payout was put back on the schedule at %v", after.NextQueryAt)
	}
	if after.Status != models.PayoutCompleted {
		t.Fatalf("payout is %q", after.Status)
	}

	// And on an open payout both do apply, so the guard is a guard and not a
	// blanket refusal.
	openTx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	op := h.payoutFor(openTx.ID)
	won, err = h.store.Payouts.MarkQueried(h.ctx, op.OriginatorConversationID, 3, time.Now().Add(time.Hour))
	if err != nil || !won {
		t.Fatalf("MarkQueried on an open payout: won=%v err=%v", won, err)
	}
	if row := h.payoutFor(openTx.ID); row.ReconcileAttempts != 3 {
		t.Fatalf("attempts %d, want 3", row.ReconcileAttempts)
	}
	won, err = h.store.Payouts.MarkGaveUp(h.ctx, op.OriginatorConversationID, "gave up")
	if err != nil || !won {
		t.Fatalf("MarkGaveUp on an open payout: won=%v err=%v", won, err)
	}
	if row := h.payoutFor(openTx.ID); row.NextQueryAt != nil {
		t.Fatal("MarkGaveUp left a schedule behind")
	}
	// Twice is a no-op: it is already off the schedule, so nothing is re-counted.
	won, err = h.store.Payouts.MarkGaveUp(h.ctx, op.OriginatorConversationID, "again")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Fatal("MarkGaveUp claimed a payout it had already given up on; the gave_up metric would double-count")
	}
	h.assertReconciled(accountID)
}
