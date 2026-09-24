//go:build integration

package service_test

import (
	"sync"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

// counters stands in for the observability package, which this service never
// imports: the hooks are the contract, so the hooks are what is checked.
type counters struct {
	mu       sync.Mutex
	placed   int
	outcomes map[string]int
	errors   map[string]int
	stopped  map[string]int
}

func newCounters() *counters {
	return &counters{
		outcomes: map[string]int{},
		errors:   map[string]int{},
		stopped:  map[string]int{},
	}
}

func (c *counters) countPlacement()        { c.mu.Lock(); c.placed++; c.mu.Unlock() }
func (c *counters) countOutcome(o string)  { c.mu.Lock(); c.outcomes[o]++; c.mu.Unlock() }
func (c *counters) countError(code string) { c.mu.Lock(); c.errors[code]++; c.mu.Unlock() }
func (c *counters) countStopped(r string)  { c.mu.Lock(); c.stopped[r]++; c.mu.Unlock() }

func (c *counters) total(m map[string]int) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}

// One contract, one increment — on each counter.
//
// The runaway alert is `increase(bot_contracts_total[1m]) > bot_runs_live * 60`,
// and it only means "more than one contract per second per run" if a contract
// increments that metric exactly once. It used to increment twice, once on
// placement and once on its outcome, which halved the alert's real threshold.
// The load run measured it: 10,166 placed against 4,905 won plus 5,278 lost.
func TestBotContractCountersCountEachContractOnce(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	c := newCounters()
	h.bots.OnPlacement(c.countPlacement)
	h.bots.OnContract(c.countOutcome)
	h.bots.OnPlacementError(c.countError)
	h.bots.OnStop(c.countStopped)

	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(4),
	})

	d.resolve(run.ID, "won", "lost", "void", "won")
	final := assertStopped(t, h, run.ID, models.BotStopMaxContracts)

	if final.ContractsPlaced != 4 {
		t.Fatalf("the run placed %d contracts, want 4", final.ContractsPlaced)
	}

	c.mu.Lock()
	placed, outcomes := c.placed, map[string]int{}
	for k, v := range c.outcomes {
		outcomes[k] = v
	}
	c.mu.Unlock()

	if placed != 4 {
		t.Fatalf("placement counter %d, want 4 — one per contract", placed)
	}
	if got := c.total(c.outcomes); got != 4 {
		t.Fatalf("outcome counters total %d, want 4 — one per contract, not one per event: %v",
			got, outcomes)
	}
	// And broken down the way the alert's sibling panels read them.
	if outcomes["won"] != 2 || outcomes["lost"] != 1 || outcomes["void"] != 1 {
		t.Fatalf("outcomes %v, want 2 won / 1 lost / 1 void", outcomes)
	}
	// "placed" is not an outcome. Its presence here is the old double count.
	if _, ok := outcomes["placed"]; ok {
		t.Fatalf("outcome counter has a 'placed' label: %v — that is the double count", outcomes)
	}
	if got := c.total(c.errors); got != 0 {
		t.Fatalf("%d placement errors on a clean run", got)
	}
	if c.stopped[models.BotStopMaxContracts] != 1 {
		t.Fatalf("stop counters %v, want one max_contracts", c.stopped)
	}
	h.assertReconciled(accountID)
}

// A run that cannot place must not look like a run that is not placing. Before
// this counter the two were indistinguishable: both leave the placement counter
// flat.
func TestBotPlacementErrorsAreCountedByCode(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	c := newCounters()
	h.bots.OnPlacement(c.countPlacement)
	h.bots.OnPlacementError(c.countError)

	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})

	// One contract settles, then the account is frozen under the run. The next
	// placement is refused inside Open's transaction, which is a placement error
	// with a name — as opposed to a run that has simply gone quiet.
	d.win(d.awaitOpen(run.ID))
	if _, err := h.admin.Freeze(h.ctx, adminID, accountID,
		"Freezing for a compliance review of this account"); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	assertStopped(t, h, run.ID, models.BotStopAccountFrozen)

	c.mu.Lock()
	errs := map[string]int{}
	for k, v := range c.errors {
		errs[k] = v
	}
	placed := c.placed
	c.mu.Unlock()

	if errs["account_frozen"] != 1 {
		t.Fatalf("placement errors %v, want one account_frozen", errs)
	}
	// The contract that did go through is still counted once, and the refusal is
	// not counted as a placement.
	if placed != 1 {
		t.Fatalf("placement counter %d, want 1 — the refusal is not a placement", placed)
	}
	h.assertReconciled(accountID)
}
