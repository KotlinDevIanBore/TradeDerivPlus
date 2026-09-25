//go:build integration

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// CP2 of the B2C plan: the payout is requested, its outcome is written in one
// place, and the two states that mean "we do not know" and "it never happened"
// are distinct in the database rather than in a comment.

// --- The send ---------------------------------------------------------------

func TestWithdrawalReservesThePayoutBeforeItSends(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}

	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutSent {
		t.Fatalf("payout is %q after a send the stub accepted, want sent", p.Status)
	}
	// The full number, which the ledger row does not keep.
	if p.PhoneNumber != "254712345678" {
		t.Fatalf("payout phone %q, want the full MSISDN — the ledger keeps only a mask", p.PhoneNumber)
	}
	if got, _ := h.tx(tx.ID).Metadata["phone"].(string); got == p.PhoneNumber {
		t.Fatal("the ledger row is carrying the full number; it should keep only the masked form")
	}
	if p.OriginatorConversationID == "" || p.ConversationID == nil || *p.ConversationID == "" {
		t.Fatalf("payout has originator %q conversation %v, want both", p.OriginatorConversationID, p.ConversationID)
	}
	if p.RequestedAt == nil {
		t.Fatal("requested_at was not stamped; nothing records that a send was attempted")
	}
	if p.NextQueryAt == nil {
		t.Fatal("no next_query_at; the reconciler would never look at this row")
	}

	// And what went to Daraja is the id on the row, not one invented at send time.
	sent, ok := h.payoutSender.last()
	if !ok {
		t.Fatal("nothing was sent")
	}
	if sent.OriginatorConversationID != p.OriginatorConversationID {
		t.Fatalf("sent id %q but the row holds %q — a callback would match nothing",
			sent.OriginatorConversationID, p.OriginatorConversationID)
	}
	if sent.Amount != 12950 {
		t.Fatalf("sent KES %d, want 12950 whole shillings", sent.Amount)
	}
	// One send per withdrawal.
	if n := h.payoutSender.count(); n != 1 {
		t.Fatalf("%d requests for one withdrawal", n)
	}
	h.assertReconciled(accountID)
}

// P1 from the investigation: one withdrawal must never produce two requests. The
// database decides, before the network call, not after.
func TestOnePayoutRequestPerWithdrawalEvenUnderConcurrency(t *testing.T) {
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

	// Eight more attempts to send the same payout, released together. The guard
	// is requested_at, taken before dispatch, so none of them may reach Daraja.
	const callers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			h.payouts.Send(context.Background(), p)
		}()
	}
	close(start)
	wg.Wait()

	if n := h.payoutSender.count(); n != 1 {
		t.Fatalf("%d requests after %d concurrent resends of one payout, want 1 — "+
			"each extra one is a real payout no API can reverse", n, callers)
	}
	h.assertReconciled(accountID)
}

// Daraja refuses at the door: nothing was queued, so nothing can pay, and the
// refund is safe immediately. This is the one failure that does not wait.
func TestRejectedPayoutRefundsImmediately(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	h.payoutSender.fail(&mpesaclient.PayoutRejection{Code: "2001", Description: "Initiator information is invalid"})

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatalf("a refused payout should still return the withdrawal: %v", err)
	}

	if tx.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q after Daraja refused the request, want failed", tx.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after a refused payout, want the original 500 — the debit was not returned", w.RealBalance)
	}
	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutFailed {
		t.Fatalf("payout row is %q, want failed", p.Status)
	}
	if p.SettledBy == nil || *p.SettledBy != service.PayoutSettledByRejection {
		t.Fatalf("settled_by %v, want %q — a rejection is not a callback and the row should say so",
			p.SettledBy, service.PayoutSettledByRejection)
	}
	if p.ResultCode == nil || *p.ResultCode != "2001" {
		t.Fatalf("result_code %v, want 2001", p.ResultCode)
	}
	// And the customer is told something true that does not blame them: 2001 is
	// our credential being wrong.
	reason, _ := h.tx(tx.ID).Metadata["reason"].(string)
	if reason == "" {
		t.Fatal("no reason on the refunded withdrawal")
	}
	if got := reason; got == "The M-Pesa PIN you entered was wrong" {
		t.Fatal("the deposit copy leaked into a payout failure")
	}
	h.assertReconciled(accountID)
}

// The ambiguous send: the request may or may not have reached Safaricom. Nothing
// is settled and — the point — nothing is refunded.
func TestAmbiguousSendLeavesTheMoneyOutAndSaysSo(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	h.payoutSender.fail(errors.New("b2c send: dial tcp: connection reset by peer"))

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}

	if tx.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q after an ambiguous send, want pending — nothing is known either way", tx.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after an ambiguous send, want 400 — refunding here can pay a customer twice", w.RealBalance)
	}
	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutUnknown {
		t.Fatalf("payout row is %q, want %q — the ambiguity must be durable, not just logged",
			p.Status, models.PayoutUnknown)
	}
	if p.RequestedAt == nil {
		t.Fatal("requested_at is unset, so nothing records that a request may be in flight")
	}
	if p.ResultDesc == nil || *p.ResultDesc == "" {
		t.Fatal("nothing on the row says why it is unknown")
	}
	if !models.PayoutIsOpen(p.Status) {
		t.Fatal("an unknown payout does not count as open, so no reconciler or operator view will find it")
	}
	h.assertReconciled(accountID)

	// A later result settles it normally: 'unknown' is a waiting room, not a
	// terminal state.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QKLATE01", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q after a result arrived for an unknown payout, want completed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after the payout completed, want 400 — completion moves no money", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The state 'unknown' is deliberately not: Safaricom never had the request, so a
// refund is provably safe. Nothing reaches this automatically yet; it exists so
// the transition is already correct when something can.
func TestNeverReceivedIsADistinctStateThatRefunds(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	h.payoutSender.fail(errors.New("b2c send: context deadline exceeded"))

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q, want unknown", p.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s while unknown, want 400 — nothing is refunded from 'unknown'", w.RealBalance)
	}

	// Established: Safaricom never had it.
	won, err := h.payouts.MarkNeverReceived(h.ctx, p.OriginatorConversationID,
		"the M-PESA portal has no record of this request", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("MarkNeverReceived reported that it changed nothing")
	}

	after := h.payoutFor(tx.ID)
	if after.Status != models.PayoutNeverReceived {
		t.Fatalf("payout is %q, want %q — the two states must be distinguishable in the database",
			after.Status, models.PayoutNeverReceived)
	}
	if models.PayoutIsOpen(after.Status) {
		t.Fatal("never_received still counts as open; nothing more will arrive for it")
	}
	if after.NextQueryAt != nil {
		t.Fatal("a never-received payout is still scheduled for a query")
	}
	// The refund, which is what makes the state worth having.
	if got := h.tx(tx.ID); got.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q, want failed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after never_received, want the original 500 — the refund is provably safe here", w.RealBalance)
	}
	h.assertReconciled(accountID)

	// And it is terminal: a result arriving afterwards credits nothing, though it
	// is worth an operator's attention.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QKLATE02", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after a late result on a never-received payout, want 500", w.RealBalance)
	}
	if got := h.payoutFor(tx.ID); got.Status != models.PayoutNeverReceived {
		t.Fatalf("payout is %q after a late result, want never_received — a losing result rewrote a settled row", got.Status)
	}
	h.assertReconciled(accountID)
}

// --- The callbacks ----------------------------------------------------------

func TestPayoutResultSettlesTheWithdrawal(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "The service request is processed successfully.", "NA30XKHVCW", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}

	got := h.tx(tx.ID)
	if got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q, want completed", got.Status)
	}
	if receipt, _ := got.Metadata["receipt"].(string); receipt != "NA30XKHVCW" {
		t.Fatalf("receipt %q on the ledger row", receipt)
	}
	if by, _ := got.Metadata["settled_by"].(string); by != service.PayoutSettledByCallback {
		t.Fatalf("settled_by %q", by)
	}
	// A completion moves no money: the debit happened at request time.
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after the payout completed, want 400", w.RealBalance)
	}
	row := h.payoutFor(tx.ID)
	if row.Status != models.PayoutCompleted || row.Receipt == nil || *row.Receipt != "NA30XKHVCW" {
		t.Fatalf("payout row %q receipt %v", row.Status, row.Receipt)
	}
	h.assertReconciled(accountID)
}

func TestPayoutFailureRefundsWithCopyThatDoesNotBlameTheCustomer(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	// Code 1 on a payout is OUR float, not the customer's balance.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "1", "The balance is insufficient for the transaction", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}

	got := h.tx(tx.ID)
	if got.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q, want failed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after a failed payout, want the original 500", w.RealBalance)
	}
	reason, _ := got.Metadata["reason"].(string)
	if reason == "Your M-Pesa balance was too low to complete this deposit" {
		t.Fatal("the deposit copy for code 1 leaked into a payout failure; it blames the wrong balance")
	}
	if reason == "" {
		t.Fatal("no reason recorded")
	}
	if row := h.payoutFor(tx.ID); row.Status != models.PayoutFailed {
		t.Fatalf("payout row %q", row.Status)
	}
	h.assertReconciled(accountID)
}

// P3, and the reason there are two routes: a queue timeout settles nothing, even
// when it carries a code that would be terminal on the result channel.
func TestQueueTimeoutSettlesNothing(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	// 2040 on the result channel would be a terminal failure and a refund.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "2040", "The customer is not registered", "", mpesaclient.ArrivedAtTimeoutURL); err != nil {
		t.Fatal(err)
	}

	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q after a queue timeout, want pending — a timeout is not an outcome", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after a queue timeout, want 400 — refunding here can refund a customer who was paid", w.RealBalance)
	}
	row := h.payoutFor(tx.ID)
	if row.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q after a timeout, want unknown", row.Status)
	}

	// And the real outcome, when it comes, still settles — including "paid",
	// which is the case a timeout-as-failure would have made unrecoverable.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QKAFTER01", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q after the real result, want completed", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400 — the timeout must not have refunded", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// A code Daraja sends that this codebase will not call terminal leaves the payout
// open rather than guessing in either direction.
func TestUnrecognisedResultCodeLeavesThePayoutOpen(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	if err := h.deliverPayoutResult(p.OriginatorConversationID, "TP40153", "Something new", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q on an unrecognised code, want pending", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400 — an unknown code must not refund", w.RealBalance)
	}
	if row := h.payoutFor(tx.ID); row.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q, want unknown", row.Status)
	}
	h.assertReconciled(accountID)
}

// P2: a result and its retries, all at once. One settlement between them.
func TestConcurrentPayoutResultsSettleExactlyOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	const callers = 8
	for attempt := 0; attempt < 5; attempt++ {
		tx, err := h.withdraw(userID, accountID, 100)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}
		p := h.payoutFor(tx.ID)

		// Half deliver the failure, half a queue timeout for the same payout: the
		// two channels racing is the shape a real outage produces.
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				arrival := mpesaclient.ArrivedAtResultURL
				if i%2 == 1 {
					arrival = mpesaclient.ArrivedAtTimeoutURL
				}
				if err := h.payouts.HandleResult(context.Background(),
					payoutResultBody(p.OriginatorConversationID, "2040", "not registered", ""), arrival); err != nil {
					errs <- err
				}
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("attempt %d: %v", attempt, err)
		}

		// Refunded once. The balance is back to its starting point every round.
		if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(1000)) {
			t.Fatalf("attempt %d: balance %s after %d concurrent results for one payout, want 1000",
				attempt, w.RealBalance, callers)
		}
		if got := h.tx(tx.ID); got.Status != models.StatusFailed {
			t.Fatalf("attempt %d: withdrawal is %q", attempt, got.Status)
		}
		h.assertReconciled(accountID)
	}
}

// The boot window: a payout service with no wallet behind it must refuse to
// settle, and refuse BEFORE consuming the guard, or the payout row goes terminal
// with the ledger pending for ever.
func TestPayoutSettleRefusesWithNoResultHandler(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	p := h.payoutFor(tx.ID)

	lone := service.NewMpesaPayoutService(h.store, h.cfg, h.log) // no SetPayouts, so no handler
	err = lone.HandleResult(h.ctx,
		payoutResultBody(p.OriginatorConversationID, "0", "processed", "QK1"), mpesaclient.ArrivedAtResultURL)
	if err == nil {
		t.Fatal("a payout settlement with no result handler was accepted")
	}

	if row := h.payoutFor(tx.ID); row.Status != models.PayoutSent {
		t.Fatalf("payout is %q after a refused settle, want sent — the guard was consumed with nowhere to send the result", row.Status)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q", got.Status)
	}

	// The wired service then settles the same result normally.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatalf("settle after the refusal: %v", err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q, want completed — the refusal burned the guard", got.Status)
	}
	h.assertReconciled(accountID)
}

// A payout to the stuck number is accepted and never reported on, which is how a
// withdrawal stuck in flight is produced on demand — for the operator tooling in
// the next checkpoint and for anyone exercising the flow by hand.
func TestStuckPhonePayoutStaysSent(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	tx, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(100), PhoneNumber: service.MockStuckPhone,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q", tx.Status)
	}
	p := h.payoutFor(tx.ID)
	if p.Status != models.PayoutSent {
		t.Fatalf("payout is %q, want sent and waiting", p.Status)
	}
	if !models.PayoutIsOpen(p.Status) {
		t.Fatal("a sent payout does not count as open")
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s, want 400", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The operator's view: everything still open, and the counts the gauges will use.
func TestOpenPayoutsAreVisibleToAnOperator(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	// One sent and waiting.
	sentTx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	// One ambiguous.
	if err := h.deliverPayoutResult(h.payoutFor(sentTx.ID).OriginatorConversationID,
		"0", "processed", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	h.payoutSender.fail(errors.New("b2c send: connection reset"))
	unknownTx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	h.payoutSender.fail(nil)

	open, err := h.store.Payouts.ListOpen(h.ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].TransactionID != unknownTx.ID {
		t.Fatalf("ListOpen returned %d rows, want just the unknown one", len(open))
	}
	if open[0].Status != models.PayoutUnknown {
		t.Fatalf("the open row is %q", open[0].Status)
	}

	counts, err := h.store.Payouts.CountByStatus(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if counts[models.PayoutCompleted] != 1 || counts[models.PayoutUnknown] != 1 {
		t.Fatalf("counts %v, want one completed and one unknown", counts)
	}
	h.assertReconciled(accountID)
}

// Mock mode's own sender, which the harness replaces everywhere else. It is what
// a dev server and a manual exercise of the flow actually use, so it is worth one
// test of its own rather than being the only untested path in the payout code.
//
// It goes through HandleResult on the result channel — the real entry point and
// the real classifier — so what mock mode exercises is the production
// classification rather than a shortcut around it.
func TestMockPayoutSenderSettlesThroughTheRealPath(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	// Put mock mode's own simulator back, quickened.
	mock := service.NewMpesaPayoutService(h.store, h.cfg, h.log)
	mock.SetMockDelayForTest(20 * time.Millisecond)
	h.wallet.SetPayouts(mock)
	defer func() {
		h.wallet.SetPayouts(h.payouts)
		h.payouts.SetSenderForTest(h.payoutSender)
	}()

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if h.payoutFor(tx.ID).Status != models.PayoutSent {
		t.Fatalf("payout is %q right after the mock accepted it", h.payoutFor(tx.ID).Status)
	}

	// The simulator reports a result on its own; one in six is a failure, so both
	// outcomes are acceptable and both must reconcile.
	waitFor(t, "the mock payout result", 5*time.Second, func() bool {
		return !models.PayoutIsOpen(h.payoutFor(tx.ID).Status)
	})

	row := h.payoutFor(tx.ID)
	got := h.tx(tx.ID)
	switch row.Status {
	case models.PayoutCompleted:
		if got.Status != models.StatusCompleted {
			t.Fatalf("payout completed but the withdrawal is %q", got.Status)
		}
		if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
			t.Fatalf("balance %s after a paid payout, want 400", w.RealBalance)
		}
		if row.Receipt == nil || *row.Receipt == "" {
			t.Fatal("a completed mock payout carries no receipt")
		}
	case models.PayoutFailed:
		if got.Status != models.StatusFailed {
			t.Fatalf("payout failed but the withdrawal is %q", got.Status)
		}
		if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
			t.Fatalf("balance %s after a failed payout, want the refunded 500", w.RealBalance)
		}
		// The failure codes the mock uses are real B2C ones, so the copy must be
		// the payout copy and not the deposit's.
		reason, _ := got.Metadata["reason"].(string)
		if reason == "" || reason == "Your M-Pesa balance was too low to complete this deposit" {
			t.Fatalf("mock failure reason %q", reason)
		}
	default:
		t.Fatalf("payout settled as %q", row.Status)
	}
	h.assertReconciled(accountID)
}

// And the stuck number, through the real mock: accepted, never reported on.
func TestMockPayoutSenderNeverReportsOnTheStuckNumber(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	mock := service.NewMpesaPayoutService(h.store, h.cfg, h.log)
	mock.SetMockDelayForTest(20 * time.Millisecond)
	h.wallet.SetPayouts(mock)
	defer func() {
		h.wallet.SetPayouts(h.payouts)
		h.payouts.SetSenderForTest(h.payoutSender)
	}()

	tx, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(100), PhoneNumber: service.MockStuckPhone,
	})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond) // fifteen times the simulator's delay
	if row := h.payoutFor(tx.ID); row.Status != models.PayoutSent {
		t.Fatalf("payout to the stuck number is %q, want sent and waiting for ever", row.Status)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q", got.Status)
	}
	h.assertReconciled(accountID)
}
