//go:build integration

package service_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

const testSymbol = "R_75"

// TestConcurrentCloseSettlesOnce is the core race: a user tapping Close while
// the stop-loss watcher is settling the same position. Exactly one must win,
// and the wallet must be credited exactly once.
func TestConcurrentCloseSettlesOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	h.setPrice(testSymbol, 100)
	trade, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// Move the price so the close has a non-zero result worth double-counting.
	h.setPrice(testSymbol, 110)

	const racers = 8
	var wg sync.WaitGroup
	results := make([]error, racers)
	start := make(chan struct{})

	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start // release them all at once
			_, results[i] = h.trading.Close(h.ctx, userID, trade.ID)
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range results {
		switch {
		case err == nil:
			succeeded++
		case err == service.ErrTradeNotOpen:
		default:
			t.Errorf("racer %d got unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d racers closed the position, want exactly 1", succeeded)
	}

	// The ledger must hold exactly one close row for this trade.
	var closes int
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM transactions WHERE reference = $1 AND type = 'trade_close'`,
		trade.ID.String()).Scan(&closes); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatalf("ledger has %d close rows, want 1", closes)
	}

	// 1000 - 100 stake + (100 stake + 10 pnl) = 1010
	w := h.wallet_(accountID)
	want := decimal.RequireFromString("1010")
	if got := w.RealBalance.Add(w.BonusBalance); !got.Equal(want) {
		t.Fatalf("balance %s, want %s", got, want)
	}
	h.assertReconciled(accountID)
}

// TestStopLossRacesManualClose runs the tick watcher against a user close on
// the same position. Whichever wins, the position settles once.
func TestStopLossRacesManualClose(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// The watcher only tracks positions that have levels set.
	h.trading.Start(h.ctx)
	h.setPrice(testSymbol, 100)

	sl := decimal.NewFromInt(95)
	trade, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
		StopLoss: &sl,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	var wg sync.WaitGroup
	start := make(chan struct{})

	// One goroutine drives the price through the stop loss, which the watcher
	// reacts to; the other closes manually at the same moment.
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		h.setPrice(testSymbol, 94)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, _ = h.trading.Close(h.ctx, userID, trade.ID)
	}()
	close(start)
	wg.Wait()

	// Give the watcher's goroutine a moment to finish its settle if it won.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		settled, err := h.store.Trades.GetByID(h.ctx, trade.ID)
		if err == nil && settled.Status == models.TradeClosed {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	settled, err := h.store.Trades.GetByID(h.ctx, trade.ID)
	if err != nil {
		t.Fatal(err)
	}
	if settled.Status != models.TradeClosed {
		t.Fatal("position never closed")
	}

	var closes int
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM transactions WHERE reference = $1 AND type = 'trade_close'`,
		trade.ID.String()).Scan(&closes); err != nil {
		t.Fatal(err)
	}
	if closes != 1 {
		t.Fatalf("ledger has %d close rows, want 1", closes)
	}
	h.assertReconciled(accountID)
}

// TestConcurrentOpensCannotOverspend is the balance race: twenty goroutines
// each try to stake 100 against a balance of 1000. Exactly ten may succeed.
func TestConcurrentOpensCannotOverspend(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	const attempts = 20
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = h.trading.Open(h.ctx, service.OpenTradeInput{
				UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
				Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
			})
		}(i)
	}
	close(start)
	wg.Wait()

	opened, refused := 0, 0
	for i, err := range errs {
		switch {
		case err == nil:
			opened++
		case err == service.ErrInsufficientFunds:
			refused++
		default:
			t.Errorf("attempt %d got unexpected error: %v", i, err)
		}
	}
	if opened != 10 || refused != 10 {
		t.Fatalf("%d opened and %d refused, want 10 and 10", opened, refused)
	}

	w := h.wallet_(accountID)
	if got := w.RealBalance.Add(w.BonusBalance); !got.IsZero() {
		t.Fatalf("balance %s after spending everything, want 0", got)
	}
	if w.RealBalance.IsNegative() || w.BonusBalance.IsNegative() {
		t.Fatalf("a balance went negative: real %s bonus %s", w.RealBalance, w.BonusBalance)
	}
	h.assertReconciled(accountID)
}

// TestConcurrentWithdrawalsYieldExactlyOnePending is the withdrawal path's
// version of the same race, and what it asserts changed when the one-in-flight
// constraint arrived (migration 0008).
//
// It used to assert that ten concurrent withdrawals of $100 against a $500
// balance produced exactly five successes — the overdraw guard holding under
// contention. That race is now unreachable: the partial unique index means an
// account can have one pending withdrawal at a time, so ten concurrent attempts
// produce one success and nine refusals, whatever the balance. Asserting five
// would be asserting the absence of the constraint.
//
// The overdraw guard has not stopped mattering, it has stopped being reachable
// from here, so it is asserted sequentially in the second half below — drain the
// balance one withdrawal at a time, refunding between, and the account must
// refuse the attempt that would take it negative rather than allow it.
func TestConcurrentWithdrawalsYieldExactlyOnePending(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	// Withdrawals draw on real balance and require verification.
	if _, err := h.store.Wallets.Adjust(h.ctx, accountID, models.BalanceReal, decimal.NewFromInt(500)); err != nil {
		t.Fatal(err)
	}
	ref := "test-topup"
	if err := h.store.Transactions.Insert(h.ctx, &models.Transaction{
		AccountID: accountID, Type: models.TxDeposit, BalanceType: models.BalanceReal,
		Amount: decimal.NewFromInt(500), Status: models.StatusCompleted, Reference: &ref,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE users SET kyc_status = 'verified' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}

	const attempts = 10
	var wg sync.WaitGroup
	errs := make([]error, attempts)
	start := make(chan struct{})

	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = h.wallet.Withdraw(h.ctx, service.WithdrawInput{
				UserID: userID, AccountID: accountID,
				AmountUSD: decimal.NewFromInt(100), PhoneNumber: "0712345678",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	succeeded := 0
	for i, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, service.ErrWithdrawalInFlight), errors.Is(err, service.ErrInsufficientFunds):
		default:
			t.Errorf("attempt %d got unexpected error: %v", i, err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("%d of %d concurrent withdrawals succeeded, want exactly 1 — the one-in-flight index is what makes a double-submitted payout impossible", succeeded, attempts)
	}

	w := h.wallet_(accountID)
	if !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("real balance %s after one $100 withdrawal of $500, want 400 — it was debited more than once", w.RealBalance)
	}
	h.assertReconciled(accountID)

	// The overdraw guard, sequentially, now that concurrency cannot reach it.
	// Each round refunds the previous withdrawal so the index is free again, so
	// the balance only falls when a withdrawal is left pending.
	pending := h.pendingWithdrawalID(accountID)
	for round := 0; round < 6; round++ {
		if _, err := h.wallet.FailWithdrawal(h.ctx, pending, "test refund", service.WithdrawalFailedByAdmin); err != nil {
			t.Fatalf("round %d refund: %v", round, err)
		}
		h.assertReconciled(accountID)

		// Take the whole balance, then try to take $100 more.
		balance := h.wallet_(accountID).RealBalance
		tx, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
			UserID: userID, AccountID: accountID,
			AmountUSD: balance, PhoneNumber: "0712345678",
		})
		if err != nil {
			t.Fatalf("round %d withdrawing the full balance %s: %v", round, balance, err)
		}
		if w := h.wallet_(accountID); !w.RealBalance.IsZero() {
			t.Fatalf("round %d: real balance %s after withdrawing all of it", round, w.RealBalance)
		}
		h.assertReconciled(accountID)

		// Refund it and try to take more than there is.
		if _, err := h.wallet.FailWithdrawal(h.ctx, tx.ID, "test refund", service.WithdrawalFailedByAdmin); err != nil {
			t.Fatal(err)
		}
		over := h.wallet_(accountID).RealBalance.Add(decimal.NewFromInt(1))
		if _, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
			UserID: userID, AccountID: accountID,
			AmountUSD: over, PhoneNumber: "0712345678",
		}); !errors.Is(err, service.ErrInsufficientFunds) {
			t.Fatalf("round %d: withdrawing %s of a %s balance returned %v, want ErrInsufficientFunds",
				round, over, h.wallet_(accountID).RealBalance, err)
		}
		if w := h.wallet_(accountID); w.RealBalance.IsNegative() {
			t.Fatalf("round %d: real balance went negative: %s", round, w.RealBalance)
		}
		h.assertReconciled(accountID)

		// Leave one pending for the next round's refund.
		next, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
			UserID: userID, AccountID: accountID,
			AmountUSD: decimal.NewFromInt(1), PhoneNumber: "0712345678",
		})
		if err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		pending = next.ID
	}
}

// TestFreezeRacesOpen is the race the freeze check exists to win. A freeze
// committing while a trade is in flight must either be invisible to that
// trade (it committed first) or must stop it. What must never happen is a
// trade opening AFTER a freeze has committed.
func TestFreezeRacesOpen(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	const rounds = 15
	for i := 0; i < rounds; i++ {
		// Re-open the account each round so both outcomes stay reachable.
		if _, err := h.admin.Unfreeze(h.ctx, adminID, accountID, "resetting for the next round"); err != nil {
			t.Fatalf("round %d unfreeze: %v", i, err)
		}

		var wg sync.WaitGroup
		var openErr error
		start := make(chan struct{})

		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, openErr = h.trading.Open(h.ctx, service.OpenTradeInput{
				UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
				Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
			})
		}()
		go func() {
			defer wg.Done()
			<-start
			_, _ = h.admin.Freeze(h.ctx, adminID, accountID, "freezing while a trade is in flight")
		}()
		close(start)
		wg.Wait()

		// Both outcomes are correct; only their combination with the final
		// account state is not.
		if openErr != nil && openErr != service.ErrAccountFrozen {
			t.Fatalf("round %d: unexpected open error: %v", i, openErr)
		}

		account, err := h.store.Accounts.GetByID(h.ctx, accountID)
		if err != nil {
			t.Fatal(err)
		}
		if openErr == nil && !account.IsFrozen() {
			t.Fatalf("round %d: trade opened and the freeze did not land", i)
		}
		h.assertReconciled(accountID)
	}

	// Once frozen and settled, a trade must be refused every time.
	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, "final freeze for the deterministic check"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		_, err := h.trading.Open(h.ctx, service.OpenTradeInput{
			UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
			Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
		})
		if err != service.ErrAccountFrozen {
			t.Fatalf("trade on a settled frozen account returned %v, want ErrAccountFrozen", err)
		}
	}
	h.assertReconciled(accountID)
}

// TestFreezeBlocksDepositAndWithdrawal covers the other two money paths the
// freeze guards.
func TestFreezeBlocksDepositAndWithdrawal(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.Zero)

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(500),
		BalanceType: models.BalanceReal, Reason: "funding the withdrawal test",
	}); err != nil {
		t.Fatalf("seed balance: %v", err)
	}
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE users SET kyc_status = 'verified' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, "freezing to check the money paths"); err != nil {
		t.Fatal(err)
	}

	if _, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(1000), PhoneNumber: "0712345678",
	}); err != service.ErrAccountFrozen {
		t.Errorf("deposit on a frozen account returned %v, want ErrAccountFrozen", err)
	}

	if _, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(100), PhoneNumber: "0712345678",
	}); err != service.ErrAccountFrozen {
		t.Errorf("withdrawal on a frozen account returned %v, want ErrAccountFrozen", err)
	}

	h.assertReconciled(accountID)
}

// TestAdjustmentSucceedsOnFrozenAccount pins a deliberate decision.
//
// An admin adjustment is NOT blocked by a freeze. The freeze exists to stop the
// account moving on its own; freezing an account and then being unable to
// correct the very balance that caused the freeze would be a dead end for
// support.
//
// If this test fails, someone has added the freeze guard to AdminService.Adjust
// "for consistency". That is the regression this test exists to catch: read the
// comment on Adjust before changing either.
func TestAdjustmentSucceedsOnFrozenAccount(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	_, accountID := h.newUser(decimal.NewFromInt(100))

	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, "frozen pending an investigation"); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	tx, wallet, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(25),
		BalanceType: models.BalanceReal, Reason: "correcting the balance that caused the freeze",
	})
	if err != nil {
		t.Fatalf("adjustment on a frozen account was refused: %v -- see the comment on AdminService.Adjust", err)
	}
	if tx.Type != models.TxAdminAdjustment {
		t.Fatalf("ledger row type %q, want admin_adjustment", tx.Type)
	}
	if !wallet.RealBalance.Equal(decimal.NewFromInt(25)) {
		t.Fatalf("real balance %s, want 25", wallet.RealBalance)
	}

	// And the account is still frozen afterwards: the adjustment does not
	// quietly thaw it.
	account, err := h.store.Accounts.GetByID(h.ctx, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if !account.IsFrozen() {
		t.Fatal("the adjustment unfroze the account")
	}
	h.assertReconciled(accountID)
}

// TestConcurrentAdjustmentsStayConsistent runs many adjustments at once, since
// support tooling is exactly where a double-click becomes a double-credit.
func TestConcurrentAdjustmentsStayConsistent(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	_, accountID := h.newUser(decimal.Zero)

	const n = 20
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make([]error, n)

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = h.admin.Adjust(h.ctx, service.AdjustInput{
				AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(10),
				BalanceType: models.BalanceReal, Reason: "concurrent adjustment consistency check",
			})
		}(i)
	}
	close(start)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("adjustment %d failed: %v", i, err)
		}
	}

	w := h.wallet_(accountID)
	if want := decimal.NewFromInt(200); !w.RealBalance.Equal(want) {
		t.Fatalf("real balance %s after 20 credits of 10, want %s", w.RealBalance, want)
	}

	// Every adjustment must have left an audit row.
	var audits int
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM admin_audit_log WHERE action = 'manual_adjustment'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != n {
		t.Fatalf("%d audit rows for %d adjustments", audits, n)
	}
	h.assertReconciled(accountID)
}
