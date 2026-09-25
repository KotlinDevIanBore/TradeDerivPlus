//go:build integration

package service_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// CP4 of the STK reconciliation plan: the console settles a stuck deposit's
// ORIGINAL row, through the callback's own path. What these tests protect is
// the reason that design exists — a callback that turns up afterwards must
// credit nothing.

// stuckDeposit starts a deposit the mock will never call back for.
func (h *harness) stuckDeposit(userID, accountID uuid.UUID, kes int64) (uuid.UUID, string) {
	h.t.Helper()
	resp, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(kes), PhoneNumber: service.MockStuckPhone,
	})
	if err != nil {
		h.t.Fatalf("start stuck deposit: %v", err)
	}
	return resp.TransactionID, resp.CheckoutRequestID
}

func (h *harness) auditRows(action string) []models.AuditEntry {
	h.t.Helper()
	rows, _, err := h.store.Audit.List(h.ctx, nil, action, 50, 0)
	if err != nil {
		h.t.Fatalf("audit list: %v", err)
	}
	return rows
}

const goodReason = "Customer sent the M-Pesa SMS; receipt confirmed on the Daraja portal"

func TestResolveSettlesTheOriginalRowAndALateCallbackCreditsNothing(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	adminID := h.newAdmin()
	txID, checkoutID := h.stuckDeposit(userID, accountID, 12950)

	tx, err := h.admin.ResolveDeposit(h.ctx, adminID, txID, service.DepositOutcomeCompleted, goodReason)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	// The original row, settled — not a new one beside it.
	if tx.ID != txID || tx.Status != models.StatusCompleted {
		t.Fatalf("resolve returned %s %q, want the original %s completed", tx.ID, tx.Status, txID)
	}
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByAdmin {
		t.Fatalf("settled_by %q, want admin", by)
	}
	if got, _ := tx.Metadata["admin_id"].(string); got != adminID.String() {
		t.Fatalf("admin_id %q on the ledger row, want %s — the console's join has nothing to name", got, adminID)
	}
	if _, has := tx.Metadata["receipt"]; has {
		t.Fatalf("a resolved deposit carries a receipt %v — nothing should be synthesised", tx.Metadata["receipt"])
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after resolve, want 100", w.RealBalance)
	}
	req := h.mpesaRow(checkoutID)
	if req.Status != models.StatusCompleted || req.ResultCode == nil || *req.ResultCode != 0 {
		t.Fatalf("mpesa row %q code %v, want completed / 0 — the guard row was not settled", req.Status, req.ResultCode)
	}

	// Audited, under its own action, with the reason and the reference.
	rows := h.auditRows(models.ActionDepositResolve)
	if len(rows) != 1 || rows[0].TargetID != txID || rows[0].Reason != goodReason || rows[0].AdminID != adminID {
		t.Fatalf("audit rows %+v, want one for %s by %s with the reason", rows, txID, adminID)
	}
	if got, _ := rows[0].Metadata["checkout_request_id"].(string); got != checkoutID {
		t.Fatalf("audit metadata carries checkout id %q, want %s", got, checkoutID)
	}

	// The console's list names the operator on the row, the way it does for an
	// adjustment.
	page, err := h.admin.Transactions(h.ctx, checkoutID, "", "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].AdminUsername == nil {
		t.Fatalf("console row for the resolved deposit: %+v, want the admin's username on it", page.Items)
	}

	// The point of all of it: Safaricom finally delivers, saying paid.
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, 0, "processed", "QKLATE0001")); err != nil {
		t.Fatalf("late callback must be acknowledged, got %v", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after the late callback, want still 100 — the deposit was credited twice", w.RealBalance)
	}
	after := h.tx(txID)
	if by, _ := after.Metadata["settled_by"].(string); by != service.SettledByAdmin {
		t.Fatalf("settled_by became %q after the late callback, want admin — the callback rewrote a settled row", by)
	}
	if _, has := after.Metadata["receipt"]; has {
		t.Fatal("the late callback's receipt landed on a row it did not settle")
	}
	h.assertReconciled(accountID)
}

// Closing a deposit as not received credits nothing, tells the user in words
// they can act on, and — if Safaricom later says paid — still credits nothing;
// that callback is the ERROR line settle logs, and a person's to reconcile.
func TestResolveAsNotReceivedCreditsNothing(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	adminID := h.newAdmin()
	txID, checkoutID := h.stuckDeposit(userID, accountID, 12950)

	tx, err := h.admin.ResolveDeposit(h.ctx, adminID, txID, service.DepositOutcomeFailed, "Not on the Daraja portal after 48h; customer confirms the PIN prompt never arrived")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if tx.Status != models.StatusFailed {
		t.Fatalf("status %q, want failed", tx.Status)
	}
	if reason, _ := tx.Metadata["reason"].(string); reason == "" || reason[:17] != "Closed by support" {
		t.Fatalf("user-facing reason %q, want it to say support closed it", reason)
	}
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByAdmin {
		t.Fatalf("settled_by %q, want admin", by)
	}
	req := h.mpesaRow(checkoutID)
	if req.Status != models.StatusFailed || req.ResultCode == nil || *req.ResultCode != service.ResultCodeAdminFailed {
		t.Fatalf("mpesa row %q code %v, want failed / %d", req.Status, req.ResultCode, service.ResultCodeAdminFailed)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("closing as not received credited %s", w.Total())
	}
	if rows := h.auditRows(models.ActionDepositResolve); len(rows) != 1 || rows[0].Metadata["outcome"] != service.DepositOutcomeFailed {
		t.Fatalf("audit rows %+v, want one with outcome failed", rows)
	}

	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, 0, "processed", "QKLATE0002")); err != nil {
		t.Fatalf("late callback must be acknowledged, got %v", err)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("a callback after the operator closed the row credited %s", w.Total())
	}
	if tx := h.tx(txID); tx.Status != models.StatusFailed {
		t.Fatalf("row is %q after the late callback, want still failed", tx.Status)
	}
	h.assertReconciled(accountID)
}

func TestResolveRequiresAReasonAndAValidOutcome(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	adminID := h.newAdmin()
	txID, _ := h.stuckDeposit(userID, accountID, 5000)

	var ve *service.ValidationError
	_, err := h.admin.ResolveDeposit(h.ctx, adminID, txID, service.DepositOutcomeCompleted, "ok")
	if !errors.As(err, &ve) || ve.Field != "reason" {
		t.Fatalf("short reason: got %v, want a validation error on reason", err)
	}
	_, err = h.admin.ResolveDeposit(h.ctx, adminID, txID, "maybe", goodReason)
	if !errors.As(err, &ve) || ve.Field != "outcome" {
		t.Fatalf("bad outcome: got %v, want a validation error on outcome", err)
	}

	if tx := h.tx(txID); tx.Status != models.StatusPending {
		t.Fatalf("a refused resolve moved the row to %q", tx.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("a refused resolve credited %s", w.Total())
	}
	if rows := h.auditRows(models.ActionDepositResolve); len(rows) != 0 {
		t.Fatalf("a refused resolve was audited: %+v", rows)
	}
}

func TestResolveRefusesAnythingButAPendingDeposit(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	adminID := h.newAdmin()

	// Already settled by the callback.
	doneID, doneCheckout := h.pendingDeposit(userID, accountID, 12950)
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(doneCheckout, 0, "processed", "QK00000030")); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ResolveDeposit(h.ctx, adminID, doneID, service.DepositOutcomeCompleted, goodReason); !errors.Is(err, service.ErrDepositSettled) {
		t.Fatalf("resolving a settled deposit: got %v, want ErrDepositSettled", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s, want the callback's 100 and nothing more", w.RealBalance)
	}

	// Not a deposit.
	adj, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(5),
		BalanceType: models.BalanceReal, Reason: "Goodwill credit for the test",
	})
	if err != nil {
		t.Fatal(err)
	}
	var ve *service.ValidationError
	if _, err := h.admin.ResolveDeposit(h.ctx, adminID, adj.ID, service.DepositOutcomeCompleted, goodReason); !errors.As(err, &ve) || ve.Field != "transaction" {
		t.Fatalf("resolving an adjustment: got %v, want a validation error on transaction", err)
	}

	// Unknown.
	if _, err := h.admin.ResolveDeposit(h.ctx, adminID, uuid.New(), service.DepositOutcomeCompleted, goodReason); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("resolving an unknown id: got %v, want ErrNotFound", err)
	}

	if rows := h.auditRows(models.ActionDepositResolve); len(rows) != 0 {
		t.Fatalf("refused resolves were audited: %+v", rows)
	}
	h.assertReconciled(accountID)
}

// The callback lands between the operator reading the row and confirming.
// The resolve loses the guard, changes nothing, and says so; the callback's
// credit stands alone, and no audit row claims an operator did it.
type raceResolver struct {
	h    *harness
	real interface {
		ResolveByAdmin(context.Context, string, bool, uuid.UUID) (bool, error)
	}
}

func (r raceResolver) ResolveByAdmin(ctx context.Context, checkoutID string, received bool, adminID uuid.UUID) (bool, error) {
	if err := r.h.mpesa.HandleCallback(ctx, darajaCallback(checkoutID, 0, "processed", "QKRACE0001")); err != nil {
		r.h.t.Errorf("callback inside the window: %v", err)
	}
	return r.real.ResolveByAdmin(ctx, checkoutID, received, adminID)
}

func TestResolveLosesToACallbackThatLandsFirst(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	adminID := h.newAdmin()
	txID, _ := h.stuckDeposit(userID, accountID, 12950)
	h.admin.SetDepositResolver(raceResolver{h: h, real: h.mpesa})
	defer h.admin.SetDepositResolver(h.mpesa)

	_, err := h.admin.ResolveDeposit(h.ctx, adminID, txID, service.DepositOutcomeCompleted, goodReason)
	if !errors.Is(err, service.ErrDepositSettled) {
		t.Fatalf("resolve racing a callback: got %v, want ErrDepositSettled", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s, want exactly the callback's 100", w.RealBalance)
	}
	tx := h.tx(txID)
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByCallback {
		t.Fatalf("settled_by %q, want callback — the losing resolve overwrote it", by)
	}
	if rows := h.auditRows(models.ActionDepositResolve); len(rows) != 0 {
		t.Fatalf("a resolve that changed nothing was audited: %+v", rows)
	}
	h.assertReconciled(accountID)
}

// The fixture the browser test and the console demo stand on: this one number
// is never called back in mock mode, so a deposit to it stays pending.
func TestMockNeverCallsBackTheStuckPhone(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.stuckDeposit(userID, accountID, 12950)

	time.Sleep(10 * time.Second) // simulateCallback would have landed by 9s
	if tx := h.tx(txID); tx.Status != models.StatusPending {
		t.Fatalf("stuck-phone deposit is %q after 10s, want pending", tx.Status)
	}
	if req := h.mpesaRow(checkoutID); req.Status != models.StatusPending {
		t.Fatalf("mpesa row is %q, want pending", req.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("credited %s", w.Total())
	}
}
