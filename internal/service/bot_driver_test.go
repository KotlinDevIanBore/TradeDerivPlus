//go:build integration

package service_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// botDriver makes a run's outcomes a fact about the test rather than about a
// random walk.
//
// A digit contract settles on the first tick whose epoch is strictly greater
// than the one it entered on, so a test that owns every injected tick owns every
// outcome. The driver keeps the epoch monotonic and names the three things that
// can happen to a contract: win, lose, void.
type botDriver struct {
	t *testing.T
	h *harness

	mu    sync.Mutex
	epoch int64
}

// newBotDriver starts the settler and puts a first price on the feed, which is
// what a run needs before it can open anything.
func newBotDriver(t *testing.T, h *harness) *botDriver {
	t.Helper()
	h.digits.Start(h.ctx)
	d := &botDriver{t: t, h: h, epoch: 1000}
	h.feed.InjectTickAt(botSymbol, decimal.RequireFromString("100.10"), d.epoch)
	return d
}

func (d *botDriver) nextEpoch() int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.epoch++
	return d.epoch
}

// tick injects a price with a fresh epoch, which settles every open contract on
// the symbol through the registered observer.
func (d *botDriver) tick(price string) {
	d.h.feed.InjectTickAt(botSymbol, decimal.RequireFromString(price), d.nextEpoch())
}

// tickQuiet updates the latest price without running observers, simulating a tick
// the settler missed. The sweep or a check-on-read will find it.
func (d *botDriver) tickQuiet(price string) {
	d.h.feed.InjectTickQuiet(botSymbol, decimal.RequireFromString(price), d.nextEpoch())
}

// awaitOpen waits for the run's next in-flight contract and returns it, so a test
// can decide that contract's fate before the loop places another.
func (d *botDriver) awaitOpen(runID uuid.UUID) *models.DigitContract {
	d.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		items, _, err := d.h.store.DigitContracts.ListByAccount(d.h.ctx, d.accountOf(runID), models.DigitOpen, &runID, 10, 0)
		if err != nil {
			d.t.Fatalf("list open contracts: %v", err)
		}
		if len(items) > 0 {
			return &items[0]
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("run %s opened no contract within 20s", runID)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (d *botDriver) accountOf(runID uuid.UUID) uuid.UUID {
	d.t.Helper()
	run, err := d.h.store.BotRuns.Get(d.h.ctx, runID)
	if err != nil {
		d.t.Fatalf("get run: %v", err)
	}
	return run.AccountID
}

// win, lose and void resolve one contract and wait for the row to reflect it.
//
// Runs in these tests select "even", so an even last digit wins and an odd one
// loses. A void needs no settling tick at all: the contract's void_after is
// backdated and the next check-on-read refunds the stake.
func (d *botDriver) win(c *models.DigitContract) {
	d.t.Helper()
	d.tick(botEvenPrice)
	d.awaitSettled(c.ID, models.DigitWon)
}

func (d *botDriver) lose(c *models.DigitContract) {
	d.t.Helper()
	d.tick(botOddPrice)
	d.awaitSettled(c.ID, models.DigitLost)
}

func (d *botDriver) void(c *models.DigitContract) {
	d.t.Helper()
	// No newer tick exists, so check-on-read cannot settle it; past void_after it
	// must refund instead. That ordering is the thing being relied on.
	if _, err := d.h.store.Pool().Exec(d.h.ctx,
		`UPDATE digit_contracts SET void_after = now() - interval '1 second' WHERE id = $1`, c.ID); err != nil {
		d.t.Fatalf("backdate void_after: %v", err)
	}
	d.awaitSettled(c.ID, models.DigitVoid)
}

func (d *botDriver) awaitSettled(contractID uuid.UUID, want string) {
	d.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		c, err := d.h.store.DigitContracts.GetByID(d.h.ctx, contractID)
		if err != nil {
			d.t.Fatalf("get contract: %v", err)
		}
		if c.Status != models.DigitOpen {
			if c.Status != want {
				d.t.Fatalf("contract %s settled %s, want %s", contractID, c.Status, want)
			}
			return
		}
		if time.Now().After(deadline) {
			d.t.Fatalf("contract %s still open after 20s", contractID)
		}
		// The bot's own poll drives settlement for a void; a sweep covers the case
		// where the observer was bypassed.
		d.h.digits.SweepForTest(d.h.ctx)
		time.Sleep(5 * time.Millisecond)
	}
}

// resolve plays a named sequence of outcomes, one contract at a time, which is
// how the streak tests stay readable.
func (d *botDriver) resolve(runID uuid.UUID, outcomes ...string) {
	d.t.Helper()
	for i, outcome := range outcomes {
		c := d.awaitOpen(runID)
		switch outcome {
		case models.DigitWon:
			d.win(c)
		case models.DigitLost:
			d.lose(c)
		case models.DigitVoid:
			d.void(c)
		default:
			d.t.Fatalf("outcome %d: unknown %q", i, outcome)
		}
	}
}

// startEvenOddRun starts a run on even/odd "even", which is the shape every
// sequence test uses.
func (d *botDriver) startEvenOddRun(userID, accountID uuid.UUID, stake int64, limits models.BotLimitsConfig) *models.BotRun {
	d.t.Helper()
	run, err := d.h.bots.StartRun(d.h.ctx, service.StartRunInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(stake),
		IntervalMS:   1,
		Limits:       limits,
	})
	if err != nil {
		d.t.Fatalf("start run: %v", err)
	}
	return run
}

// assertStopped waits for the run to end and checks why, which is the assertion
// almost every stop-condition test makes.
func assertStopped(t *testing.T, h *harness, runID uuid.UUID, wantReason string) *models.BotRun {
	t.Helper()
	run := awaitRunStopped(t, h, runID)
	if run.StopReason == nil {
		t.Fatalf("run stopped with no reason, want %s", wantReason)
	}
	if *run.StopReason != wantReason {
		t.Fatalf("stop reason %q, want %q (placed %d, W/L/V %d/%d/%d, realised %s)",
			*run.StopReason, wantReason, run.ContractsPlaced, run.Wins, run.Losses, run.Voids, run.RealisedPnL)
	}
	return run
}

// assertNoFurtherPlacements proves a stopped run is actually inert: it feeds more
// ticks and checks the count does not move.
func assertNoFurtherPlacements(t *testing.T, h *harness, d *botDriver, runID uuid.UUID) {
	t.Helper()
	before, err := h.store.BotRuns.Get(h.ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	for i := 0; i < 5; i++ {
		d.tick(botEvenPrice)
		time.Sleep(10 * time.Millisecond)
	}
	after, err := h.store.BotRuns.Get(h.ctx, runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if after.ContractsPlaced != before.ContractsPlaced {
		t.Fatalf("a stopped run placed %d more contracts",
			after.ContractsPlaced-before.ContractsPlaced)
	}
}

// derefReason makes a failure message say the reason rather than its address.
func derefReason(r *string) string {
	if r == nil {
		return "<none>"
	}
	return *r
}
