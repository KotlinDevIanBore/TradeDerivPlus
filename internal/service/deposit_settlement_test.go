//go:build integration

package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// CP0 of the STK reconciliation plan: the defects in the callback path that a
// reconciler would make more likely to bite, fixed before it exists.

// Deposit writes checkout_request_id into the ledger row after InitiateSTK
// returns. That write used to go through UpdateStatus and re-assert 'pending',
// so a settlement landing in the gap — a fast live callback, or later a boot-pass
// query — was reverted: wallet credited, ledger pending, and unrepairable,
// because the mpesa_requests row was already terminal and every later attempt
// lost the guard.
//
// The mock never shows this: its callback waits four seconds. So the test puts
// the settlement inside the gap deliberately, through a hook that runs at the
// end of InitiateSTK, before Deposit gets to its follow-up write.
func TestDepositMetadataWriteCannotRevertASettlement(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	// Settle synchronously, inside InitiateSTK's return path.
	h.mpesa.SetAfterInitiateForTest(func(checkoutID string) {
		if err := h.mpesa.HandleCallback(h.ctx,
			darajaCallback(checkoutID, 0, "processed", "QK00000001")); err != nil {
			t.Errorf("settle inside the gap: %v", err)
		}
	})
	defer h.mpesa.SetAfterInitiateForTest(nil)

	resp, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(12950), PhoneNumber: "0712345678",
	})
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// The follow-up write happened — the id is on the row — and it did not undo
	// the settlement that preceded it.
	tx, err := h.store.Transactions.GetByID(h.ctx, resp.TransactionID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != models.StatusCompleted {
		t.Fatalf("deposit is %q after a settlement landed inside Deposit's window, want completed — "+
			"the follow-up metadata write reverted it", tx.Status)
	}
	if got, _ := tx.Metadata["checkout_request_id"].(string); got != resp.CheckoutRequestID {
		t.Fatalf("checkout_request_id %q on the row, want %q", got, resp.CheckoutRequestID)
	}
	if receipt, _ := tx.Metadata["receipt"].(string); receipt != "QK00000001" {
		t.Fatalf("the metadata merge lost the receipt: %v", tx.Metadata)
	}

	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s, want 100", w.RealBalance)
	}
	// This is the line the bug fails on: a completed credit against a pending
	// row is money the ledger cannot account for.
	h.assertReconciled(accountID)
}

// settle must refuse when nothing is registered to receive its result, and it
// must refuse BEFORE consuming the guard. Winning UpdateResult with no handler
// leaves the mpesa row terminal and the ledger pending for ever — and every
// later attempt, including the real one, loses to the guard this one burned.
func TestSettleRefusesWithoutAResultHandler(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.pendingDeposit(userID, accountID, 12950)

	// A service with no wallet behind it — the state main.go is in between
	// NewMpesaService and NewWalletService.
	lone := service.NewMpesaService(h.store, h.cfg, h.log)

	err := lone.HandleCallback(h.ctx, darajaCallback(checkoutID, 0, "processed", "QK00000002"))
	if err == nil {
		t.Fatal("a settlement with no result handler was accepted")
	}

	// Nothing moved, and — the point — the guard is still there to be won.
	req, err := h.store.Mpesa.GetByCheckoutID(h.ctx, checkoutID)
	if err != nil {
		t.Fatal(err)
	}
	if req.Status != models.StatusPending {
		t.Fatalf("mpesa row is %q after a refused settle, want pending — the guard was consumed", req.Status)
	}
	tx, err := h.store.Transactions.GetByID(h.ctx, txID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != models.StatusPending {
		t.Fatalf("ledger row is %q after a refused settle, want pending", tx.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("a refused settle credited %s", w.Total())
	}

	// The real service, with its handler, settles the same callback normally.
	if err := h.mpesa.HandleCallback(h.ctx,
		darajaCallback(checkoutID, 0, "processed", "QK00000002")); err != nil {
		t.Fatalf("settle after the refusal: %v", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after the real settle, want 100 — the refusal burned the guard", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The pending screen shows the customer their CheckoutRequestID. Support could
// not search for it. A deposit settled without a receipt — by a query, or by an
// operator — has no other reference at all.
func TestSearchFindsADepositByCheckoutID(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.pendingDeposit(userID, accountID, 5000)

	page, err := h.admin.Transactions(h.ctx, checkoutID, "", "", 25, 0)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	found := false
	for _, item := range page.Items {
		if item.ID == txID {
			found = true
		}
	}
	if !found {
		t.Fatalf("searching %q returned %d rows and not the deposit", checkoutID, len(page.Items))
	}
}

// Safaricom says the customer paid; the row was already closed as failed. The
// guard holds — no credit — and the event is logged at ERROR rather than as a
// routine duplicate, because only a person can reconcile it. The harness logs
// to io.Discard, so what is asserted here is the money; the log line is by
// inspection.
func TestSucceedingCallbackOnAFailedRowIsLoggedNotCredited(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.pendingDeposit(userID, accountID, 12950)

	if err := h.mpesa.HandleCallback(h.ctx,
		darajaCallback(checkoutID, 1032, "Request cancelled by user", "")); err != nil {
		t.Fatalf("failure callback: %v", err)
	}
	if err := h.mpesa.HandleCallback(h.ctx,
		darajaCallback(checkoutID, 0, "processed", "QK00000003")); err != nil {
		t.Fatalf("a later success callback errored: %v — it should be acknowledged and logged", err)
	}

	tx, err := h.store.Transactions.GetByID(h.ctx, txID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != models.StatusFailed {
		t.Fatalf("row is %q, want failed — a losing callback rewrote a terminal row", tx.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("a callback that lost the guard credited %s", w.Total())
	}
	h.assertReconciled(accountID)
}

// Every terminal row says how it got there.
func TestSettledDepositsRecordTheirSource(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	okID, okCheckout := h.pendingDeposit(userID, accountID, 12950)
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(okCheckout, 0, "processed", "QK00000004")); err != nil {
		t.Fatal(err)
	}
	failID, failCheckout := h.pendingDeposit(userID, accountID, 1000)
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(failCheckout, 1032, "cancelled", "")); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		name string
		id   uuid.UUID
	}{{"completed", okID}, {"failed", failID}} {
		tx, err := h.store.Transactions.GetByID(h.ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByCallback {
			t.Fatalf("%s deposit has settled_by %q, want %q", c.name, by, service.SettledByCallback)
		}
	}
	h.assertReconciled(accountID)
}
