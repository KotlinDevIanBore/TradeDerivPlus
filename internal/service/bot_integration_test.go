//go:build integration

package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// Prices chosen so the settlement digit is known: 100.1234 at pip size 4 ends in
// 4, which is even. An even/odd run on "even" therefore wins on that tick and
// loses on an odd one, which is what makes these tests deterministic rather than
// dependent on a random walk.
const (
	botEvenPrice = "100.1234" // last digit 4 → even
	botOddPrice  = "100.1235" // last digit 5 → odd
)

// driveSettlements feeds ticks until the run's placed count stops moving, so a
// test can watch a loop make progress without sleeping for its interval.
//
// Each injected tick carries a strictly greater epoch, which is what settlement
// requires: a contract settles on the first tick after the one it entered on.
func driveSettlements(t *testing.T, h *harness, price string, epochFrom int64, until func() bool, limit int) {
	t.Helper()
	epoch := epochFrom
	deadline := time.Now().Add(20 * time.Second)
	for i := 0; i < limit; i++ {
		if until() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out driving settlements after %d ticks", i)
		}
		epoch++
		h.feed.InjectTickAt(botSymbol, decimal.RequireFromString(price), epoch)
		time.Sleep(15 * time.Millisecond)
	}
	if !until() {
		t.Fatalf("condition not met after %d ticks", limit)
	}
}

const botSymbol = "R_75"

// awaitRunStopped polls the row until the run is stopped, and returns it.
func awaitRunStopped(t *testing.T, h *harness, runID uuid.UUID) *models.BotRun {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		run, err := h.store.BotRuns.Get(h.ctx, runID)
		if err != nil {
			t.Fatalf("get run: %v", err)
		}
		if run.Status == models.BotStopped {
			return run
		}
		if time.Now().After(deadline) {
			t.Fatalf("run %s still %s after 20s", runID, run.Status)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func intp(n int) *int { return &n }

// startRun is the common setup: a funded demo account, a live price, and a run.
func startRun(t *testing.T, h *harness, limits models.BotLimitsConfig, stake int64) (uuid.UUID, uuid.UUID, *models.BotRun) {
	t.Helper()
	h.digits.Start(h.ctx)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(botSymbol, decimal.RequireFromString("100.10"), 1000)

	run, err := h.bots.StartRun(h.ctx, service.StartRunInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(stake),
		IntervalMS:   1,
		Limits:       limits,
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	return userID, accountID, run
}

// The headline test for Checkpoint 2: every contract a run places is attributable,
// never more than one is in flight, and the run's totals — written inside the
// settlement transaction rather than by the loop — agree with the contracts.
func TestBotRunPlacesSequentiallyWithProvenance(t *testing.T) {
	h := newHarness(t)
	_, accountID, run := startRun(t, h, models.BotLimitsConfig{
		StopLoss:     decimal.NewFromInt(500),
		MaxContracts: intp(4),
	}, 10)

	// Watch for more than one contract in flight while the run works. A
	// sequential loop must never have two, and the per-account cap would not catch
	// a bug here because it allows five.
	worst := 0
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			n, err := h.store.DigitContracts.CountOpenByRun(h.ctx, run.ID)
			if err == nil && n > worst {
				worst = n
			}
			time.Sleep(2 * time.Millisecond)
		}
	}()

	driveSettlements(t, h, botEvenPrice, 1000, func() bool {
		r, err := h.store.BotRuns.Get(h.ctx, run.ID)
		return err == nil && r.Status == models.BotStopped
	}, 200)
	close(stop)

	final := awaitRunStopped(t, h, run.ID)

	if worst > 1 {
		t.Fatalf("%d contracts in flight at once; a run places one at a time", worst)
	}
	if final.StopReason == nil || *final.StopReason != models.BotStopMaxContracts {
		t.Fatalf("stop reason %v, want max_contracts", final.StopReason)
	}
	if final.ContractsPlaced != 4 {
		t.Fatalf("placed %d, want 4", final.ContractsPlaced)
	}

	// Every contract carries the run, and the counters match the contracts. This
	// is the assertion that the counters-in-transaction change exists for: if the
	// loop maintained them instead, a settlement arriving after the run stopped
	// would leave these disagreeing.
	contracts, total, err := h.store.DigitContracts.ListByAccount(h.ctx, accountID, "", nil, 100, 0)
	if err != nil {
		t.Fatalf("list contracts: %v", err)
	}
	if total != final.ContractsPlaced {
		t.Fatalf("%d contracts in the table, run counted %d", total, final.ContractsPlaced)
	}

	var wins, losses, voids int
	realised := decimal.Zero
	for i := range contracts {
		c := contracts[i]
		if c.BotRunID == nil {
			t.Fatalf("contract %s has no bot_run_id", c.ID)
		}
		if *c.BotRunID != run.ID {
			t.Fatalf("contract %s points at run %s, want %s", c.ID, *c.BotRunID, run.ID)
		}
		switch c.Status {
		case models.DigitWon:
			wins++
		case models.DigitLost:
			losses++
		case models.DigitVoid:
			voids++
		default:
			t.Fatalf("contract %s left %s", c.ID, c.Status)
		}
		if c.PnL != nil {
			realised = realised.Add(*c.PnL)
		}
	}
	if wins != final.Wins || losses != final.Losses || voids != final.Voids {
		t.Fatalf("contracts say %dW/%dL/%dV, run says %dW/%dL/%dV",
			wins, losses, voids, final.Wins, final.Losses, final.Voids)
	}
	if !realised.Equal(final.RealisedPnL) {
		t.Fatalf("contracts sum to %s, run says %s", realised, final.RealisedPnL)
	}

	// The ledger row carries the provenance too, so a statement can be read
	// without joining back to the contracts.
	txs, _, err := h.store.Transactions.ListByAccount(h.ctx, accountID, []string{models.TxDigitOpen}, 100, 0)
	if err != nil {
		t.Fatalf("list transactions: %v", err)
	}
	if len(txs) != final.ContractsPlaced {
		t.Fatalf("%d digit_open rows, want %d", len(txs), final.ContractsPlaced)
	}
	for _, tx := range txs {
		if got, ok := tx.Metadata["bot_run_id"].(string); !ok || got != run.ID.String() {
			t.Fatalf("digit_open metadata bot_run_id = %v, want %s", tx.Metadata["bot_run_id"], run.ID)
		}
	}

	// And the money still adds up, which is the invariant everything else rests on.
	h.assertReconciled(accountID)

	// The user was told, on their own topic.
	events := h.events.ofType("bot_run")
	if len(events) == 0 {
		t.Fatal("no bot_run events were broadcast")
	}
}

// The bot_run_id filter narrows the contract list to one run's contracts, which
// is what a screen showing "this run's trades" needs. Two runs on the same
// account, so a filter that quietly ignored its argument would be caught.
func TestDigitContractsFilterByBotRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	first := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(2),
	})
	d.resolve(first.ID, models.DigitWon, models.DigitWon)
	assertStopped(t, h, first.ID, models.BotStopMaxContracts)

	second := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(1),
	})
	d.resolve(second.ID, models.DigitLost)
	assertStopped(t, h, second.ID, models.BotStopMaxContracts)

	// A human contract too, so the filter has something to exclude that has no
	// run at all.
	if _, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	}); err != nil {
		t.Fatalf("human open: %v", err)
	}

	all, total, err := h.digits.List(h.ctx, userID, accountID, "", nil, 100, 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if total != 4 {
		t.Fatalf("%d contracts on the account, want 4", total)
	}

	for _, tc := range []struct {
		name  string
		runID uuid.UUID
		want  int
	}{
		{"first run", first.ID, 2},
		{"second run", second.ID, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runID := tc.runID
			items, n, err := h.digits.List(h.ctx, userID, accountID, "", &runID, 100, 0)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if n != tc.want || len(items) != tc.want {
				t.Fatalf("total %d items %d, want %d", n, len(items), tc.want)
			}
			for _, c := range items {
				if c.BotRunID == nil || *c.BotRunID != runID {
					t.Fatalf("contract %s is not from run %s", c.ID, runID)
				}
			}
		})
	}

	// The unfiltered list still includes the human contract, which is the one row
	// no run-scoped query should ever return.
	humans := 0
	for _, c := range all {
		if c.BotRunID == nil {
			humans++
		}
	}
	if humans != 1 {
		t.Fatalf("%d contracts with no run, want 1", humans)
	}
	h.assertReconciled(accountID)
}
