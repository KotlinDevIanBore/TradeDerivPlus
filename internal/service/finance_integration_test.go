//go:build integration

package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// The finance aggregates, against seeded outcomes with arithmetic worked out by
// hand. Every number in these tests is derivable from the payout table and the
// stakes, so a wrong aggregate shows up as a wrong number rather than as a test
// that agrees with whatever the code happens to do.
//
// Even/Odd pays 0.9520, so at stake 10 a win credits 19.52 and pnl is +9.52; a
// loss is -10; a void is 0 and refunds the stake.

// financeEven and financeOdd settle a contract deterministically: the injected
// price's last digit at pip size 4 decides the outcome.
const (
	financeEvenPrice = "100.1234" // last digit 4 → even
	financeOddPrice  = "100.1235" // last digit 5 → odd
)

// financeSeeder places and settles contracts on one account, driving the feed
// itself so every outcome is chosen rather than waited for.
type financeSeeder struct {
	h     *harness
	epoch int64
}

func newFinanceSeeder(t *testing.T, h *harness) *financeSeeder {
	t.Helper()
	h.digits.Start(h.ctx)
	return &financeSeeder{h: h, epoch: 1000}
}

func (s *financeSeeder) next() int64 { s.epoch++; return s.epoch }

// open places one Even contract at the given stake.
func (s *financeSeeder) open(t *testing.T, userID, accountID uuid.UUID, stake decimal.Decimal) *models.DigitContract {
	t.Helper()
	s.h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), s.next())
	c, err := s.h.digits.Open(s.h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        stake,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	return c
}

// settle drives a tick that resolves the contract the chosen way.
func (s *financeSeeder) settle(t *testing.T, c *models.DigitContract, want string) {
	t.Helper()
	price := financeEvenPrice // backing Even, so an even digit wins
	if want == models.DigitLost {
		price = financeOddPrice
	}
	s.h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString(price), s.next())
	s.h.digits.SweepForTest(s.h.ctx)

	got, err := s.h.store.DigitContracts.GetByID(s.h.ctx, c.ID)
	if err != nil {
		t.Fatalf("get after settle: %v", err)
	}
	if got.Status != want {
		t.Fatalf("contract %s is %s, want %s", c.ID, got.Status, want)
	}
}

// void backdates void_after and sweeps, the recipe TestDigitAutoVoid uses.
func (s *financeSeeder) void(t *testing.T, c *models.DigitContract) {
	t.Helper()
	if _, err := s.h.store.Pool().Exec(s.h.ctx,
		`UPDATE digit_contracts SET void_after = now() - interval '1 second' WHERE id = $1`, c.ID); err != nil {
		t.Fatalf("backdate void_after: %v", err)
	}
	s.h.digits.SweepForTest(s.h.ctx)
	got, err := s.h.store.DigitContracts.GetByID(s.h.ctx, c.ID)
	if err != nil {
		t.Fatalf("get after void: %v", err)
	}
	if got.Status != models.DigitVoid {
		t.Fatalf("contract %s is %s, want void", c.ID, got.Status)
	}
}

// play runs the plan's standard seed on one account: 3 wins, 2 losses, 1 void at
// stake 10. House P&L is -(3*9.52 - 2*10 + 0) = -8.56, and stakes exclude the
// void, so 50.
func (s *financeSeeder) play(t *testing.T, userID, accountID uuid.UUID) {
	t.Helper()
	ten := decimal.NewFromInt(10)
	for i := 0; i < 3; i++ {
		s.settle(t, s.open(t, userID, accountID, ten), models.DigitWon)
	}
	for i := 0; i < 2; i++ {
		s.settle(t, s.open(t, userID, accountID, ten), models.DigitLost)
	}
	s.void(t, s.open(t, userID, accountID, ten))
}

// financeWindow is a range wide enough to contain everything a test just seeded.
func financeWindow() (time.Time, time.Time) {
	now := time.Now().UTC()
	return now.Add(-time.Hour), now.Add(time.Hour)
}

// The cross-check: two independent paths to house P&L must agree.
//
// One reads digit_contracts.pnl through the finance aggregate. The other sums
// the ledger rows the settlement wrote — open is -stake, settle is +credit for a
// win, 0 for a loss, +stake for a void — which is the same figure arrived at
// from the other side of the books.
//
// Guards risk #2: a HousePnL derived as stake x payout_ratio instead of from the
// stored pnl disagrees with the ledger by models.WinCredit's rounding. The 3.33
// stake below is chosen so that rounding bites: 3.33 x 1.9520 = 6.50016, stored
// as 6.50, so a ratio-derived pnl would be 3.17016 against the ledger's 3.17.
func TestFinanceHousePnLMatchesLedger(t *testing.T) {
	h := newHarness(t)
	seed := newFinanceSeeder(t, h)

	realUser, realAccount := h.verifiedWithBalance(500)
	promoUser, promoAccount := h.newUser(decimal.NewFromInt(500))

	seed.play(t, realUser, realAccount)
	seed.play(t, promoUser, promoAccount)

	// The rounding case, on the real-funded account.
	odd := decimal.RequireFromString("3.33")
	seed.settle(t, seed.open(t, realUser, realAccount, odd), models.DigitWon)

	from, to := financeWindow()
	report, err := h.admin.Finance(h.ctx, from, to)
	if err != nil {
		t.Fatalf("finance: %v", err)
	}

	fromAggregate := report.Real.HousePnL.Add(report.Promotional.HousePnL)

	var fromLedger decimal.Decimal
	err = h.store.Pool().QueryRow(h.ctx, `
		SELECT -COALESCE(sum(t.amount), 0)
		FROM transactions t
		JOIN digit_contracts dc ON dc.id::text = t.reference
		JOIN accounts a ON a.id = dc.account_id
		WHERE t.type IN ('digit_open','digit_settle')
		  AND dc.status IN ('won','lost','void')
		  AND dc.settled_at >= $1 AND dc.settled_at < $2
		  AND a.account_type = 'real'`, from, to).Scan(&fromLedger)
	if err != nil {
		t.Fatalf("ledger cross-check query: %v", err)
	}

	if !fromAggregate.Equal(fromLedger) {
		t.Fatalf("house P&L disagrees: aggregate %s, ledger %s — the two sides of the books must match",
			fromAggregate, fromLedger)
	}

	// And it is the figure the hand arithmetic predicts: two accounts at -8.56
	// each, plus the 3.33 win whose pnl is 6.50 - 3.33 = 3.17, house-side -3.17.
	want := decimal.RequireFromString("-20.29")
	if !fromAggregate.Equal(want) {
		t.Fatalf("house P&L = %s, want %s (2 x -8.56 plus -3.17)", fromAggregate, want)
	}

	h.assertReconciled(realAccount)
	h.assertReconciled(promoAccount)
}

// The three-way funding split, with the exact figures the plan specifies.
//
// Guards risk #1: if the funding-balance join were dropped, promotional money
// would land in Real and these counts would double.
func TestFinanceSeededOutcomesAndFundingSplit(t *testing.T) {
	h := newHarness(t)
	seed := newFinanceSeeder(t, h)

	realUser, realAccount := h.verifiedWithBalance(500)
	promoUser, promoAccount := h.newUser(decimal.NewFromInt(500))
	demoAccount := h.newDemoAccount(promoUser)

	// One settled before the window, first: it must not appear in the range.
	stale := seed.open(t, realUser, realAccount, decimal.NewFromInt(10))
	seed.settle(t, stale, models.DigitWon)
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE digit_contracts SET settled_at = now() - interval '10 days' WHERE id = $1`, stale.ID); err != nil {
		t.Fatalf("backdate settled_at: %v", err)
	}

	seed.play(t, realUser, realAccount)
	seed.play(t, promoUser, promoAccount)
	seed.play(t, promoUser, demoAccount)

	// An open contract, LAST and deliberately: every open() injects a tick, and
	// that tick settles whatever is already open (100.10 is 100.1000 at pip size
	// 4, so digit 0, which wins an Even bet). Opening this any earlier would have
	// it settled by the next open's tick rather than left open, which is the
	// mistake the first draft of this test made.
	seed.open(t, realUser, realAccount, decimal.NewFromInt(10))

	from, to := financeWindow()
	report, err := h.admin.Finance(h.ctx, from, to)
	if err != nil {
		t.Fatalf("finance: %v", err)
	}

	wantStakes := decimal.NewFromInt(50)
	wantPnL := decimal.RequireFromString("-8.56")

	for name, got := range map[string]struct {
		contracts, wins, losses, voids int
		stakes, housePnL               decimal.Decimal
	}{
		"real":        {report.Real.Contracts, report.Real.Wins, report.Real.Losses, report.Real.Voids, report.Real.Stakes, report.Real.HousePnL},
		"promotional": {report.Promotional.Contracts, report.Promotional.Wins, report.Promotional.Losses, report.Promotional.Voids, report.Promotional.Stakes, report.Promotional.HousePnL},
	} {
		if got.contracts != 6 || got.wins != 3 || got.losses != 2 || got.voids != 1 {
			t.Fatalf("%s: contracts=%d wins=%d losses=%d voids=%d, want 6/3/2/1",
				name, got.contracts, got.wins, got.losses, got.voids)
		}
		if !got.stakes.Equal(wantStakes) {
			t.Fatalf("%s: stakes = %s, want 50 (the void's stake is not volume)", name, got.stakes)
		}
		if !got.housePnL.Equal(wantPnL) {
			t.Fatalf("%s: house P&L = %s, want -8.56", name, got.housePnL)
		}
	}

	if report.DemoContracts != 6 {
		t.Fatalf("demo contracts = %d, want 6", report.DemoContracts)
	}

	// The edge, all funding: 15 non-void contracts across the three accounts.
	if report.Edge.SampleSize != 15 {
		t.Fatalf("edge sample = %d, want 15 (5 non-void x 3 accounts)", report.Edge.SampleSize)
	}
	if report.EdgeReal.SampleSize != 5 {
		t.Fatalf("real edge sample = %d, want 5", report.EdgeReal.SampleSize)
	}
	if want := decimal.RequireFromString("0.024"); !report.Edge.Theoretical.Equal(want) {
		t.Fatalf("theoretical edge = %s, want 0.024 for Even/Odd", report.Edge.Theoretical)
	}
	// Realised on 5 contracts: -8.56 house / 50 staked = -0.1712.
	if want := decimal.RequireFromString("-0.1712"); !report.EdgeReal.Realised.Equal(want) {
		t.Fatalf("real realised edge = %s, want -0.1712", report.EdgeReal.Realised)
	}
	if !report.Edge.TooFewToRead {
		t.Fatalf("15 contracts must be flagged too few to read")
	}
	if report.Edge.Unpriced != 0 {
		t.Fatalf("unpriced = %d, want 0 — every seeded selection is priceable", report.Edge.Unpriced)
	}

	if report.Surface != service.FinanceSurface {
		t.Fatalf("surface = %q, want %q", report.Surface, service.FinanceSurface)
	}

	h.assertReconciled(realAccount)
	h.assertReconciled(promoAccount)
	h.assertReconciled(demoAccount)
}

// The daily series must have a row per day, including days nothing happened.
func TestFinanceByDayHasNoGaps(t *testing.T) {
	h := newHarness(t)
	seed := newFinanceSeeder(t, h)

	userID, accountID := h.verifiedWithBalance(500)
	seed.play(t, userID, accountID)

	to := time.Now().UTC().Add(time.Hour)
	from := to.AddDate(0, 0, -7)

	report, err := h.admin.Finance(h.ctx, from, to)
	if err != nil {
		t.Fatalf("finance: %v", err)
	}

	// generate_series from the start day to the end day inclusive over a span of
	// 7 days and a bit gives 8 buckets; what matters is that none is missing and
	// the empty ones are zeros rather than absent.
	if len(report.ByDay) < 7 {
		t.Fatalf("by-day has %d rows over a 7-day range, want at least 7 — empty days must be zeros, not gaps",
			len(report.ByDay))
	}
	if report.ByDayCapped {
		t.Fatalf("a 7-day range must not be capped")
	}

	empty := 0
	total := decimal.Zero
	for _, d := range report.ByDay {
		total = total.Add(d.RealHousePnL)
		if d.Contracts == 0 {
			empty++
		}
	}
	if empty == 0 {
		t.Fatalf("expected at least one empty day in a 7-day window seeded only today")
	}
	if !total.Equal(report.Real.HousePnL) {
		t.Fatalf("by-day real house P&L sums to %s, but the total says %s", total, report.Real.HousePnL)
	}
}

// Guards risk #3: deposits and withdrawals must use balanceAffectingRows, the
// same predicate the drift gauge uses. A pending WITHDRAWAL is money already
// gone and counts; a pending DEPOSIT has not arrived and does not.
func TestFinanceFlowsUseBalanceAffectingRows(t *testing.T) {
	h := newHarness(t)
	if err := h.store.Settings.Set(h.ctx, "withdrawals_enabled", "true"); err != nil {
		t.Fatal(err)
	}
	h.settings.BustCache()

	userID, accountID := h.verifiedWithBalance(500)

	// A withdrawal, left pending: the balance is already debited.
	if _, err := h.withdraw(userID, accountID, 100); err != nil {
		t.Fatalf("withdraw: %v", err)
	}

	// A pending deposit, which must not count.
	ref := "pending-deposit"
	if err := h.store.Transactions.Insert(h.ctx, &models.Transaction{
		AccountID: accountID, Type: models.TxDeposit, BalanceType: models.BalanceReal,
		Amount: decimal.NewFromInt(999), Status: models.StatusPending, Reference: &ref,
	}); err != nil {
		t.Fatalf("seed pending deposit: %v", err)
	}

	from, to := financeWindow()
	report, err := h.admin.Finance(h.ctx, from, to)
	if err != nil {
		t.Fatalf("finance: %v", err)
	}

	// verifiedWithBalance wrote one completed 500 deposit; the pending 999 is
	// excluded.
	if report.Flows.Deposits != 1 || !report.Flows.DepositsUSD.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("deposits = %d / %s, want 1 / 500 — a pending deposit must not count",
			report.Flows.Deposits, report.Flows.DepositsUSD)
	}
	if report.Flows.Withdrawals != 1 || !report.Flows.WithdrawalsUSD.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("withdrawals = %d / %s, want 1 / 100 — a pending withdrawal must count",
			report.Flows.Withdrawals, report.Flows.WithdrawalsUSD)
	}
	if want := decimal.NewFromInt(400); !report.Flows.NetFloatUSD.Equal(want) {
		t.Fatalf("net float = %s, want 400", report.Flows.NetFloatUSD)
	}

	var liability decimal.Decimal
	if err := h.store.Pool().QueryRow(h.ctx, `
		SELECT COALESCE(sum(w.real_balance), 0) FROM wallets w
		JOIN accounts a ON a.id = w.account_id WHERE a.account_type = 'real'`).Scan(&liability); err != nil {
		t.Fatalf("liability query: %v", err)
	}
	if !report.Flows.CustomerLiabilityUSD.Equal(liability) {
		t.Fatalf("liability = %s, want %s", report.Flows.CustomerLiabilityUSD, liability)
	}
}

// Guards risk #4: the bot split comes from digit_contracts.bot_run_id, never
// from bot_runs.realised_pnl — which is a maintained total of the same rows, so
// summing both would double-count.
func TestFinanceBotSplitDoesNotDoubleCount(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)

	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{StopLoss: decimal.NewFromInt(500)})
	d.resolve(run.ID, models.DigitWon, models.DigitLost)

	// Stop the run and let it finish before the manual contract, or the loop
	// keeps placing and the "manual" figure below silently includes a bot
	// contract. How many it placed before stopping is a timing detail, so the
	// assertions read it back rather than hardcoding it.
	if _, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	for i := 0; i < 50; i++ {
		r, err := h.store.BotRuns.Get(h.ctx, run.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.Status == models.BotStopped {
			break
		}
		d.tick(botEvenPrice) // settle whatever is in flight
		time.Sleep(20 * time.Millisecond)
	}
	stopped := awaitRunStopped(t, h, run.ID)

	// One manual contract on the same account, so the split has something to
	// separate.
	seed := &financeSeeder{h: h, epoch: 9000}
	seed.settle(t, seed.open(t, userID, accountID, decimal.NewFromInt(10)), models.DigitWon)

	from, to := financeWindow()
	report, err := h.admin.Finance(h.ctx, from, to)
	if err != nil {
		t.Fatalf("finance: %v", err)
	}

	// newUser funds bonus, so this account is the promotional bucket.
	b := report.Promotional
	if b.BotContracts != stopped.ContractsPlaced {
		t.Fatalf("bot contracts = %d, but the run placed %d — the bot_run_id split lost or gained rows",
			b.BotContracts, stopped.ContractsPlaced)
	}
	if b.Contracts != b.BotContracts+1 {
		t.Fatalf("contracts = %d, want %d (%d bot + 1 manual)",
			b.Contracts, b.BotContracts+1, b.BotContracts)
	}

	stored := stopped
	if !b.BotHousePnL.Equal(stored.RealisedPnL.Neg()) {
		t.Fatalf("bot house P&L = %s, but bot_runs.realised_pnl says %s (house-side %s) — the two views of the same contracts disagree",
			b.BotHousePnL, stored.RealisedPnL, stored.RealisedPnL.Neg())
	}

	manual := b.HousePnL.Sub(b.BotHousePnL)
	if want := decimal.RequireFromString("-9.52"); !manual.Equal(want) {
		t.Fatalf("manual house P&L = %s, want -9.52 (one win at stake 10) — total minus bot must be exactly the manual contract",
			manual)
	}

	h.assertReconciled(accountID)
}

func TestFinanceRangeValidation(t *testing.T) {
	h := newHarness(t)
	now := time.Now().UTC()

	var ve *service.ValidationError
	if _, err := h.admin.Finance(h.ctx, now, now.Add(-time.Hour)); !errors.As(err, &ve) {
		t.Fatalf("a reversed range returned %v, want a validation error", err)
	}
	if _, err := h.admin.Finance(h.ctx, now.AddDate(0, 0, -401), now); !errors.As(err, &ve) {
		t.Fatalf("a 401-day span returned %v, want a validation error", err)
	}
	// An unbounded range is allowed and resolves against the earliest settlement.
	if _, err := h.admin.Finance(h.ctx, time.Time{}, now); err != nil {
		t.Fatalf("unbounded range: %v", err)
	}
}
