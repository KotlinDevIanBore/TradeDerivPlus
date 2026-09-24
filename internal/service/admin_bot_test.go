//go:build integration

package service_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// The console's runs table. Before this, asking for finished runs returned an
// empty list by design, so a run that ended was unreachable from the console the
// moment it ended — which is exactly when an operator wants to look at it.
func TestAdminListsLiveAndFinishedRunsWithTheirOwners(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)

	// One run that is over, and one still going, on different users.
	pastUser, pastAccount := h.newUser(decimal.NewFromInt(1000))
	past := d.startEvenOddRun(pastUser, pastAccount, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(1),
	})
	d.resolve(past.ID, "won")
	assertStopped(t, h, past.ID, models.BotStopMaxContracts)

	liveUser, liveAccount := h.newUser(decimal.NewFromInt(1000))
	live := d.startEvenOddRun(liveUser, liveAccount, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})

	t.Run("finished runs are reachable", func(t *testing.T) {
		page, err := h.admin.ListBotRuns(h.ctx, models.BotStopped, 25, 0)
		if err != nil {
			t.Fatalf("list stopped: %v", err)
		}
		if page.Total != 1 || len(page.Items) != 1 {
			t.Fatalf("stopped runs: %d items of %d total, want 1 of 1", len(page.Items), page.Total)
		}
		if page.Items[0].ID != past.ID {
			t.Fatalf("stopped list returned run %s, want %s", page.Items[0].ID, past.ID)
		}
	})

	t.Run("live runs still are", func(t *testing.T) {
		page, err := h.admin.ListBotRuns(h.ctx, "live", 25, 0)
		if err != nil {
			t.Fatalf("list live: %v", err)
		}
		if page.Total != 1 || page.Items[0].ID != live.ID {
			t.Fatalf("live list: %d rows, first %v, want just %s", page.Total, page.Items, live.ID)
		}
	})

	t.Run("no status means both", func(t *testing.T) {
		page, err := h.admin.ListBotRuns(h.ctx, "", 25, 0)
		if err != nil {
			t.Fatalf("list all: %v", err)
		}
		if page.Total != 2 {
			t.Fatalf("total %d, want 2 — live and finished together", page.Total)
		}
		// Newest first, like every other admin table.
		if page.Items[0].ID != live.ID {
			t.Fatalf("first row %s, want the newer run %s", page.Items[0].ID, live.ID)
		}
	})

	t.Run("each row says whose it is", func(t *testing.T) {
		page, err := h.admin.ListBotRuns(h.ctx, "live", 25, 0)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		row := page.Items[0]
		// The table's "user, account" columns. Without these the console could
		// show a run but not who to contact about it.
		if row.UserID != liveUser.String() {
			t.Fatalf("user_id %q, want %q", row.UserID, liveUser)
		}
		if row.UserPhone == "" {
			t.Fatal("user_phone is empty; the console identifies users by phone")
		}
		if row.AccountNumber == "" {
			t.Fatal("account_number is empty")
		}
		account, err := h.store.Accounts.GetByID(h.ctx, row.AccountID)
		if err != nil {
			t.Fatalf("get account: %v", err)
		}
		if row.AccountNumber != account.AccountNumber || row.AccountType != account.AccountType {
			t.Fatalf("row says account %s/%s, the account row says %s/%s",
				row.AccountNumber, row.AccountType, account.AccountNumber, account.AccountType)
		}
		// And still everything the run row carried before.
		if row.SymbolCode != botSymbol {
			t.Fatalf("symbol_code %q, want %q", row.SymbolCode, botSymbol)
		}
	})

	t.Run("history pages", func(t *testing.T) {
		first, err := h.admin.ListBotRuns(h.ctx, "", 1, 0)
		if err != nil {
			t.Fatalf("page 1: %v", err)
		}
		second, err := h.admin.ListBotRuns(h.ctx, "", 1, 1)
		if err != nil {
			t.Fatalf("page 2: %v", err)
		}
		if len(first.Items) != 1 || len(second.Items) != 1 {
			t.Fatalf("pages of 1 returned %d and %d rows", len(first.Items), len(second.Items))
		}
		// The count is of everything, not of the page — that is what makes a
		// pager able to say how many pages there are.
		if first.Total != 2 || second.Total != 2 {
			t.Fatalf("totals %d and %d, want 2 on both pages", first.Total, second.Total)
		}
		if first.Items[0].ID == second.Items[0].ID {
			t.Fatal("both pages returned the same run")
		}
	})
}

// startIdleRun starts a run and waits until its loop is parked between
// contracts, so a stop below lands on an idle run rather than in the middle of a
// placement. The long interval is what makes that window wide enough to be a
// fact rather than a coincidence: every other bot test uses 1ms, where the loop
// is essentially never idle.
func startIdleRun(t *testing.T, h *harness, d *botDriver, userID, accountID uuid.UUID) *models.BotRun {
	t.Helper()
	run, err := h.bots.StartRun(h.ctx, service.StartRunInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
		IntervalMS:   2000,
		Limits:       models.BotLimitsConfig{StopLoss: decimal.NewFromInt(500)},
	})
	if err != nil {
		t.Fatalf("start run: %v", err)
	}
	d.win(d.awaitOpen(run.ID))
	// The settlement is recorded before the loop sleeps; give it the moment in
	// between.
	time.Sleep(100 * time.Millisecond)
	if open, err := h.store.DigitContracts.CountOpenByRun(h.ctx, run.ID); err != nil || open != 0 {
		t.Fatalf("run has %d open contracts (err %v); it was meant to be idle", open, err)
	}
	return run
}

// A per-row stop. The kill switch stops everything and turns the feature off;
// this stops one run and leaves everyone else trading.
func TestAdminStopsOneRunAndRecordsIt(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()

	targetUser, targetAccount := h.newUser(decimal.NewFromInt(1000))
	target := startIdleRun(t, h, d, targetUser, targetAccount)
	otherUser, otherAccount := h.newUser(decimal.NewFromInt(1000))
	other := d.startEvenOddRun(otherUser, otherAccount, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})

	// A reason is required, exactly as it is for a freeze or an adjustment.
	if _, err := h.admin.StopBotRun(h.ctx, adminID, target.ID, "spam"); err == nil {
		t.Fatal("a one-word reason should be refused")
	}

	const reason = "Stopping this run at the account holder's request over the phone"
	if _, err := h.admin.StopBotRun(h.ctx, adminID, target.ID, reason); err != nil {
		t.Fatalf("stop run: %v", err)
	}

	final := assertStopped(t, h, target.ID, models.BotStopAdminStop)
	if final.StopReason == nil || *final.StopReason == models.BotStopAdminKill {
		t.Fatalf("reason %s: a single stop must not be recorded as the kill switch",
			derefReason(final.StopReason))
	}
	assertNoFurtherPlacements(t, h, d, target.ID)
	h.assertReconciled(targetAccount)

	t.Run("the platform keeps running", func(t *testing.T) {
		// The other run is untouched, and automated trading is still on — which
		// is the whole difference between this and the kill switch.
		still, err := h.store.BotRuns.Get(h.ctx, other.ID)
		if err != nil {
			t.Fatalf("get other run: %v", err)
		}
		if !still.IsLive() {
			t.Fatalf("the other run is %q; stopping one run must not stop the rest", still.Status)
		}
		raw, err := h.store.Settings.Get(h.ctx, repository.SettingBotEnabled)
		if err != nil || raw != "true" {
			t.Fatalf("bot_enabled = %q err %v, want it left on", raw, err)
		}
		h.assertReconciled(otherAccount)
	})

	t.Run("its own audited action", func(t *testing.T) {
		entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotRunStop, 10, 0)
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("%d bot_run_stop audit rows, want 1", len(entries))
		}
		e := entries[0]
		if e.Reason != reason {
			t.Fatalf("audit reason %q, want %q", e.Reason, reason)
		}
		if e.TargetType != models.TargetBotRun || e.TargetID != target.ID {
			t.Fatalf("audit target %s/%s, want bot_run/%s", e.TargetType, e.TargetID, target.ID)
		}
		// The refused attempt wrote nothing, and nothing was filed under the
		// kill switch's name.
		kills, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotKill, 10, 0)
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		if len(kills) != 0 {
			t.Fatalf("%d bot_kill rows, want 0 — a per-run stop is not a kill", len(kills))
		}
	})

	t.Run("stopping a stopped run changes nothing", func(t *testing.T) {
		again, err := h.admin.StopBotRun(h.ctx, adminID, target.ID, reason)
		if err != nil {
			t.Fatalf("second stop: %v", err)
		}
		if again.StopReason == nil || *again.StopReason != models.BotStopAdminStop {
			t.Fatalf("reason %s after a second stop", derefReason(again.StopReason))
		}
		// No second audit row: an action that changed nothing must not read as
		// an operator having stopped something.
		entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotRunStop, 10, 0)
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("%d bot_run_stop rows after a repeat stop, want 1", len(entries))
		}
	})

	t.Run("a run that does not exist", func(t *testing.T) {
		if _, err := h.admin.StopBotRun(h.ctx, adminID, uuid.New(), reason); !errors.Is(err, service.ErrNotFound) {
			t.Fatalf("stop of an unknown run: %v, want ErrNotFound", err)
		}
	})
}

// An admin stop lands the same way a user's does when a contract is in flight:
// the run waits for it, counts it, and only then ends.
func TestAdminStopWaitsForAnInFlightContract(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	inFlight := d.awaitOpen(run.ID)

	stopped, err := h.admin.StopBotRun(h.ctx, adminID, run.ID,
		"Stopping a run while the account is reviewed for suspicious activity")
	if err != nil {
		t.Fatalf("stop run: %v", err)
	}
	if stopped.Status != models.BotStopping {
		t.Fatalf("status %q with a contract in flight, want stopping", stopped.Status)
	}

	d.win(inFlight)

	final := assertStopped(t, h, run.ID, models.BotStopAdminStop)
	if final.ContractsPlaced != 1 || final.Wins != 1 {
		t.Fatalf("placed %d wins %d, want the in-flight contract counted",
			final.ContractsPlaced, final.Wins)
	}
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

// A run ends once, so it is announced once.
//
// Two parties try to write the ending of a stopped run: whoever asked for the
// stop, and the run's own goroutine on its way out. Both used to broadcast, and
// a user who stopped a run was told twice that it had stopped.
func TestARunStopIsAnnouncedOnce(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// Idle, so the stop request itself writes the ending while the run's
	// goroutine is still alive to try the same thing. That is the case the two
	// broadcasts came from; a run with a contract in flight has only one writer.
	run := startIdleRun(t, h, d, userID, accountID)

	if _, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	assertStopped(t, h, run.ID, models.BotStopUser)
	// The goroutine exits after the row is written; give it the chance to
	// broadcast if it were going to.
	assertNoFurtherPlacements(t, h, d, run.ID)

	endings := 0
	for _, e := range h.events.ofType("bot_run") {
		row, ok := e.Payload.(*models.BotRun)
		if !ok || row.ID != run.ID {
			continue
		}
		if row.Status == models.BotStopped {
			endings++
		}
	}
	if endings != 1 {
		t.Fatalf("the run's ending was broadcast %d times, want 1", endings)
	}
	h.assertReconciled(accountID)
}

// The way back. Without it the console can end automated trading and not restart
// it, which makes the kill switch a decision only a database can undo.
func TestAdminReEnablesAutomatedTrading(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	if _, err := h.admin.KillBots(h.ctx, adminID, "Halting automated trading pending a feed investigation"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	awaitRunStopped(t, h, run.ID)

	start := func() error {
		_, err := h.bots.StartRun(h.ctx, service.StartRunInput{
			UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
			ContractType: models.DigitEvenOdd,
			Selection:    models.DigitSelection{Parity: models.DigitParityEven},
			Stake:        decimal.NewFromInt(10), IntervalMS: 1,
			Limits: models.BotLimitsConfig{StopLoss: decimal.NewFromInt(50)},
		})
		return err
	}

	if err := start(); !errors.Is(err, service.ErrBotDisabled) {
		t.Fatalf("start while killed: %v, want ErrBotDisabled", err)
	}

	// A reason is required, exactly as it is for unfreezing an account — the
	// other restorative action in this console.
	if err := h.admin.EnableBots(h.ctx, adminID, "back on"); err == nil {
		t.Fatal("a one-word reason should be refused")
	}

	const reason = "Feed confirmed healthy after the provider's incident closed"
	if err := h.admin.EnableBots(h.ctx, adminID, reason); err != nil {
		t.Fatalf("enable: %v", err)
	}

	// And it takes effect now, not when the settings cache next expires.
	if err := start(); err != nil {
		t.Fatalf("start after re-enabling: %v", err)
	}

	t.Run("the restart is on the record too", func(t *testing.T) {
		entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotEnable, 10, 0)
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("%d bot_enable audit rows, want 1", len(entries))
		}
		if entries[0].Reason != reason {
			t.Fatalf("audit reason %q, want %q", entries[0].Reason, reason)
		}
		if entries[0].TargetType != models.TargetPlatform {
			t.Fatalf("audit target %q, want platform", entries[0].TargetType)
		}
		// The kill and the restart are two rows, not one action recorded twice.
		kills, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotKill, 10, 0)
		if err != nil {
			t.Fatalf("audit list: %v", err)
		}
		if len(kills) != 1 {
			t.Fatalf("%d bot_kill rows, want 1", len(kills))
		}
	})

	t.Run("the dashboard says which it is", func(t *testing.T) {
		dash, err := h.admin.Dashboard(h.ctx)
		if err != nil {
			t.Fatalf("dashboard: %v", err)
		}
		if !dash.BotEnabled {
			t.Fatal("dashboard reports automated trading off after it was turned on")
		}
		if _, err := h.admin.KillBots(h.ctx, adminID, "Second kill, to check the console reports it"); err != nil {
			t.Fatalf("kill: %v", err)
		}
		dash, err = h.admin.Dashboard(h.ctx)
		if err != nil {
			t.Fatalf("dashboard: %v", err)
		}
		if dash.BotEnabled {
			t.Fatal("dashboard reports automated trading on after the kill switch")
		}
	})
}
