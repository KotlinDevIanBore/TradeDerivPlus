//go:build integration

package service_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// A stop asked for while a contract is in flight must not abandon that contract:
// its result belongs to the run, and the run holds the account's live slot until
// it lands. That intermediate state is what 'stopping' is for.
func TestBotUserStopWaitsForInFlight(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	inFlight := d.awaitOpen(run.ID)

	stopped, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser)
	if err != nil {
		t.Fatalf("stop run: %v", err)
	}
	if stopped.Status != models.BotStopping {
		t.Fatalf("status %q while a contract is in flight, want stopping", stopped.Status)
	}

	// The slot is still taken, so nothing can start in the gap.
	if _, err := d.h.bots.StartRun(h.ctx, service.StartRunInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10), IntervalMS: 1,
		Limits: models.BotLimitsConfig{StopLoss: decimal.NewFromInt(50)},
	}); !errors.Is(err, service.ErrBotRunActive) {
		t.Fatalf("second run while stopping: %v, want ErrBotRunActive", err)
	}

	d.win(inFlight)

	final := assertStopped(t, h, run.ID, models.BotStopUser)
	// The contract that was in flight is counted, which is the whole point of
	// waiting rather than finalising immediately.
	if final.Wins != 1 || final.ContractsPlaced != 1 {
		t.Fatalf("placed %d wins %d, want 1 and 1 — the in-flight contract must be counted",
			final.ContractsPlaced, final.Wins)
	}
	if !final.RealisedPnL.Equal(decimal.RequireFromString("9.52")) {
		t.Fatalf("realised %s, want 9.52", final.RealisedPnL)
	}
	assertNoFurtherPlacements(t, h, d, run.ID)
	h.assertReconciled(accountID)
}

// Stopping a stopped run is not an error: the caller's intent is already met.
func TestBotStopRunIsIdempotent(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(1),
	})
	d.resolve(run.ID, models.DigitWon)
	assertStopped(t, h, run.ID, models.BotStopMaxContracts)

	again, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser)
	if err != nil {
		t.Fatalf("stopping a stopped run: %v, want no error", err)
	}
	// And the original reason survives: whoever stopped it first owns the record.
	if again.StopReason == nil || *again.StopReason != models.BotStopMaxContracts {
		t.Fatalf("reason %v, want it to stay max_contracts", again.StopReason)
	}
}

func TestBotRefusesSecondRunOnSameAccount(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})

	second := func() error {
		_, err := h.bots.StartRun(h.ctx, service.StartRunInput{
			UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
			ContractType: models.DigitEvenOdd,
			Selection:    models.DigitSelection{Parity: models.DigitParityOdd},
			Stake:        decimal.NewFromInt(10), IntervalMS: 1,
			Limits: models.BotLimitsConfig{StopLoss: decimal.NewFromInt(50)},
		})
		return err
	}

	if err := second(); !errors.Is(err, service.ErrBotRunActive) {
		t.Fatalf("while running: %v, want ErrBotRunActive", err)
	}

	// And while stopping, which is the case the partial unique index covers by
	// treating 'stopping' as live.
	inFlight := d.awaitOpen(run.ID)
	if _, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := second(); !errors.Is(err, service.ErrBotRunActive) {
		t.Fatalf("while stopping: %v, want ErrBotRunActive", err)
	}

	// Once stopped, the slot frees.
	d.win(inFlight)
	assertStopped(t, h, run.ID, models.BotStopUser)
	if err := second(); err != nil {
		t.Fatalf("after stopped: %v, want a new run to be allowed", err)
	}
	h.assertReconciled(accountID)
}

// The kill switch: one transaction stops every live run, turns the feature off and
// records who did it. Then nothing can start again.
func TestBotKillSwitchStopsAllRuns(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()

	type owned struct {
		userID, accountID uuid.UUID
		run               *models.BotRun
	}
	var runs []owned
	for i := 0; i < 3; i++ {
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
			StopLoss: decimal.NewFromInt(500),
		})
		runs = append(runs, owned{userID, accountID, run})
	}

	live, err := h.store.BotRuns.CountLive(h.ctx)
	if err != nil || live != 3 {
		t.Fatalf("live runs %d err %v, want 3", live, err)
	}

	// A reason is required, like every other action that touches money.
	if _, err := h.admin.KillBots(h.ctx, adminID, "short"); err == nil {
		t.Fatal("kill with a one-word reason should be refused")
	}

	const reason = "Halting automated trading pending a payout table review"
	stopped, err := h.admin.KillBots(h.ctx, adminID, reason)
	if err != nil {
		t.Fatalf("kill: %v", err)
	}
	if stopped != 3 {
		t.Fatalf("stopped %d runs, want 3", stopped)
	}

	for _, o := range runs {
		final := awaitRunStopped(t, h, o.run.ID)
		if final.StopReason == nil || *final.StopReason != models.BotStopAdminKill {
			t.Fatalf("run %s reason %v, want admin_kill", o.run.ID, final.StopReason)
		}
		h.assertReconciled(o.accountID)
	}

	// Every owner is told, exactly once. The kill switch's own UPDATE writes all
	// three endings, so no goroutine wins its Finalise and none of them
	// announces anything — the switch owes the announcement itself. A user whose
	// run vanished with no explanation is the failure this guards.
	for _, o := range runs {
		endings := 0
		for _, e := range h.events.ofType("bot_run") {
			row, ok := e.Payload.(*models.BotRun)
			if ok && row.ID == o.run.ID && row.Status == models.BotStopped {
				endings++
			}
		}
		if endings != 1 {
			t.Fatalf("run %s ending broadcast %d times, want 1", o.run.ID, endings)
		}
	}

	// The flag went with it, so users cannot simply start again.
	raw, err := h.store.Settings.Get(h.ctx, repository.SettingBotEnabled)
	if err != nil || raw != "false" {
		t.Fatalf("bot_enabled = %q err %v, want false", raw, err)
	}
	if _, err := h.bots.StartRun(h.ctx, service.StartRunInput{
		UserID: runs[0].userID, AccountID: runs[0].accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10), IntervalMS: 1,
		Limits: models.BotLimitsConfig{StopLoss: decimal.NewFromInt(50)},
	}); !errors.Is(err, service.ErrBotDisabled) {
		t.Fatalf("start after kill: %v, want ErrBotDisabled", err)
	}

	// And it is on the record.
	entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionBotKill, 10, 0)
	if err != nil {
		t.Fatalf("audit list: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d bot_kill audit rows, want 1", len(entries))
	}
	if entries[0].Reason != reason {
		t.Fatalf("audit reason %q, want %q", entries[0].Reason, reason)
	}
	if entries[0].TargetType != models.TargetPlatform {
		t.Fatalf("audit target %q, want platform", entries[0].TargetType)
	}
}

// Runs do not resume across a restart, and a contract still open when the process
// comes back still folds into its run's totals — which is the property that keeps
// the counters in the settlement transaction rather than in the loop.
func TestBotRestartMarksRunsStoppedAndTotalsReconcile(t *testing.T) {
	h := newHarness(t)
	h.digits.Start(h.ctx)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(botSymbol, decimal.RequireFromString("100.10"), 2000)

	// A run row as a crashed process would have left it: live, with a contract
	// still open. Written directly rather than through the loop, because the point
	// is what happens to state the loop is no longer watching.
	symbol, err := h.store.Symbols.GetByCode(h.ctx, botSymbol)
	if err != nil {
		t.Fatalf("symbol: %v", err)
	}
	run := &models.BotRun{
		AccountID: accountID, SymbolID: symbol.ID,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10), IntervalMS: 1000,
		Limits:          models.BotLimitsConfig{StopLoss: decimal.NewFromInt(500)},
		StartingBalance: decimal.NewFromInt(1000),
	}
	if err := h.store.BotRuns.Create(h.ctx, run); err != nil {
		t.Fatalf("create run: %v", err)
	}
	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
		BotRunID:     &run.ID,
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// A tick the crashed process never saw: present on the feed, unobserved.
	h.feed.InjectTickQuiet(botSymbol, decimal.RequireFromString(botEvenPrice), 2001)

	// Boot order, as main.go has it: the settler's sweep, then the bot's restart
	// sweep.
	if err := h.bots.Start(h.ctx); err != nil {
		t.Fatalf("bots start: %v", err)
	}

	after, err := h.store.BotRuns.Get(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if after.Status != models.BotStopped {
		t.Fatalf("status %q after restart, want stopped", after.Status)
	}
	if after.StopReason == nil || *after.StopReason != models.BotStopRestart {
		t.Fatalf("reason %v, want restart", after.StopReason)
	}

	// The straggler settles afterwards and still reaches its run, even though the
	// row is already stopped. A reconciliation pass at boot would have missed it.
	h.digits.SweepForTest(h.ctx)

	settled, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get contract: %v", err)
	}
	if settled.Status != models.DigitWon {
		t.Fatalf("contract %s, want won", settled.Status)
	}
	final, err := h.store.BotRuns.Get(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Wins != 1 {
		t.Fatalf("wins %d after the straggler settled, want 1", final.Wins)
	}
	if !final.RealisedPnL.Equal(decimal.RequireFromString("9.52")) {
		t.Fatalf("realised %s, want 9.52", final.RealisedPnL)
	}
	// Stopped stays stopped: a late settlement must not revive a run.
	if final.Status != models.BotStopped {
		t.Fatalf("status %q, want it to stay stopped", final.Status)
	}
	h.assertReconciled(accountID)
}

// On shutdown a run writes its ending on a detached context, because the root one
// is already cancelled and the write would otherwise fail silently — leaving a row
// that claims to be live forever and blocking that account's next run.
func TestBotShutdownFinalisesRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	ctx, cancel := context.WithCancel(context.Background())
	if err := h.bots.Start(ctx); err != nil {
		t.Fatalf("bots start: %v", err)
	}

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	inFlight := d.awaitOpen(run.ID)

	cancel()
	shutdownCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
	defer done()
	h.bots.Shutdown(shutdownCtx)

	final, err := h.store.BotRuns.Get(h.ctx, run.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if final.Status != models.BotStopped {
		t.Fatalf("status %q after shutdown, want stopped", final.Status)
	}
	if final.StopReason == nil || *final.StopReason != models.BotStopShutdown {
		t.Fatalf("reason %v, want shutdown", derefReason(final.StopReason))
	}

	// The contract was left for the next process rather than abandoned: the void
	// grace refunds it, and the money still balances.
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE digit_contracts SET void_after = now() - interval '1 second' WHERE id = $1`, inFlight.ID); err != nil {
		t.Fatalf("backdate void_after: %v", err)
	}
	h.digits.SweepForTest(h.ctx)

	c, err := h.store.DigitContracts.GetByID(h.ctx, inFlight.ID)
	if err != nil {
		t.Fatalf("get contract: %v", err)
	}
	if c.Status != models.DigitVoid {
		t.Fatalf("contract %s, want void after the boot sweep", c.Status)
	}
	h.assertReconciled(accountID)
}

// A bot and a person trading the same account at once must not be able to spend
// more than is there. The locking is DigitService's, already tested; this proves
// the bot inherits it rather than working around it.
func TestBotAndHumanConcurrentOpensNeverOverdraw(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(100))

	// A stop loss cannot exceed the balance, so it is sized to the account rather
	// than to the run's ambitions.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(50),
	})

	var refused, opened atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := h.digits.Open(h.ctx, service.OpenDigitInput{
				UserID: userID, AccountID: accountID, SymbolCode: botSymbol,
				ContractType: models.DigitEvenOdd,
				Selection:    models.DigitSelection{Parity: models.DigitParityOdd},
				Stake:        decimal.NewFromInt(10),
			})
			switch {
			case err == nil:
				opened.Add(1)
			case errors.Is(err, service.ErrInsufficientFunds),
				errors.Is(err, service.ErrTooManyOpen),
				errors.Is(err, service.ErrAccountFrozen):
				refused.Add(1)
			default:
				t.Errorf("unexpected error from a human open: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	// Settle everything the burst left open, then stop the run.
	for i := 0; i < 10; i++ {
		d.tick(botEvenPrice)
		time.Sleep(15 * time.Millisecond)
	}
	if _, err := h.bots.StopRun(h.ctx, userID, run.ID, models.BotStopUser); err != nil {
		t.Fatalf("stop run: %v", err)
	}
	awaitRunStopped(t, h, run.ID)

	// The invariant: a wallet is never negative and the ledger still agrees with
	// it, whoever was placing.
	w := h.wallet_(accountID)
	if w.RealBalance.IsNegative() || w.BonusBalance.IsNegative() {
		t.Fatalf("negative balance: real %s bonus %s", w.RealBalance, w.BonusBalance)
	}
	if opened.Load() == 0 {
		t.Fatal("no human open succeeded; the test proved nothing")
	}
	h.assertReconciled(accountID)
}

func TestBotStopsOnInsufficientFunds(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	// The money is taken away mid-run rather than the run being started too small.
	// A stake must be within a tenth of the balance and a stop loss within the
	// balance, so a run that could exhaust its own funds before its stop loss
	// cannot be configured — which leaves an adjustment (or a withdrawal) as the
	// realistic way an account runs dry underneath a live run.
	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(900),
	})
	d.resolve(run.ID, models.DigitLost)

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID:     adminID,
		AccountID:   accountID,
		Amount:      decimal.NewFromInt(-985),
		BalanceType: models.BalanceBonus,
		Reason:      "Withdrawing the balance while a run is live, for this test",
	}); err != nil {
		t.Fatalf("adjust: %v", err)
	}

	final := assertStopped(t, h, run.ID, models.BotStopInsufficientFunds)
	if final.ContractsPlaced != 1 {
		t.Fatalf("placed %d, want 1 before the money went away", final.ContractsPlaced)
	}
	h.assertReconciled(accountID)
}

// Freezing an account stops its run, and does so without any bot-specific code:
// the freeze check already lives inside Open's transaction.
func TestBotFreezeStopsRun(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500),
	})
	inFlight := d.awaitOpen(run.ID)
	d.win(inFlight)

	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, "Freezing for a compliance review of this account"); err != nil {
		t.Fatalf("freeze: %v", err)
	}

	final := assertStopped(t, h, run.ID, models.BotStopAccountFrozen)
	if final.Wins != 1 {
		t.Fatalf("wins %d, want the pre-freeze win to be counted", final.Wins)
	}
	h.assertReconciled(accountID)
}

// A poll that lands in the window where a later tick is visible but the observer
// has not run yet will try to settle, and may lose the row lock to whoever got
// there first. That is a re-read, not a failure, and the run must carry on.
func TestBotLosesSettleRaceGracefully(t *testing.T) {
	h := newHarness(t)
	d := newBotDriver(t, h)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	run := d.startEvenOddRun(userID, accountID, 10, models.BotLimitsConfig{
		StopLoss: decimal.NewFromInt(500), MaxContracts: intp(1),
	})
	inFlight := d.awaitOpen(run.ID)

	// The tick exists but no observer ran, so the bot's own poll and these racers
	// are all eligible to settle it.
	tick := models.Tick{
		Symbol: botSymbol,
		Price:  decimal.RequireFromString(botEvenPrice),
		Epoch:  d.nextEpoch(),
	}
	h.feed.InjectTickQuiet(botSymbol, tick.Price, tick.Epoch)

	var settled atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := h.store.DigitContracts.GetByID(h.ctx, inFlight.ID)
			if err != nil {
				return
			}
			if _, err := h.digits.SettleForTest(h.ctx, c, tick, "race"); err == nil {
				settled.Add(1)
			}
		}()
	}
	wg.Wait()

	// At most one racer can win, and the bot may have settled it first — so the
	// contract settled exactly once either way.
	if settled.Load() > 1 {
		t.Fatalf("%d racers settled the same contract", settled.Load())
	}
	c, err := h.store.DigitContracts.GetByID(h.ctx, inFlight.ID)
	if err != nil {
		t.Fatalf("get contract: %v", err)
	}
	if c.Status != models.DigitWon {
		t.Fatalf("contract %s, want won", c.Status)
	}

	// And the run counted it once, not once per racer.
	final := assertStopped(t, h, run.ID, models.BotStopMaxContracts)
	if final.Wins != 1 || final.ContractsPlaced != 1 {
		t.Fatalf("placed %d wins %d, want 1 and 1", final.ContractsPlaced, final.Wins)
	}
	if !final.RealisedPnL.Equal(decimal.RequireFromString("9.52")) {
		t.Fatalf("realised %s, want 9.52 counted once", final.RealisedPnL)
	}
	h.assertReconciled(accountID)
}

// Every gate StartRun applies, each forced on its own so a failure names one rule.
func TestBotStartGates(t *testing.T) {
	h := newHarness(t)
	newBotDriver(t, h)

	base := func(accountID uuid.UUID) service.StartRunInput {
		return service.StartRunInput{
			AccountID: accountID, SymbolCode: botSymbol,
			ContractType: models.DigitEvenOdd,
			Selection:    models.DigitSelection{Parity: models.DigitParityEven},
			Stake:        decimal.NewFromInt(10), IntervalMS: 1000,
			Limits: models.BotLimitsConfig{StopLoss: decimal.NewFromInt(50)},
		}
	}

	t.Run("flag off", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		if err := h.store.Settings.Set(h.ctx, repository.SettingBotEnabled, "false"); err != nil {
			t.Fatal(err)
		}
		h.settings.BustCache()
		defer func() {
			_ = h.store.Settings.Set(h.ctx, repository.SettingBotEnabled, "true")
			h.settings.BustCache()
		}()

		in := base(accountID)
		in.UserID = userID
		if _, err := h.bots.StartRun(h.ctx, in); !errors.Is(err, service.ErrBotDisabled) {
			t.Fatalf("%v, want ErrBotDisabled", err)
		}
	})

	t.Run("not on the allowlist", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		// Somebody else's id, so the list is non-empty but excludes this user.
		if err := h.store.Settings.Set(h.ctx, repository.SettingBotAllowlist, uuid.New().String()); err != nil {
			t.Fatal(err)
		}
		h.settings.BustCache()
		defer func() {
			_ = h.store.Settings.Set(h.ctx, repository.SettingBotAllowlist, "")
			h.settings.BustCache()
		}()

		in := base(accountID)
		in.UserID = userID
		if _, err := h.bots.StartRun(h.ctx, in); !errors.Is(err, service.ErrBotDisabled) {
			t.Fatalf("%v, want ErrBotDisabled", err)
		}

		// Adding them lets them in, which proves the list is doing the refusing
		// rather than something else.
		if err := h.store.Settings.Set(h.ctx, repository.SettingBotAllowlist, userID.String()); err != nil {
			t.Fatal(err)
		}
		h.settings.BustCache()
		if _, err := h.bots.StartRun(h.ctx, in); err != nil {
			t.Fatalf("allowlisted user refused: %v", err)
		}
	})

	t.Run("real account while real money is off", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(1000)) // newUser makes a real account
		if err := h.store.Settings.Set(h.ctx, repository.SettingBotRealMoneyEnabled, "false"); err != nil {
			t.Fatal(err)
		}
		h.settings.BustCache()
		defer func() {
			_ = h.store.Settings.Set(h.ctx, repository.SettingBotRealMoneyEnabled, "true")
			h.settings.BustCache()
		}()

		in := base(accountID)
		in.UserID = userID
		if _, err := h.bots.StartRun(h.ctx, in); !errors.Is(err, service.ErrBotRealMoneyDisabled) {
			t.Fatalf("%v, want ErrBotRealMoneyDisabled", err)
		}

		// A demo account on the same user is allowed, so it is the account type
		// being refused and not the user.
		// newDemoAccount goes through WalletService, which funds it, so there is
		// no wallet to create here.
		in.AccountID = h.newDemoAccount(userID)
		if _, err := h.bots.StartRun(h.ctx, in); err != nil {
			t.Fatalf("demo account refused: %v", err)
		}
	})

	t.Run("stake above the per-contract ceiling", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(100000))
		in := base(accountID)
		in.UserID = userID
		in.Stake = decimal.NewFromInt(101) // DigitLimits.MaxStake is 100
		assertStartRefused(t, h, in, "stake")
	})

	t.Run("stake above a tenth of the balance", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(50))
		in := base(accountID)
		in.UserID = userID
		in.Stake = decimal.NewFromInt(10) // 20% of 50
		assertStartRefused(t, h, in, "stake")
	})

	t.Run("interval below the floor", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		in := base(accountID)
		in.UserID = userID
		in.IntervalMS = 0
		assertStartRefused(t, h, in, "interval_ms")
	})

	t.Run("no stop loss", func(t *testing.T) {
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		in := base(accountID)
		in.UserID = userID
		in.Limits = models.BotLimitsConfig{}
		assertStartRefused(t, h, in, "stop_loss")
	})

	t.Run("frozen account", func(t *testing.T) {
		adminID := h.newAdmin()
		userID, accountID := h.newUser(decimal.NewFromInt(1000))
		if _, err := h.admin.Freeze(h.ctx, adminID, accountID, "Frozen for the purposes of this test case"); err != nil {
			t.Fatalf("freeze: %v", err)
		}
		in := base(accountID)
		in.UserID = userID
		if _, err := h.bots.StartRun(h.ctx, in); !errors.Is(err, service.ErrAccountFrozen) {
			t.Fatalf("%v, want ErrAccountFrozen", err)
		}
	})

	t.Run("someone else's account", func(t *testing.T) {
		_, accountID := h.newUser(decimal.NewFromInt(1000))
		otherUser, _ := h.newUser(decimal.NewFromInt(1000))
		in := base(accountID)
		in.UserID = otherUser
		if _, err := h.bots.StartRun(h.ctx, in); !errors.Is(err, service.ErrForbidden) {
			t.Fatalf("%v, want ErrForbidden", err)
		}
	})

	t.Run("at capacity", func(t *testing.T) {
		// Its own service, because capacity is a property of the process rather
		// than of the row, and the harness's allows fifty.
		small := service.NewBotService(h.store, h.digits, h.feed, h.settings, h.events,
			service.BotLimits{MinIntervalMS: 1, MaxRuns: 1}, h.log)
		if err := small.Start(h.ctx); err != nil {
			t.Fatalf("start: %v", err)
		}

		u1, a1 := h.newUser(decimal.NewFromInt(1000))
		in := base(a1)
		in.UserID = u1
		in.IntervalMS = 1
		if _, err := small.StartRun(h.ctx, in); err != nil {
			t.Fatalf("first run: %v", err)
		}

		u2, a2 := h.newUser(decimal.NewFromInt(1000))
		in2 := base(a2)
		in2.UserID = u2
		in2.IntervalMS = 1
		if _, err := small.StartRun(h.ctx, in2); !errors.Is(err, service.ErrBotCapacity) {
			t.Fatalf("%v, want ErrBotCapacity", err)
		}
	})
}

// assertStartRefused starts a run that should be refused and checks which field
// the refusal names, so the table above reads as one line per rule.
func assertStartRefused(t *testing.T, h *harness, in service.StartRunInput, field string) {
	t.Helper()
	_, err := h.bots.StartRun(h.ctx, in)
	var ve *service.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want a validation error, got %v", err)
	}
	if ve.Field != field {
		t.Fatalf("validation field %q, want %q (%s)", ve.Field, field, ve.Message)
	}
}
