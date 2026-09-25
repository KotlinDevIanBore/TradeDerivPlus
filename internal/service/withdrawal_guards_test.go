//go:build integration

package service_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// CP0 of the B2C withdrawal work: close the holes that exist whether or not a
// payout is ever built, and make the refund arithmetic real before anything can
// trigger it.

// verifiedWithBalance is a KYC-verified real account holding usd, with a ledger
// row to match so the account starts reconciled.
func (h *harness) verifiedWithBalance(usd int64) (userID, accountID uuid.UUID) {
	h.t.Helper()
	userID, accountID = h.newUser(decimal.Zero)
	if _, err := h.store.Wallets.Adjust(h.ctx, accountID, models.BalanceReal, decimal.NewFromInt(usd)); err != nil {
		h.t.Fatal(err)
	}
	ref := "test-topup"
	if err := h.store.Transactions.Insert(h.ctx, &models.Transaction{
		AccountID: accountID, Type: models.TxDeposit, BalanceType: models.BalanceReal,
		Amount: decimal.NewFromInt(usd), Status: models.StatusCompleted, Reference: &ref,
	}); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.pool.Exec(h.ctx, `UPDATE users SET kyc_status = 'verified' WHERE id = $1`, userID); err != nil {
		h.t.Fatal(err)
	}
	h.assertReconciled(accountID)
	return userID, accountID
}

func (h *harness) withdraw(userID, accountID uuid.UUID, usd int64) (*models.Transaction, error) {
	h.t.Helper()
	return h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(usd), PhoneNumber: "0712345678",
	})
}

// The platform gate. It comes before every user-specific check, because while
// withdrawals are off nothing about the request can make one happen — and it is
// off by default, since the payout does not exist yet.
func TestWithdrawalsAreRefusedWhileTheFeatureIsOff(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	if err := h.store.Settings.Set(h.ctx, "withdrawals_enabled", "false"); err != nil {
		t.Fatal(err)
	}
	h.settings.BustCache()

	_, err := h.withdraw(userID, accountID, 100)
	if !errors.Is(err, service.ErrWithdrawalsDisabled) {
		t.Fatalf("withdrawal with the feature off returned %v, want ErrWithdrawalsDisabled", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("a refused withdrawal moved the balance to %s", w.RealBalance)
	}
	if n := h.countWithdrawals(accountID); n != 0 {
		t.Fatalf("a refused withdrawal wrote %d ledger rows", n)
	}

	// And it is the platform's answer, not the user's: an unverified user with a
	// demo account gets the same refusal rather than a hint about themselves.
	otherUser, otherAccount := h.newUser(decimal.Zero)
	if _, err := h.withdraw(otherUser, otherAccount, 10); !errors.Is(err, service.ErrWithdrawalsDisabled) {
		t.Fatalf("unverified demo user with the feature off returned %v, want ErrWithdrawalsDisabled", err)
	}

	// Turned on, the same request reaches the real gates again.
	if err := h.store.Settings.Set(h.ctx, "withdrawals_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	h.settings.BustCache()
	if _, err := h.withdraw(userID, accountID, 100); err != nil {
		t.Fatalf("withdrawal with the feature on: %v", err)
	}
	h.assertReconciled(accountID)
}

// A wallet service with no flag source refuses rather than assuming the feature
// is on. Fail closed: the wrong direction pays real money out.
func TestWithdrawalRefusedWithNoFlagSourceWired(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	lone := service.NewWalletService(h.store, h.mpesa, noopBroadcaster{}, h.cfg, h.log)
	if _, err := lone.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(100), PhoneNumber: "0712345678",
	}); !errors.Is(err, service.ErrWithdrawalsDisabled) {
		t.Fatalf("unwired wallet returned %v, want ErrWithdrawalsDisabled", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s, want untouched 500", w.RealBalance)
	}
}

// Per-transaction and per-day ceilings, both in KES. Before this a withdrawal
// had no upper bound at all, and Daraja's own limits would have refused the
// payout after the customer was already debited.
func TestWithdrawalCeilings(t *testing.T) {
	h := newHarness(t)
	h.cfg.MaxWithdrawKES = 12950    // $100 at the harness rate
	h.cfg.MaxWithdrawDayKES = 25900 // $200
	userID, accountID := h.verifiedWithBalance(500)

	var ve *service.ValidationError

	// Over the per-transaction ceiling.
	if _, err := h.withdraw(userID, accountID, 101); !errors.As(err, &ve) || ve.Field != "amount" {
		t.Fatalf("withdrawing $101 against a KES 12,950 ceiling returned %v, want a validation error on amount", err)
	}
	if n := h.countWithdrawals(accountID); n != 0 {
		t.Fatalf("a refused withdrawal wrote %d rows", n)
	}

	// At the ceiling, allowed.
	first, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatalf("withdrawing exactly the ceiling: %v", err)
	}
	if _, err := h.wallet.FailWithdrawal(h.ctx, first.ID, "make room", service.WithdrawalFailedByAdmin); err != nil {
		t.Fatal(err)
	}

	// The refunded one still counts toward nothing — it failed, so the day is
	// clear again. Take the ceiling twice to reach the daily cap.
	second, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatalf("second withdrawal: %v", err)
	}
	if _, err := h.wallet.FailWithdrawal(h.ctx, second.ID, "make room", service.WithdrawalFailedByAdmin); err != nil {
		t.Fatal(err)
	}
	h.assertReconciled(accountID)
}

// The daily cap counts pending alongside completed, because a pending
// withdrawal is money already gone.
//
// Getting a test to distinguish that took some care, and the first attempt did
// not. It flipped the first withdrawal to 'completed' by hand to free the
// one-in-flight index — which meant the row counted either way and the test
// passed just as happily with the cap reading only completed rows.
//
// What distinguishes them is the ORDER of the two refusals. The cap is checked
// before the INSERT that the index guards, so when a request would breach both,
// the cap answers first. With a $100 withdrawal pending and a $150 daily cap, a
// second $100 request is over the cap (validation error) if pending counts, and
// merely a second in flight (ErrWithdrawalInFlight) if it does not. So the two
// readings produce two different errors, and the test can tell them apart.
//
// A consequence worth knowing: when both apply, the customer is told about the
// daily limit rather than about the withdrawal already on its way. For a smaller
// second request — the common case — the cap has headroom and the in-flight
// message is what they get, which is the more actionable of the two.
func TestWithdrawalDailyCapCountsPendingToo(t *testing.T) {
	h := newHarness(t)
	h.cfg.MaxWithdrawKES = 12950    // $100
	h.cfg.MaxWithdrawDayKES = 19425 // $150
	userID, accountID := h.verifiedWithBalance(500)

	first, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if first.Status != models.StatusPending {
		t.Fatalf("first withdrawal is %q, want pending", first.Status)
	}

	// Still pending, deliberately. $100 pending + $100 requested is over the
	// $150 cap, so the cap must be what refuses this.
	var ve *service.ValidationError
	_, err = h.withdraw(userID, accountID, 100)
	if !errors.As(err, &ve) || ve.Field != "amount" {
		t.Fatalf("a second $100 against a $150 daily cap with $100 already pending returned %v, "+
			"want a validation error on amount — the cap is not counting the pending withdrawal", err)
	}

	// A smaller request fits under the cap, and then meets the index instead —
	// which is the ordering the comment above describes.
	if _, err := h.withdraw(userID, accountID, 10); !errors.Is(err, service.ErrWithdrawalInFlight) {
		t.Fatalf("a $10 request with headroom under the cap returned %v, want ErrWithdrawalInFlight", err)
	}

	// Refund the pending one and the day is clear again: a failed withdrawal
	// returned the money, so it must not count toward the cap either.
	if _, err := h.wallet.FailWithdrawal(h.ctx, first.ID, "not received", service.WithdrawalFailedByAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := h.withdraw(userID, accountID, 100); err != nil {
		t.Fatalf("withdrawing $100 after the pending one was refunded: %v", err)
	}
	h.assertReconciled(accountID)
}

// One withdrawal in flight per account, enforced by the database. This is the
// guard that makes a double-submitted payout impossible rather than unlikely,
// and it releases as soon as the withdrawal settles.
func TestOnlyOneWithdrawalInFlightPerAccount(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	first, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := h.withdraw(userID, accountID, 100); !errors.Is(err, service.ErrWithdrawalInFlight) {
		t.Fatalf("second withdrawal returned %v, want ErrWithdrawalInFlight", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after one accepted withdrawal, want 400 — the refused one debited", w.RealBalance)
	}
	if n := h.countWithdrawals(accountID); n != 1 {
		t.Fatalf("%d withdrawal rows, want 1", n)
	}

	// Settling it frees the slot.
	if _, err := h.wallet.FailWithdrawal(h.ctx, first.ID, "not received", service.WithdrawalFailedByAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := h.withdraw(userID, accountID, 100); err != nil {
		t.Fatalf("withdrawal after the first settled: %v", err)
	}

	// And the constraint is per account, not global: another account is free.
	otherUser, otherAccount := h.verifiedWithBalance(500)
	if _, err := h.withdraw(otherUser, otherAccount, 100); err != nil {
		t.Fatalf("another account's withdrawal was refused: %v", err)
	}
	h.assertReconciled(accountID)
	h.assertReconciled(otherAccount)
}

// The refund, which is the arithmetic this checkpoint had to settle: the
// guarded flip to failed plus the wallet credit, and nothing else. A third
// compensating row would credit the ledger twice.
func TestFailWithdrawalRefundsExactlyOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	tx, err := h.withdraw(userID, accountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("balance %s after the debit, want 400", w.RealBalance)
	}
	before := h.countRows(accountID)

	won, err := h.wallet.FailWithdrawal(h.ctx, tx.ID, "Daraja said the recipient is not registered", service.WithdrawalFailedByPayout)
	if err != nil {
		t.Fatalf("refund: %v", err)
	}
	if !won {
		t.Fatal("the first refund reported that it did not perform the refund")
	}

	row := h.tx(tx.ID)
	if row.Status != models.StatusFailed {
		t.Fatalf("withdrawal is %q after the refund, want failed", row.Status)
	}
	if reason, _ := row.Metadata["reason"].(string); reason == "" {
		t.Fatalf("no reason on the refunded row: %v", row.Metadata)
	}
	if by, _ := row.Metadata["settled_by"].(string); by != service.WithdrawalFailedByPayout {
		t.Fatalf("settled_by %q, want %q", by, service.WithdrawalFailedByPayout)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after the refund, want the original 500", w.RealBalance)
	}
	// No third row: the flip and the credit are the whole refund.
	if after := h.countRows(accountID); after != before {
		t.Fatalf("the refund wrote %d extra ledger rows; a compensating row would credit the ledger twice", after-before)
	}
	// The invariant that would catch either half being wrong.
	h.assertReconciled(accountID)

	// A second refund — a retried payout result, or an operator after the fact —
	// credits nothing.
	won, err = h.wallet.FailWithdrawal(h.ctx, tx.ID, "again", service.WithdrawalFailedByAdmin)
	if err != nil {
		t.Fatalf("second refund errored: %v — it should be an acknowledged no-op", err)
	}
	if won {
		t.Fatal("the second refund reported that it performed the refund")
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("balance %s after a duplicate refund, want 500 — it credited twice", w.RealBalance)
	}
	if by, _ := h.tx(tx.ID).Metadata["settled_by"].(string); by != service.WithdrawalFailedByPayout {
		t.Fatalf("settled_by is now %q — the losing refund rewrote a settled row", by)
	}
	h.assertReconciled(accountID)
}

// Concurrent refunds of one withdrawal: one credit between them, not several.
// The same shape as the deposit path's double-credit test, on the way out.
func TestConcurrentRefundsCreditExactlyOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(1000)

	const callers = 8
	for attempt := 0; attempt < 5; attempt++ {
		tx, err := h.withdraw(userID, accountID, 100)
		if err != nil {
			t.Fatalf("attempt %d: %v", attempt, err)
		}

		start := make(chan struct{})
		results := make([]bool, callers)
		errs := make([]error, callers)
		done := make(chan struct{}, callers)
		for i := 0; i < callers; i++ {
			go func(i int) {
				defer func() { done <- struct{}{} }()
				<-start
				results[i], errs[i] = h.wallet.FailWithdrawal(h.ctx, tx.ID, "concurrent", service.WithdrawalFailedByPayout)
			}(i)
		}
		close(start)
		for i := 0; i < callers; i++ {
			<-done
		}

		won := 0
		for i := range results {
			if errs[i] != nil {
				t.Fatalf("attempt %d caller %d: %v", attempt, i, errs[i])
			}
			if results[i] {
				won++
			}
		}
		if won != 1 {
			t.Fatalf("attempt %d: %d of %d refunds claimed to have credited, want exactly 1", attempt, won, callers)
		}
		if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(1000)) {
			t.Fatalf("attempt %d: balance %s after %d concurrent refunds of one $100 withdrawal, want 1000",
				attempt, w.RealBalance, callers)
		}
		h.assertReconciled(accountID)
	}
}

// A withdrawal that is pending has moved money, so the dashboard must say so.
// Filtering on 'completed' made these tiles structurally zero while real
// balances were being debited.
func TestTodayTotalsCountsPendingWithdrawals(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.verifiedWithBalance(500)

	count, total, err := h.store.Transactions.TodayTotals(h.ctx, models.TxWithdrawal)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || !total.IsZero() {
		t.Fatalf("a fresh account shows %d withdrawals totalling %s", count, total)
	}

	if _, err := h.withdraw(userID, accountID, 100); err != nil {
		t.Fatal(err)
	}
	count, total, err = h.store.Transactions.TodayTotals(h.ctx, models.TxWithdrawal)
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 || !total.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("a pending withdrawal shows as %d totalling %s, want 1 totalling 100 — the tile cannot see money that has left", count, total)
	}

	// A refunded one stops counting, because the money came back.
	pending := h.pendingWithdrawalID(accountID)
	if _, err := h.wallet.FailWithdrawal(h.ctx, pending, "refunded", service.WithdrawalFailedByAdmin); err != nil {
		t.Fatal(err)
	}
	count, total, err = h.store.Transactions.TodayTotals(h.ctx, models.TxWithdrawal)
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 || !total.IsZero() {
		t.Fatalf("a refunded withdrawal still counts: %d totalling %s", count, total)
	}

	// Deposits are unchanged by all of this: only completed ones count.
	if _, _, err := h.store.Transactions.TodayTotals(h.ctx, models.TxDeposit); err != nil {
		t.Fatal(err)
	}
	h.assertReconciled(accountID)
}
