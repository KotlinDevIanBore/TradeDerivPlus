//go:build integration

package service_test

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

// The stop conditions, each forced deterministically rather than waited for.
//
// Every test here drives the outcomes itself through botDriver, so a run's
// history is exactly the sequence written in the test and a failure means the
// policy is wrong rather than that a coin landed badly.

func TestBotStopLossHaltsRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// Stake 10 against a stop loss of 25: three losses put realised P/L at -30,
	// which is past the floor. Two would not be, so this also pins down that the
	// check is on the total rather than on the count.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(25),
	})
	d.resolve(run.ID, models.DigitLost, models.DigitLost, models.DigitLost)

	final := assertStopped(t, h, run.ID, models.BotStopStopLoss)
	if final.ContractsPlaced != 3 || final.Losses != 3 {
		t.Fatalf("placed %d losses %d, want 3 and 3", final.ContractsPlaced, final.Losses)
	}
	if !final.RealisedPnL.Equal(decimal.NewFromInt(-30)) {
		t.Fatalf("realised %s, want -30", final.RealisedPnL)
	}
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

func TestBotTakeProfitHaltsRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// Even/odd pays 0.952, so a winning $10 contract returns $9.52 of profit. Two
	// wins clear a $15 take profit; one does not.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss:   decimal.NewFromInt(500),
		TakeProfit: decimalPtr("15"),
	})
	d.resolve(run.ID, models.DigitWon, models.DigitWon)

	final := assertStopped(t, h, run.ID, models.BotStopTakeProfit)
	if final.Wins != 2 {
		t.Fatalf("wins %d, want 2", final.Wins)
	}
	if !final.RealisedPnL.Equal(decimal.RequireFromString("19.04")) {
		t.Fatalf("realised %s, want 19.04", final.RealisedPnL)
	}
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

// A void is a refund, not a loss. If it counted as one, a stalled feed would
// close out a run that had lost nothing at all.
func TestBotVoidIsNotALoss(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss:             decimal.NewFromInt(500),
		MaxConsecutiveLosses: intp(1),
	})

	// One void, with max_consecutive_losses set to 1: if a void were treated as a
	// loss the run would stop here, which is exactly the bug this guards.
	voided := d.awaitOpen(run.ID)
	d.void(voided)

	deadline := time.Now().Add(5 * time.Second)
	for {
		r, err := h.store.BotRuns.Get(h.ctx, run.ID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if r.Voids == 1 {
			if r.Losses != 0 {
				t.Fatalf("losses %d after a void, want 0", r.Losses)
			}
			if r.ConsecutiveLosses != 0 {
				t.Fatalf("consecutive_losses %d after a void, want 0", r.ConsecutiveLosses)
			}
			if !r.RealisedPnL.IsZero() {
				t.Fatalf("realised %s after a void, want 0", r.RealisedPnL)
			}
			if r.Status == models.BotStopped && r.StopReason != nil &&
				*r.StopReason == models.BotStopMaxConsecutiveLosses {
				t.Fatal("a void ended the run as if it were a loss")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no void recorded: voids %d losses %d", r.Voids, r.Losses)
		}
		time.Sleep(10 * time.Millisecond)
	}

	// The stake came back. Checked against the contract's own ledger rows rather
	// than a wallet snapshot, because by now the run has placed its next contract
	// and debited for it — comparing balances would race the loop and measure the
	// wrong thing.
	ref := voided.ID.String()
	rows, _, err := h.store.Transactions.ListByAccount(h.ctx, accountID,
		[]string{models.TxDigitOpen, models.TxDigitSettle}, 100, 0)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	net := decimal.Zero
	found := 0
	for _, tx := range rows {
		if tx.Reference == nil || *tx.Reference != ref {
			continue
		}
		found++
		net = net.Add(tx.Amount)
	}
	if found < 2 {
		t.Fatalf("%d ledger rows for the voided contract, want the debit and its refund", found)
	}
	if !net.IsZero() {
		t.Fatalf("the voided contract moved %s net, want 0", net)
	}

	// And the run is still going: a loss after the void is what stops it.
	d.resolve(run.ID, models.DigitLost)
	assertStopped(t, h, run.ID, models.BotStopMaxConsecutiveLosses)
	h.assertReconciled(accountID)
}

// Open cannot report a stalled feed — a stale last price still prices a contract
// — so repeated voids are what notice one.
func TestBotConsecutiveVoidsStopFeedDown(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	d.resolve(run.ID, models.DigitVoid, models.DigitVoid, models.DigitVoid)

	final := assertStopped(t, h, run.ID, models.BotStopFeedDown)
	if final.Voids != 3 {
		t.Fatalf("voids %d, want 3", final.Voids)
	}
	if !final.RealisedPnL.IsZero() {
		t.Fatalf("realised %s, want 0: voids cost nothing", final.RealisedPnL)
	}
	h.assertReconciled(accountID)
}

// A win resets the losing streak; a void leaves it exactly where it was. Both
// halves matter, so the sequence exercises them in one run.
func TestBotMaxConsecutiveLossesResetsOnWinNotVoid(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss:             decimal.NewFromInt(500),
		MaxConsecutiveLosses: intp(3),
	})

	// lose lose        → streak 2
	// win              → streak 0   (a naive counter would stop two losses later)
	// lose             → streak 1
	// void             → streak 1   (unchanged; a void is not a loss)
	// lose lose        → streak 3   → stop
	d.resolve(run.ID,
		models.DigitLost, models.DigitLost,
		models.DigitWon,
		models.DigitLost,
		models.DigitVoid,
		models.DigitLost, models.DigitLost,
	)

	final := assertStopped(t, h, run.ID, models.BotStopMaxConsecutiveLosses)
	if final.ContractsPlaced != 7 {
		t.Fatalf("placed %d, want 7 — the win must reset the streak and the void must not advance it",
			final.ContractsPlaced)
	}
	if final.Wins != 1 || final.Losses != 5 || final.Voids != 1 {
		t.Fatalf("W/L/V %d/%d/%d, want 1/5/1", final.Wins, final.Losses, final.Voids)
	}
	if final.ConsecutiveLosses != 3 {
		t.Fatalf("consecutive_losses %d, want 3", final.ConsecutiveLosses)
	}
	h.assertReconciled(accountID)
}

// max_contracts counts attempts, voids included. If it counted only settled
// wins and losses, a stalled feed would place without bound.
func TestBotMaxContractsCountsVoids(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// Two, deliberately below the three-void feed-down threshold, so the reason
	// this test asserts can only be max_contracts.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss:     decimal.NewFromInt(500),
		MaxContracts: intp(2),
	})
	d.resolve(run.ID, models.DigitVoid, models.DigitVoid)

	final := assertStopped(t, h, run.ID, models.BotStopMaxContracts)
	if final.ContractsPlaced != 2 || final.Voids != 2 {
		t.Fatalf("placed %d voids %d, want 2 and 2", final.ContractsPlaced, final.Voids)
	}
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

func TestBotMaxDuration(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// The validated minimum is a minute, which no test should wait for. The
	// deadline is derived from the row on every iteration, so backdating
	// started_at is the same thing as time passing — and that it works is itself
	// worth asserting, since an earlier draft captured the deadline once at launch.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss:           decimal.NewFromInt(500),
		MaxDurationSeconds: intp(60),
	})
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE bot_runs SET started_at = now() - interval '2 minutes' WHERE id = $1`, run.ID); err != nil {
		t.Fatalf("backdate started_at: %v", err)
	}

	// Let whatever is in flight finish, so the loop reaches its next check.
	if items, _, err := h.store.DigitContracts.ListByAccount(h.ctx, accountID, models.DigitOpen, &run.ID, 10, 0); err == nil && len(items) > 0 {
		d.win(&items[0])
	}

	assertStopped(t, h, run.ID, models.BotStopMaxDuration)
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

func decimalPtr(s string) *decimal.Decimal {
	d := decimal.RequireFromString(s)
	return &d
}
