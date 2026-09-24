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

// The race Phase 3 found, in both directions.
//
// A stop decides between 'stopping' and 'stopped' by counting the run's open
// contracts. A count cannot see a placement whose transaction has not committed,
// so a stop arriving mid-placement used to read zero, write the ending, and let
// the contract commit underneath it — a contract in flight belonging to a run
// that was already stopped, and an account whose one-live-run slot had been
// released while it still had one.
//
// Both paths now take the run row's lock: DigitService.Open before it inserts,
// and applyStop before it counts. These tests pin the two orderings that lock
// makes the only two. Neither uses a goroutine's timing to get there — the locks
// themselves are the synchronisation, taken by a transaction the test controls.
//
// No bot loop runs in either: a run row is created directly and Open is called
// by hand, so what is under test is the two operations and nothing else.

// Ordering one: the stop gets there first. The placement that was already on its
// way must find the ending and refuse, rather than committing under it.
func TestPlacementRefusedOnceItsRunIsStopped(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	run := createRunRow(t, h, accountID)

	// A transaction the test owns, holding the wallet row. Open takes this lock
	// first, so a placement starting now blocks here — inside Open, before it has
	// inserted anything, which is exactly the window the bug lived in.
	blocker, err := h.store.Pool().Begin(h.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := blocker.Exec(h.ctx,
		`SELECT account_id FROM wallets WHERE account_id = $1 FOR UPDATE`, accountID); err != nil {
		t.Fatalf("lock wallet: %v", err)
	}

	placed := make(chan error, 1)
	go func() {
		_, err := h.digits.Open(context.Background(), openInputFor(run, userID, accountID))
		placed <- err
	}()

	// It is stuck on the wallet, not finished: proving the placement really is in
	// flight, rather than having quietly failed before the interesting part.
	select {
	case err := <-placed:
		t.Fatalf("placement finished while the wallet was locked: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Now the stop, which sees no open contract because there is none yet.
	stopped, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser)
	if err != nil {
		t.Fatalf("stop run: %v", err)
	}
	if stopped.Status != models.BotStopped {
		t.Fatalf("status %q with nothing in flight, want stopped", stopped.Status)
	}

	// Release the wallet and let the placement run into the ending.
	if err := blocker.Rollback(h.ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	select {
	case err := <-placed:
		if !errors.Is(err, service.ErrBotRunNotLive) {
			t.Fatalf("placement returned %v, want ErrBotRunNotLive — a stopped run must not place", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("placement never returned after the wallet was released")
	}

	// The assertion the bug was: nothing of this run's is open, so the account's
	// live-run slot was not released while a contract was still in flight.
	open, err := h.store.DigitContracts.CountOpenByRun(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("count open: %v", err)
	}
	if open != 0 {
		t.Fatalf("%d contracts open under a stopped run", open)
	}
	final, err := h.store.BotRuns.Get(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.ContractsPlaced != 0 {
		t.Fatalf("a stopped run counted %d placements", final.ContractsPlaced)
	}
	h.assertReconciled(accountID)
	_ = d
}

// Ordering two: the placement gets there first. The stop must wait for it rather
// than deciding around it, and then count it.
func TestStopWaitsForAPlacementAlreadyHoldingTheRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	run := createRunRow(t, h, accountID)

	// Stand in for a placement that has taken the run's lock and not yet
	// committed, by taking the same lock the same way.
	holder, err := h.store.Pool().Begin(h.ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := holder.Exec(h.ctx,
		`SELECT id FROM bot_runs WHERE id = $1 FOR UPDATE`, run.ID); err != nil {
		t.Fatalf("lock run: %v", err)
	}

	stopped := make(chan error, 1)
	go func() {
		_, err := h.bots.StopRun(context.Background(), userID, run.ID, models.BotStopUser)
		stopped <- err
	}()

	// The stop is blocked, which is the property: it is not deciding anything
	// while a placement could still commit.
	select {
	case err := <-stopped:
		t.Fatalf("stop decided while the run row was held: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	// Commit a contract from inside the holding transaction, the way a placement
	// would, then release.
	if _, err := holder.Exec(h.ctx, `
		INSERT INTO digit_contracts (
			account_id, symbol_id, contract_type, selection, stake, payout_ratio,
			potential_payout, entry_price, entry_digit, entry_tick_epoch, status, void_after, bot_run_id
		) VALUES ($1,$2,'even_odd','{"parity":"even"}',10,0.952,19.52,100.10,0,1,'open',now() + interval '5 seconds',$3)`,
		accountID, run.SymbolID, run.ID); err != nil {
		t.Fatalf("insert contract: %v", err)
	}
	if err := holder.Commit(h.ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("stop never returned after the run row was released")
	}

	// It counted the contract it waited for, so the run holds the account's slot
	// until that contract settles rather than releasing it early.
	after, err := h.store.BotRuns.Get(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if after.Status != models.BotStopping {
		t.Fatalf("status %q with a contract in flight, want stopping", after.Status)
	}
	_ = d
}

// createRunRow makes a run without starting a goroutine for it, so these tests
// are about two operations rather than about a loop's timing.
func createRunRow(t *testing.T, h *harness, accountID uuid.UUID) *models.BotRun {
	t.Helper()
	symbol, err := h.store.Symbols.GetByCode(h.ctx, botSymbol)
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	run := &models.BotRun{
		AccountID:       accountID,
		SymbolID:        symbol.ID,
		ContractType:    models.DigitEvenOdd,
		Selection:       models.DigitSelection{Parity: models.DigitParityEven},
		Stake:           decimal.NewFromInt(10),
		IntervalMS:      2000,
		Limits:          models.BotLimitsConfig{StopLoss: decimal.NewFromInt(500)},
		StartingBalance: decimal.NewFromInt(1000),
	}
	if err := h.store.BotRuns.Create(h.ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run
}

func openInputFor(run *models.BotRun, userID, accountID uuid.UUID) service.OpenDigitInput {
	return service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
		BotRunID:     &run.ID,
	}
}
