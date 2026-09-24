//go:build integration

package service_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// The deposit path is the one that moves outside money in, so it is exercised
// end to end against a real database rather than only through the HTTP smoke
// test.

func TestDepositLifecycleCreditsExactlyOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	settled := make(chan string, 4)
	h.wallet.OnDepositSettled(func(status string) { settled <- status })

	resp, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(12950), PhoneNumber: "0712345678",
	})
	if err != nil {
		t.Fatalf("deposit: %v", err)
	}

	// 12,950 KES at 129.50 to the dollar is exactly $100.
	if want := decimal.NewFromInt(100); !resp.AmountUSD.Equal(want) {
		t.Fatalf("converted to %s, want %s", resp.AmountUSD, want)
	}
	if resp.Status != models.StatusPending {
		t.Fatalf("deposit started as %q, want pending", resp.Status)
	}

	// Nothing has moved yet: a pending deposit must not credit the wallet.
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("balance %s before the callback, want 0", w.Total())
	}
	h.assertReconciled(accountID)

	// The mock M-Pesa client settles on its own after a few seconds.
	select {
	case status := <-settled:
		t.Logf("settled as %s", status)
	case <-time.After(20 * time.Second):
		t.Fatal("the simulated callback never arrived")
	}

	tx, err := h.wallet.Transaction(h.ctx, userID, resp.TransactionID)
	if err != nil {
		t.Fatalf("read transaction: %v", err)
	}

	w := h.wallet_(accountID)
	switch tx.Status {
	case models.StatusCompleted:
		if !w.RealBalance.Equal(decimal.NewFromInt(100)) {
			t.Fatalf("completed deposit left balance %s, want 100", w.RealBalance)
		}
	case models.StatusFailed:
		// The mock fails about one in six on purpose; a failed deposit must
		// credit nothing at all.
		if !w.Total().IsZero() {
			t.Fatalf("failed deposit left balance %s, want 0", w.Total())
		}
	default:
		t.Fatalf("deposit still %q after settlement", tx.Status)
	}
	h.assertReconciled(accountID)
}

func TestDepositRejectsBadInput(t *testing.T) {
	h := newHarness(t)
	userID, realAccount := h.newUser(decimal.Zero)

	// A demo account may not take real money.
	demo := h.newDemoAccount(userID)
	if _, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: demo,
		AmountKES: decimal.NewFromInt(1000), PhoneNumber: "0712345678",
	}); err != service.ErrRealOnly {
		t.Errorf("deposit to a demo account returned %v, want ErrRealOnly", err)
	}

	cases := []struct {
		name   string
		amount int64
		phone  string
		field  string
	}{
		{"below the minimum", 10, "0712345678", "amount"},
		{"above the maximum", 999999999, "0712345678", "amount"},
		{"not a Safaricom number", 1000, "0812345678", "phone_number"},
		{"not a phone number at all", 1000, "abc", "phone_number"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := h.wallet.Deposit(h.ctx, service.DepositInput{
				UserID: userID, AccountID: realAccount,
				AmountKES: decimal.NewFromInt(c.amount), PhoneNumber: c.phone,
			})
			var ve *service.ValidationError
			if !asValidation(err, &ve) {
				t.Fatalf("got %v, want a validation error", err)
			}
			if ve.Field != c.field {
				t.Errorf("error named field %q, want %q", ve.Field, c.field)
			}
		})
	}
	h.assertReconciled(realAccount)
}

func TestWithdrawalGatesAndLimits(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.Zero)

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(200),
		BalanceType: models.BalanceReal, Reason: "funding the withdrawal checks",
	}); err != nil {
		t.Fatal(err)
	}
	// Bonus money exists but must never be withdrawable.
	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(500),
		BalanceType: models.BalanceBonus, Reason: "bonus that must stay unwithdrawable",
	}); err != nil {
		t.Fatal(err)
	}

	// Unverified users cannot withdraw at all.
	if _, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(10), PhoneNumber: "0712345678",
	}); err != service.ErrKYCRequired {
		t.Fatalf("unverified withdrawal returned %v, want ErrKYCRequired", err)
	}

	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE users SET kyc_status = 'verified' WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}

	// Total balance is 700 but only the 200 real is withdrawable.
	if _, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(500), PhoneNumber: "0712345678",
	}); err != service.ErrInsufficientFunds {
		t.Fatalf("withdrawing bonus money returned %v, want ErrInsufficientFunds", err)
	}

	tx, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(200), PhoneNumber: "0712345678",
	})
	if err != nil {
		t.Fatalf("withdrawal: %v", err)
	}
	if tx.Status != models.StatusPending {
		t.Errorf("withdrawal status %q, want pending", tx.Status)
	}
	// The debit happens up front so the same funds cannot be spent twice.
	if w := h.wallet_(accountID); !w.RealBalance.IsZero() {
		t.Errorf("real balance %s after withdrawing it all, want 0", w.RealBalance)
	}
	// And a pending withdrawal counts toward the balance, which is exactly
	// what the reconciliation predicate has to get right.
	h.assertReconciled(accountID)
}

func TestAccountCreationAndReads(t *testing.T) {
	h := newHarness(t)
	userID, realAccount := h.newUser(decimal.NewFromInt(50))

	demo, err := h.wallet.CreateAccount(h.ctx, userID, models.AccountDemo)
	if err != nil {
		t.Fatalf("create demo: %v", err)
	}
	if demo.AccountType != models.AccountDemo {
		t.Fatalf("created a %q account", demo.AccountType)
	}

	// A demo account is funded with practice money as bonus, never as real.
	demoWallet := h.wallet_(demo.ID)
	if !demoWallet.RealBalance.IsZero() {
		t.Errorf("demo real balance %s, want 0", demoWallet.RealBalance)
	}
	if !demoWallet.BonusBalance.Equal(decimal.NewFromInt(10000)) {
		t.Errorf("demo bonus balance %s, want 10000", demoWallet.BonusBalance)
	}
	h.assertReconciled(demo.ID)

	if _, err := h.wallet.CreateAccount(h.ctx, userID, models.AccountDemo); err != service.ErrAccountExists {
		t.Errorf("second demo account returned %v, want ErrAccountExists", err)
	}
	if _, err := h.wallet.CreateAccount(h.ctx, userID, "savings"); err == nil {
		t.Error("an unknown account type was accepted")
	}

	accounts, err := h.wallet.ListAccounts(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("listed %d accounts, want 2", len(accounts))
	}

	if _, err := h.wallet.GetWallet(h.ctx, userID, realAccount); err != nil {
		t.Fatalf("read own wallet: %v", err)
	}

	// Another user must not be able to read either.
	otherUser, _ := h.newUser(decimal.Zero)
	if _, err := h.wallet.GetWallet(h.ctx, otherUser, realAccount); err != service.ErrForbidden {
		t.Errorf("cross-user wallet read returned %v, want ErrForbidden", err)
	}
	if _, err := h.wallet.OwnedAccount(h.ctx, otherUser, realAccount); err != service.ErrForbidden {
		t.Errorf("cross-user account read returned %v, want ErrForbidden", err)
	}
}

func TestLedgerPaginationAndFiltering(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	for i := 0; i < 5; i++ {
		if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
			AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(10),
			BalanceType: models.BalanceReal, Reason: "building a ledger to page through",
		}); err != nil {
			t.Fatal(err)
		}
	}
	trade, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.trading.Close(h.ctx, userID, trade.ID); err != nil {
		t.Fatal(err)
	}

	all, err := h.wallet.Transactions(h.ctx, userID, accountID, nil, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Items) != 3 {
		t.Fatalf("page holds %d rows, want 3", len(all.Items))
	}
	if all.Total < 8 {
		t.Fatalf("total %d, want at least 8", all.Total)
	}

	second, err := h.wallet.Transactions(h.ctx, userID, accountID, nil, 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	if second.Items[0].ID == all.Items[0].ID {
		t.Error("the second page repeated the first")
	}

	adjustments, err := h.wallet.Transactions(h.ctx, userID, accountID,
		[]string{models.TxAdminAdjustment}, 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if adjustments.Total != 5 {
		t.Fatalf("filtered to %d adjustments, want 5", adjustments.Total)
	}
	for _, tx := range adjustments.Items {
		if tx.Type != models.TxAdminAdjustment {
			t.Fatalf("filter leaked a %q row", tx.Type)
		}
	}

	// Another user must not be able to read this ledger.
	otherUser, _ := h.newUser(decimal.Zero)
	if _, err := h.wallet.Transactions(h.ctx, otherUser, accountID, nil, 25, 0); err != service.ErrForbidden {
		t.Errorf("cross-user ledger read returned %v, want ErrForbidden", err)
	}
	if _, err := h.wallet.Transaction(h.ctx, otherUser, all.Items[0].ID); err != service.ErrForbidden {
		t.Errorf("cross-user transaction read returned %v, want ErrForbidden", err)
	}

	h.assertReconciled(accountID)
}

func TestRateIsTheOneUsedForConversion(t *testing.T) {
	h := newHarness(t)
	if got := h.wallet.Rate(); got != 129.50 {
		t.Fatalf("rate %v, want 129.50", got)
	}

	// The rate the client is told must be the rate the server converts at, or
	// the deposit sheet's "≈ $x" line is a lie.
	userID, accountID := h.newUser(decimal.Zero)
	resp, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(1295), PhoneNumber: "0712345678",
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := decimal.NewFromInt(10); !resp.AmountUSD.Equal(want) {
		t.Fatalf("1295 KES became %s, want %s at the advertised rate", resp.AmountUSD, want)
	}
}

// The mock M-Pesa client fails about one deposit in six at random, so relying
// on it alone leaves the settlement branches covered by luck. These drive the
// Daraja callback directly so both outcomes are exercised every run.

func TestDepositSettlementCreditsOnSuccess(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	txID, checkoutID := h.pendingDeposit(userID, accountID, 12950)

	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, 0,
		"The service request is processed successfully.", "QK12345678")); err != nil {
		t.Fatalf("callback: %v", err)
	}

	tx, err := h.store.Transactions.GetByID(h.ctx, txID)
	if err != nil {
		t.Fatal(err)
	}
	if tx.Status != models.StatusCompleted {
		t.Fatalf("transaction is %q after a successful callback", tx.Status)
	}
	if receipt, _ := tx.Metadata["receipt"].(string); receipt != "QK12345678" {
		t.Errorf("receipt %q was not recorded", receipt)
	}

	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after a 12,950 KES deposit, want 100", w.RealBalance)
	}
	h.assertReconciled(accountID)

	// Daraja retries. A repeated callback must not credit twice.
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, 0, "duplicate", "QK12345678")); err != nil {
		t.Fatalf("duplicate callback errored: %v", err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("a retried callback credited again: balance is now %s", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

func TestDepositSettlementCreditsNothingOnFailure(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	// Each Daraja result code becomes copy a user can act on.
	cases := []struct {
		code int
		desc string
		want string
	}{
		{1032, "Request cancelled by user", "You cancelled the request"},
		{1, "The balance is insufficient", "Your M-Pesa balance was too low to complete this deposit"},
		{1037, "DS timeout user cannot be reached", "We couldn't reach your phone. Check it is on and try again"},
		{2001, "Wrong PIN", "The M-Pesa PIN you entered was wrong"},
	}

	for _, c := range cases {
		txID, checkoutID := h.pendingDeposit(userID, accountID, 1000)

		if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, c.code, c.desc, "")); err != nil {
			t.Fatalf("callback for %d: %v", c.code, err)
		}

		tx, err := h.store.Transactions.GetByID(h.ctx, txID)
		if err != nil {
			t.Fatal(err)
		}
		if tx.Status != models.StatusFailed {
			t.Fatalf("result code %d left the transaction %q", c.code, tx.Status)
		}
		if reason, _ := tx.Metadata["reason"].(string); reason != c.want {
			t.Errorf("result %d became %q, want %q", c.code, reason, c.want)
		}
	}

	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("failed deposits credited %s", w.Total())
	}
	h.assertReconciled(accountID)
}

func TestCallbackForAnUnknownCheckoutIsRejected(t *testing.T) {
	h := newHarness(t)

	// The callback is public, so an unsolicited one must find no request to
	// settle rather than inventing money.
	err := h.mpesa.HandleCallback(h.ctx, darajaCallback("ws_CO_NEVER_SENT", 0, "ok", "FAKE"))
	if err == nil {
		t.Fatal("a callback for a request we never made was accepted")
	}
}

// Daraja retries callbacks, and a retry can overlap the delivery it is retrying.
// Sequential duplicates are covered above; this is the case that used to slip
// through — two deliveries for one push, at once, each reading 'pending' before
// either had written.
//
// The guard is in the UPDATE rather than in a preceding read, so the database
// picks the winner. Exactly one credit must land no matter how many callers
// arrive together.
func TestConcurrentCallbacksCreditExactlyOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	const callers = 8

	for attempt := 0; attempt < 5; attempt++ {
		txID, checkoutID := h.pendingDeposit(userID, accountID, 12950)

		// Released together, so they contend rather than queue.
		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				if err := h.mpesa.HandleCallback(context.Background(),
					darajaCallback(checkoutID, 0, "processed", "QK12345678")); err != nil {
					errs <- err
				}
			}()
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("callback %d: %v", attempt, err)
		}

		// One credit per deposit, so the balance is exactly the number of
		// deposits so far times $100 — not eight times it.
		want := decimal.NewFromInt(int64(100 * (attempt + 1)))
		if w := h.wallet_(accountID); !w.RealBalance.Equal(want) {
			t.Fatalf("after %d concurrent callbacks on deposit %d the balance is %s, want %s",
				callers, attempt+1, w.RealBalance, want)
		}

		tx, err := h.store.Transactions.GetByID(h.ctx, txID)
		if err != nil {
			t.Fatal(err)
		}
		if tx.Status != models.StatusCompleted {
			t.Fatalf("transaction is %q after the callbacks", tx.Status)
		}

		// The ledger is the source of truth, so it has to agree too: a second
		// credit with no ledger row would be caught here and nowhere else.
		h.assertReconciled(accountID)
	}
}

// The same race on the failure path. A failed deposit credits nothing, so a
// double settle cannot create money — but it can fire the result notification
// twice and tell a user their deposit failed twice, and the count is the thing
// under test.
func TestConcurrentFailureCallbacksSettleOnce(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	settled := make(chan string, 256)
	h.wallet.OnDepositSettled(func(status string) { settled <- status })

	const callers = 8
	const deposits = 5
	for attempt := 0; attempt < deposits; attempt++ {
		_, checkoutID := h.pendingDeposit(userID, accountID, 1000)

		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_ = h.mpesa.HandleCallback(context.Background(),
					darajaCallback(checkoutID, 1032, "Request cancelled by user", ""))
			}()
		}
		close(start)
		wg.Wait()
	}

	// Give any straggler notification a moment to arrive before counting.
	time.Sleep(200 * time.Millisecond)
	close(settled)
	n := 0
	for range settled {
		n++
	}
	if n != deposits {
		t.Fatalf("%d settlement notifications for %d failed deposits, want one each", n, deposits)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("a failed deposit credited %s", w.Total())
	}
	h.assertReconciled(accountID)
}
