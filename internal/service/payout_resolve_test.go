//go:build integration

package service_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// CP4: the console's two jobs on a stuck payout — see it, and settle it. The
// settle goes through the payout service's own guarded path, so a result arriving
// afterwards moves nothing.

const payoutReason = "Checked the M-Pesa portal against the request id; no record of this payout"

// stuckPayout leaves a withdrawal in flight with its payout unanswered, which is
// the only state the console has anything to do.
func (h *harness) stuckPayout(userID, accountID uuid.UUID, usd int64) (*models.Transaction, *models.MpesaPayout) {
	h.t.Helper()
	tx, err := h.withdraw(userID, accountID, usd)
	if err != nil {
		h.t.Fatal(err)
	}
	return tx, h.payoutFor(tx.ID)
}

// Marking a payout paid closes the withdrawal and moves no money: the debit
// happened when it was requested.
func TestResolvePayoutAsPaidMovesNoMoney(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	adminID := h.newAdmin()
	tx, p := h.stuckPayout(userID, accountID, 100)

	got, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, service.PayoutOutcomePaid,
		"Confirmed on the M-Pesa portal: receipt NA30XKHVCW to this number")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.ID != tx.ID || got.Status != models.StatusCompleted {
		t.Fatalf("resolve returned %s %q, want the original %s completed", got.ID, got.Status, tx.ID)
	}
	if by, _ := got.Metadata["settled_by"].(string); by != service.PayoutSettledByAdmin {
		t.Fatalf("settled_by %q, want admin", by)
	}
	if id, _ := got.Metadata["admin_id"].(string); id != adminID.String() {
		t.Fatalf("admin_id %q on the ledger row, want %s", id, adminID)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after marking a payout paid, want 400 — the money had already left", w.RealBalance)
	}
	row := h.payoutFor(tx.ID)
	if row.Status != models.PayoutCompleted {
		t.Fatalf("payout is %q", row.Status)
	}

	rows := h.auditRows(models.ActionPayoutResolve)
	if len(rows) != 1 || rows[0].TargetID != tx.ID || rows[0].AdminID != adminID {
		t.Fatalf("audit rows %+v, want one for %s by %s", rows, tx.ID, adminID)
	}
	if got, _ := rows[0].Metadata["originator_conversation_id"].(string); got != p.OriginatorConversationID {
		t.Fatalf("audit metadata carries request id %q, want %s", got, p.OriginatorConversationID)
	}
	if got, _ := rows[0].Metadata["outcome"].(string); got != service.PayoutOutcomePaid {
		t.Fatalf("audit outcome %q", got)
	}

	// The point of settling the original row: a late result credits or refunds
	// nothing.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "2040", "not registered", "", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after a late failure result, want 400 — it refunded a payout an operator had confirmed", w.RealBalance)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusCompleted {
		t.Fatalf("withdrawal is %q after a late result", got.Status)
	}
	h.assertReconciled(accountID)
}

// The two refunding outcomes. They move the same money and record different
// findings, which is why both exist.
func TestResolvePayoutRefundsAndRecordsWhichFindingItWas(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()

	for _, c := range []struct {
		outcome    string
		wantStatus string
	}{
		{service.PayoutOutcomeNeverReceived, models.PayoutNeverReceived},
		{service.PayoutOutcomeFailed, models.PayoutFailed},
	} {
		t.Run(c.outcome, func(t *testing.T) {
			userID, accountID := h.verifiedWithBalance(500)
			tx, p := h.stuckPayout(userID, accountID, 100)

			got, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, c.outcome, payoutReason)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got.Status != models.StatusFailed {
				t.Fatalf("withdrawal is %q, want failed", got.Status)
			}
			if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
				t.Fatalf("balance %s, want the refunded 500", w.RealBalance)
			}
			if row := h.payoutFor(tx.ID); row.Status != c.wantStatus {
				t.Fatalf("payout is %q, want %q — the two findings must be distinguishable in the record",
					row.Status, c.wantStatus)
			}
			// And a late "paid" result credits nothing, though it is worth an
			// operator's attention.
			if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QKLATE", mpesaclient.ArrivedAtResultURL); err != nil {
				t.Fatal(err)
			}
			if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
				t.Fatalf("balance %s after a late paid result, want 500", w.RealBalance)
			}
			h.assertReconciled(accountID)
		})
	}
}

func TestResolvePayoutRequiresAReasonAndAValidOutcome(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	adminID := h.newAdmin()
	tx, _ := h.stuckPayout(userID, accountID, 100)

	var ve *service.ValidationError
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, service.PayoutOutcomePaid, "ok"); !errors.As(err, &ve) || ve.Field != "reason" {
		t.Fatalf("short reason: %v, want a validation error on reason", err)
	}
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, "refund-it", payoutReason); !errors.As(err, &ve) || ve.Field != "outcome" {
		t.Fatalf("bad outcome: %v, want a validation error on outcome", err)
	}

	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("a refused resolve moved the withdrawal to %q", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("a refused resolve moved the balance to %s", w.RealBalance)
	}
	if rows := h.auditRows(models.ActionPayoutResolve); len(rows) != 0 {
		t.Fatalf("a refused resolve was audited: %+v", rows)
	}
}

func TestResolvePayoutRefusesAnythingButAnOpenWithdrawal(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)
	adminID := h.newAdmin()

	// Already settled.
	done, p := h.stuckPayout(userID, accountID, 100)
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, done.ID, service.PayoutOutcomePaid, payoutReason); !errors.Is(err, service.ErrPayoutSettled) {
		t.Fatalf("resolving a settled payout: %v, want ErrPayoutSettled", err)
	}

	// Not a withdrawal.
	depTx, _ := h.pendingDeposit(userID, accountID, 5000)
	var ve *service.ValidationError
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, depTx, service.PayoutOutcomePaid, payoutReason); !errors.As(err, &ve) || ve.Field != "transaction" {
		t.Fatalf("resolving a deposit as a payout: %v, want a validation error on transaction", err)
	}

	// Unknown id.
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, uuid.New(), service.PayoutOutcomePaid, payoutReason); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("resolving an unknown id: %v, want ErrNotFound", err)
	}

	if rows := h.auditRows(models.ActionPayoutResolve); len(rows) != 0 {
		t.Fatalf("refused resolves were audited: %+v", rows)
	}
	h.assertReconciled(accountID)
}

// The console loses to a result that lands first, and audits nothing: the row
// already says who settled it.
func TestResolvePayoutLosesToAResultThatLandsFirst(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	adminID := h.newAdmin()
	tx, p := h.stuckPayout(userID, accountID, 100)

	// Settle it behind the operator's back, as a callback would between their read
	// and their confirm.
	if err := h.deliverPayoutResult(p.OriginatorConversationID, "0", "processed", "QKFIRST", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}

	if _, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, service.PayoutOutcomeNeverReceived, payoutReason); !errors.Is(err, service.ErrPayoutSettled) {
		t.Fatalf("resolve racing a result: %v, want ErrPayoutSettled", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s — a losing resolve refunded a paid payout", w.RealBalance)
	}
	if by, _ := h.tx(tx.ID).Metadata["settled_by"].(string); by != service.PayoutSettledByCallback {
		t.Fatalf("settled_by %q, want callback — the losing resolve overwrote it", by)
	}
	if rows := h.auditRows(models.ActionPayoutResolve); len(rows) != 0 {
		t.Fatalf("a resolve that changed nothing was audited: %+v", rows)
	}
	h.assertReconciled(accountID)
}

// A payout the console cannot settle because nothing was wired: refuse rather
// than settle by some other route.
func TestResolvePayoutRefusesWithNoPayoutServiceWired(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	adminID := h.newAdmin()
	tx, _ := h.stuckPayout(userID, accountID, 100)

	lone := service.NewAdminService(h.store, noopBroadcaster{}, h.cfg, h.log)
	if _, err := lone.ResolvePayout(h.ctx, adminID, tx.ID, service.PayoutOutcomePaid, payoutReason); err == nil {
		t.Fatal("an unwired admin service resolved a payout")
	}
	if got := h.tx(tx.ID); got.Status != models.StatusPending {
		t.Fatalf("withdrawal is %q", got.Status)
	}
	h.assertReconciled(accountID)
}

// The operator's view: what is open, what is unknown, and what nothing is looking
// at any more — with the account and the request id they need to check the portal.
func TestPayoutListShowsAnOperatorWhatToActOn(t *testing.T) {
	h := newHarness(t)

	// Three accounts, not three withdrawals on one: an account may have only one
	// withdrawal in flight (migration 0008), and the list is cross-account anyway,
	// so this exercises that too.
	settledUser, settledAccount := h.verifiedWithBalance(500)
	inFlightUser, inFlightAccount := h.verifiedWithBalance(500)
	unknownUser, unknownAccount := h.verifiedWithBalance(500)

	settledTx, sp := h.stuckPayout(settledUser, settledAccount, 100)
	if err := h.deliverPayoutResult(sp.OriginatorConversationID, "0", "processed", "QK1", mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	inFlightTx, _ := h.stuckPayout(inFlightUser, inFlightAccount, 100)
	h.payoutSender.fail(errors.New("b2c send: connection reset"))
	unknownTx, _ := h.stuckPayout(unknownUser, unknownAccount, 100)
	h.payoutSender.fail(nil)
	up := h.payoutFor(unknownTx.ID)
	h.agePayout(up.OriginatorConversationID, "interval '25 hours'")
	h.duePayout(up.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)

	ids := func(filter string) map[uuid.UUID]bool {
		page, err := h.admin.Payouts(h.ctx, filter, 50, 0)
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]bool{}
		for _, p := range page.Items {
			out[p.TransactionID] = true
		}
		return out
	}

	open := ids("open")
	if !open[inFlightTx.ID] || !open[unknownTx.ID] || open[settledTx.ID] {
		t.Fatalf("the open filter returned %v; want the in-flight and unknown ones and not the settled one", open)
	}
	if u := ids("unknown"); !u[unknownTx.ID] || u[inFlightTx.ID] {
		t.Fatalf("the unknown filter returned %v, want only the unknown payout", u)
	}
	if g := ids("given_up"); !g[unknownTx.ID] || g[inFlightTx.ID] {
		t.Fatalf("the given-up filter returned %v, want only the one nothing is checking", g)
	}
	if s := ids("settled"); !s[settledTx.ID] || s[inFlightTx.ID] {
		t.Fatalf("the settled filter returned %v", s)
	}

	// And the row carries what an operator needs to check the portal and to know
	// whether anyone is still looking.
	page, err := h.admin.Payouts(h.ctx, "unknown", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	row := page.Items[0]
	if row.OriginatorConversationID == "" {
		t.Fatal("no request id on the row; there is nothing to search the portal for")
	}
	if row.AccountNumber == "" || row.UserPhone == "" || row.PhoneNumber == "" {
		t.Fatalf("row is missing identity: %+v", row)
	}
	if row.LedgerStatus != models.StatusPending {
		t.Fatalf("ledger status %q, want pending — the withdrawal is still out", row.LedgerStatus)
	}
	if !row.GivenUp {
		t.Fatal("the row does not report that nothing is checking it any more")
	}
	if page.Total != 1 {
		t.Fatalf("total %d, want 1", page.Total)
	}

	// The default filter is the one an operator opens the screen for.
	byDefault, err := h.admin.Payouts(h.ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if byDefault.Total != 2 {
		t.Fatalf("the default filter returned %d rows, want the 2 open ones", byDefault.Total)
	}
	for _, acct := range []uuid.UUID{settledAccount, inFlightAccount, unknownAccount} {
		h.assertReconciled(acct)
	}
}

// An operator resolving a payout the pass had given up on: the two halves of CP3
// and CP4 meeting, which is the case the whole checkpoint exists for.
func TestOperatorResolvesWhatThePassGaveUpOn(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)
	adminID := h.newAdmin()

	h.payoutSender.fail(errors.New("b2c send: i/o timeout"))
	tx, p := h.stuckPayout(userID, accountID, 100)
	h.payoutSender.fail(nil)

	h.agePayout(p.OriginatorConversationID, "interval '25 hours'")
	h.duePayout(p.OriginatorConversationID)
	h.payouts.Reconcile(h.ctx)

	row := h.payoutFor(tx.ID)
	if row.NextQueryAt != nil || row.Status != models.PayoutUnknown {
		t.Fatalf("payout is %q scheduled %v, want unknown and given up", row.Status, row.NextQueryAt)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s — the pass refunded on give-up", w.RealBalance)
	}

	// The operator checks the portal and finds nothing.
	if _, err := h.admin.ResolvePayout(h.ctx, adminID, tx.ID, service.PayoutOutcomeNeverReceived, payoutReason); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := h.tx(tx.ID); got.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q", got.Status)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after the operator refunded it, want 500", w.RealBalance)
	}
	if final := h.payoutFor(tx.ID); final.Status != models.PayoutNeverReceived {
		t.Fatalf("payout is %q", final.Status)
	}
	if n, err := h.store.Payouts.CountGivenUp(h.ctx); err != nil || n != 0 {
		t.Fatalf("given-up count %d after the operator settled it, want 0", n)
	}
	h.assertReconciled(accountID)
}

// The other way a customer learns: the settlement notification.
//
// The withdrawal sheet closes as soon as the request is sent, and its copy now
// promises we will say when M-Pesa confirms. An outcome that announced nothing
// would make that copy false, so this asserts the announcement rather than
// trusting it.
//
// It builds its own wallet and payout pair, because the shared harness gives the
// wallet a no-op broadcaster — there is nothing to observe on it.
func TestWithdrawalOutcomeIsAnnouncedToTheCustomer(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	events := &captureBroadcaster{}
	sender := &stubPayoutSender{}
	wallet := service.NewWalletService(h.store, h.mpesa, events, h.cfg, h.log)
	wallet.SetWithdrawalFlags(h.settings)
	payouts := service.NewMpesaPayoutService(h.store, h.cfg, h.log)
	payouts.SetSenderForTest(sender)
	wallet.SetPayouts(payouts)

	withdraw := func(usd int64) *models.Transaction {
		t.Helper()
		tx, err := wallet.Withdraw(h.ctx, service.WithdrawInput{
			UserID: userID, AccountID: accountID,
			AmountUSD: decimal.NewFromInt(usd), PhoneNumber: "0712345678",
		})
		if err != nil {
			t.Fatal(err)
		}
		return tx
	}
	lastAnnouncement := func() map[string]any {
		t.Helper()
		all := events.ofType("transaction")
		if len(all) == 0 {
			t.Fatal("nothing was announced")
		}
		payload, ok := all[len(all)-1].Payload.(map[string]any)
		if !ok {
			t.Fatalf("announcement payload is %T", all[len(all)-1].Payload)
		}
		return payload
	}

	// Paid.
	paid := withdraw(100)
	before := len(events.ofType("transaction"))
	if err := payouts.HandleResult(h.ctx,
		payoutResultBody(h.payoutFor(paid.ID).OriginatorConversationID, "0", "processed", "QK1"),
		mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if len(events.ofType("transaction")) <= before {
		t.Fatal("a paid withdrawal announced nothing; the sheet's promise that we would say so is false")
	}
	got := lastAnnouncement()
	if got["status"] != models.StatusCompleted || got["type"] != models.TxWithdrawal {
		t.Fatalf("announcement %v, want a completed withdrawal", got)
	}
	if got["receipt"] != "QK1" {
		t.Fatalf("announcement carries receipt %v, want the M-Pesa one", got["receipt"])
	}

	// Failed, with a reason from the payout vocabulary rather than the deposit's.
	failed := withdraw(100)
	before = len(events.ofType("transaction"))
	if err := payouts.HandleResult(h.ctx,
		payoutResultBody(h.payoutFor(failed.ID).OriginatorConversationID, "1", "insufficient", ""),
		mpesaclient.ArrivedAtResultURL); err != nil {
		t.Fatal(err)
	}
	if len(events.ofType("transaction")) <= before {
		t.Fatal("a failed withdrawal announced nothing")
	}
	got = lastAnnouncement()
	if got["status"] != models.StatusFailed {
		t.Fatalf("announcement %v, want a failed withdrawal", got)
	}
	reason, _ := got["reason"].(string)
	if reason == "" {
		t.Fatal("a failed withdrawal was announced with no reason")
	}
	if reason == "Your M-Pesa balance was too low to complete this deposit" {
		t.Fatalf("code 1 announced with the deposit copy: %q — it blames the customer's balance "+
			"when ours was what ran out", reason)
	}
	h.assertReconciled(accountID)
}
