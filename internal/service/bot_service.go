package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// Defaults for the bot's own bounds, applied when a field of BotLimits is zero.
//
// These are not the user's stop conditions — those live on the run. These bound
// what the platform will allow any run to do, and they exist because the bot
// calls DigitService.Open directly and so never meets the HTTP rate limiter that
// bounds a browser.
const (
	defaultBotMinIntervalMS = 1000
	defaultBotMaxRuns       = 50

	// botPollEvery is how often a run asks whether its contract has settled.
	// Polling rather than a settlement callback: a callback would fire on the
	// feed goroutine (and on the sweep goroutine, and on HTTP goroutines via
	// EnsureSettled), and would still need this as a fallback for a lost wakeup.
	botPollEvery = 250 * time.Millisecond

	// botSettleSlack is how long past the void grace a run waits before it gives
	// up on a contract. Void is the backstop, so passing it means something is
	// wrong with settlement itself, not with the feed.
	botSettleSlack = 2 * time.Second

	// botOpenTimeout bounds one placement. Open takes row locks, so a human
	// placing on the same account can hold it up briefly; without a timeout the
	// wait would be unbounded.
	botOpenTimeout = 10 * time.Second

	// botColdStartRetries is how many times a run tolerates "no price yet" before
	// concluding the feed is not coming. This is a cold start only: a *stalled*
	// feed still has a last price, so Open succeeds and the contract voids —
	// which is why consecutive voids, not this, detect a feed that died.
	botColdStartRetries = 5

	// botMaxConsecutiveVoids is that detector. Not user-configurable: it is a
	// fault threshold, not a preference.
	botMaxConsecutiveVoids = 3

	// botFinaliseTimeout is the budget for writing a run's ending on a detached
	// context during shutdown, when the root context is already cancelled.
	botFinaliseTimeout = 5 * time.Second
)

// BotLimits bounds every run. A zero field takes the default; there is no way to
// say "unlimited", for the same reason DigitLimits has none.
type BotLimits struct {
	MinIntervalMS int
	MaxRuns       int
}

func (l BotLimits) withDefaults() BotLimits {
	if l.MinIntervalMS <= 0 {
		l.MinIntervalMS = defaultBotMinIntervalMS
	}
	if l.MaxRuns <= 0 {
		l.MaxRuns = defaultBotMaxRuns
	}
	return l
}

// botFlags is the slice of SettingsService the bot needs. An interface so the
// service can be unit-tested without a database behind the flags.
type botFlags interface {
	BotEnabledFor(ctx context.Context, userID uuid.UUID) (bool, error)
	BotRealMoneyEnabled(ctx context.Context) (bool, error)
}

// liveRun is the in-process handle on a running goroutine. The database row is
// the durable truth; this is only what lets the process talk to the loop.
type liveRun struct {
	userID uuid.UUID

	// stop is closed to ask the loop to finish. Closed at most once, guarded by
	// stopOnce, because a user stop and an admin kill can arrive together.
	stop     chan struct{}
	stopOnce sync.Once

	// reason is why the stop was asked for, written before stop is closed and
	// read after, so the two are ordered by the channel close.
	mu     sync.Mutex
	reason string
}

func (r *liveRun) requestStop(reason string) {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.reason = reason
		r.mu.Unlock()
		close(r.stop)
	})
}

func (r *liveRun) stopReason() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reason
}

// BotService runs automated digit-contract sessions.
//
// One goroutine per run, one contract in flight at a time. It does not register a
// tick observer: FeedService runs observers synchronously on the single feed
// goroutine with no way to unregister, so a bot living there would stall price
// delivery for every connected user and could not be stopped cleanly.
//
// Every contract goes through DigitService.Open. There is no second path into the
// money code, so the row locking, the ledger write, the freeze check, the stake
// ceiling and the open-contract cap are all the ones already under test.
type BotService struct {
	store  *repository.Store
	digits *DigitService
	feed   *FeedService
	flags  botFlags
	hub    Broadcaster
	limits BotLimits
	log    *slog.Logger

	// rootCtx is the process lifetime, captured by Start. Placements and
	// settlement waits hang off it rather than off a per-run cancellable context:
	// cancelling mid-Open would be safe for the money but could leave a committed
	// contract the loop never saw, so a stop is a flag the loop consults, never a
	// cancellation of work in flight.
	rootCtx context.Context

	mu   sync.Mutex
	runs map[uuid.UUID]*liveRun
	wg   sync.WaitGroup

	// pollEvery is overridable so tests do not wait in real time.
	pollEvery time.Duration

	// Hooks for metrics, in the shape WalletService.OnDepositSettled established,
	// so this service never imports Prometheus.
	onContract   func(outcome string)
	onPlacement  func()
	onPlaceError func(code string)
	onStop       func(reason string)
}

func NewBotService(store *repository.Store, digits *DigitService, feed *FeedService, flags *SettingsService, hub Broadcaster, limits BotLimits, log *slog.Logger) *BotService {
	return newBotService(store, digits, feed, flags, hub, limits, log)
}

func newBotService(store *repository.Store, digits *DigitService, feed *FeedService, flags botFlags, hub Broadcaster, limits BotLimits, log *slog.Logger) *BotService {
	if log == nil {
		log = slog.Default()
	}
	return &BotService{
		store:     store,
		digits:    digits,
		feed:      feed,
		flags:     flags,
		hub:       hub,
		limits:    limits.withDefaults(),
		log:       log,
		rootCtx:   context.Background(),
		runs:      map[uuid.UUID]*liveRun{},
		pollEvery: botPollEvery,
	}
}

// OnContract, OnPlacement, OnPlacementError and OnStop register metric hooks.
//
// Placements and settlements are separate hooks rather than one with a label,
// because they are two events about one contract: a single hook was called twice
// per contract and every reading of the counter it fed was double the truth.
func (s *BotService) OnContract(fn func(outcome string))    { s.onContract = fn }
func (s *BotService) OnPlacement(fn func())                 { s.onPlacement = fn }
func (s *BotService) OnPlacementError(fn func(code string)) { s.onPlaceError = fn }
func (s *BotService) OnStop(fn func(reason string))         { s.onStop = fn }

// placementErrorCode names a failed placement the way the API names it, so the
// metric's labels and the error codes a client sees are the same vocabulary.
func placementErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrInsufficientFunds):
		return "insufficient_funds"
	case errors.Is(err, ErrAccountFrozen):
		return "account_frozen"
	case errors.Is(err, ErrMarketClosed):
		return "market_closed"
	case errors.Is(err, ErrNoPrice):
		return "no_price"
	case errors.Is(err, ErrTooManyOpen):
		return "too_many_open"
	case errors.Is(err, ErrBotRunNotLive):
		return "bot_run_not_live"
	default:
		return "error"
	}
}

// SetPollInterval is for tests, which would otherwise wait in real time.
func (s *BotService) SetPollInterval(d time.Duration) { s.pollEvery = d }

// Start adopts the process context and clears out runs left live by a previous
// process. Runs deliberately do not resume: a run silently restarting after a
// deploy, placing money on settings its user has forgotten, is the wrong default.
//
// This must run after DigitService.Start, whose boot sweep settles or voids what
// it can. Anything still open afterwards settles on a later tick and folds into
// its run's totals through the settlement transaction — the row is already
// stopped by then, and that is fine, which is the point of keeping the counters
// there rather than in the loop.
func (s *BotService) Start(ctx context.Context) error {
	s.rootCtx = ctx
	n, err := s.store.BotRuns.StopAllLive(ctx, models.BotStopRestart)
	if err != nil {
		return err
	}
	if n > 0 {
		s.log.Info("bot runs stopped by restart", "runs", n)
	}
	return nil
}

// Shutdown asks every run to finish and waits for them, so a deploy does not
// leave goroutines writing to a closing pool. Called beside srv.Shutdown.
func (s *BotService) Shutdown(ctx context.Context) {
	s.mu.Lock()
	for _, r := range s.runs {
		r.requestStop(models.BotStopShutdown)
	}
	s.mu.Unlock()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		s.log.Warn("bot shutdown timed out with runs still finishing")
	}
}

// StopAll is the admin kill switch's in-process half: the database half has
// already marked the rows, this stops the goroutines that would otherwise keep
// placing until their next check — and tells each run's owner that it ended.
//
// The telling is not optional and does not belong to the loops. The kill
// switch's own UPDATE wrote every ending, so no goroutine wins its Finalise and
// none of them announces anything; whoever writes an ending owes the
// announcement, and here that is this. Without it a user's run would vanish from
// under them with no explanation, which is the one thing the kill switch most
// owes them.
func (s *BotService) StopAll(reason string) int {
	s.mu.Lock()
	stopping := make([]uuid.UUID, 0, len(s.runs))
	for id, r := range s.runs {
		r.requestStop(reason)
		stopping = append(stopping, id)
	}
	s.mu.Unlock()

	// Outside the lock: each announcement reads the run and its account, and
	// holding the map while doing that would block every placement.
	ctx, cancel := context.WithTimeout(context.Background(), botFinaliseTimeout)
	defer cancel()
	for _, id := range stopping {
		s.announce(ctx, id)
	}
	return len(stopping)
}

// announce republishes a run to its owner. Used by the paths that write an
// ending outside the run's own goroutine, which is every path the console has.
func (s *BotService) announce(ctx context.Context, runID uuid.UUID) {
	run, owner, err := s.runAndOwner(ctx, runID)
	if err != nil {
		s.log.Warn("bot run announce failed", "run", runID, "err", err)
		return
	}
	s.broadcast(owner, run)
}

// LiveCount is how many runs this process is actually running, as opposed to how
// many rows say they are live. The two differing means a run was orphaned.
func (s *BotService) LiveCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs)
}

// StartRunInput is what the API accepts to begin a run.
type StartRunInput struct {
	UserID     uuid.UUID
	AccountID  uuid.UUID
	SymbolCode string

	ContractType string
	Selection    models.DigitSelection
	Stake        decimal.Decimal
	IntervalMS   int
	Limits       models.BotLimitsConfig
}

// StartRun validates, records and launches a run.
//
// Every gate here is server-side and none of it is reachable from the client's
// copy of the rules: the bot bypasses the HTTP rate limiter by construction, so
// this function is the only thing standing between a misconfigured run and a
// few hundred contracts.
func (s *BotService) StartRun(ctx context.Context, in StartRunInput) (*models.BotRun, error) {
	account, err := s.store.Accounts.GetByID(ctx, in.AccountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if account.UserID != in.UserID {
		return nil, ErrForbidden
	}
	if account.IsFrozen() {
		return nil, ErrAccountFrozen
	}

	enabled, err := s.flags.BotEnabledFor(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, ErrBotDisabled
	}

	if account.AccountType == models.AccountReal {
		realOK, err := s.flags.BotRealMoneyEnabled(ctx)
		if err != nil {
			return nil, err
		}
		if !realOK {
			return nil, ErrBotRealMoneyDisabled
		}
	}

	symbol, err := s.store.Symbols.GetByCode(ctx, in.SymbolCode)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !symbol.IsActive {
		return nil, ErrMarketClosed
	}
	if symbol.Category != models.CategoryVolatility {
		return nil, invalid("symbol", "Automated runs are only available on volatility indices")
	}

	if err := validateDigitSelection(in.ContractType, in.Selection); err != nil {
		return nil, err
	}

	wallet, err := s.store.Wallets.Get(ctx, in.AccountID)
	if err != nil {
		return nil, err
	}
	balance := wallet.Total()

	if err := s.validateRunConfig(in, balance); err != nil {
		return nil, err
	}

	// Checked before insert for a clear error, and enforced again by the partial
	// unique index, which is what actually makes it safe under a race.
	if existing, err := s.store.BotRuns.GetLiveByAccount(ctx, in.AccountID); err == nil {
		_ = existing
		return nil, ErrBotRunActive
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}

	// Capacity bounds the settlement load a bot can add to the feed goroutine:
	// every bot contract is settled by DigitService.onTick, one transaction each.
	live, err := s.store.BotRuns.CountLive(ctx)
	if err != nil {
		return nil, err
	}
	if live >= s.limits.MaxRuns {
		return nil, ErrBotCapacity
	}

	run := &models.BotRun{
		AccountID:       in.AccountID,
		SymbolID:        symbol.ID,
		ContractType:    in.ContractType,
		Selection:       in.Selection,
		Stake:           in.Stake.Round(2),
		IntervalMS:      in.IntervalMS,
		Limits:          in.Limits,
		StartingBalance: balance,
	}
	if err := s.store.BotRuns.Create(ctx, run); err != nil {
		if errors.Is(err, repository.ErrConflict) {
			return nil, ErrBotRunActive
		}
		return nil, err
	}

	created, err := s.store.BotRuns.Get(ctx, run.ID)
	if err != nil {
		return nil, err
	}

	s.launch(created, in.UserID)
	s.broadcast(in.UserID, created)
	s.log.Info("bot run started",
		"run", created.ID, "account", created.AccountID, "symbol", created.SymbolCode,
		"contract_type", created.ContractType, "stake", created.Stake.String(),
		"interval_ms", created.IntervalMS)
	return created, nil
}

func (s *BotService) validateRunConfig(in StartRunInput, balance decimal.Decimal) error {
	if in.Stake.LessThanOrEqual(decimal.Zero) {
		return invalid("stake", "Enter a stake greater than zero")
	}
	if in.Stake.GreaterThan(s.digits.MaxStake()) {
		return invalid("stake", "The most you can stake on one contract is $"+s.digits.MaxStake().StringFixed(2))
	}
	// A run places repeatedly, so a stake that is a large share of the balance is
	// a different risk from the same stake placed once by hand.
	maxByBalance := balance.Mul(decimal.RequireFromString("0.1")).Round(2)
	if in.Stake.GreaterThan(maxByBalance) {
		return invalid("stake", "An automated run can stake at most 10% of your balance, which is $"+maxByBalance.StringFixed(2))
	}
	if in.IntervalMS < s.limits.MinIntervalMS {
		return invalid("interval_ms", fmt.Sprintf("The shortest interval between contracts is %d seconds", s.limits.MinIntervalMS/1000))
	}
	// Stop loss is required. A run with no floor is not something this product
	// offers, and defaulting one silently would be worse than refusing.
	if in.Limits.StopLoss.LessThanOrEqual(decimal.Zero) {
		return invalid("stop_loss", "Set a stop loss so the run knows when to stop")
	}
	if in.Limits.StopLoss.GreaterThan(balance) {
		return invalid("stop_loss", "The stop loss cannot be more than your balance")
	}
	if tp := in.Limits.TakeProfit; tp != nil && tp.LessThanOrEqual(decimal.Zero) {
		return invalid("take_profit", "Enter a take profit greater than zero, or leave it off")
	}
	if n := in.Limits.MaxConsecutiveLosses; n != nil && (*n < 1 || *n > 20) {
		return invalid("max_consecutive_losses", "Losses in a row must be between 1 and 20")
	}
	if n := in.Limits.MaxContracts; n != nil && (*n < 1 || *n > 500) {
		return invalid("max_contracts", "Contracts per run must be between 1 and 500")
	}
	if n := in.Limits.MaxDurationSeconds; n != nil && (*n < 60 || *n > 7200) {
		return invalid("max_duration_s", "A run can last between 1 minute and 2 hours")
	}
	return nil
}

// StopRun asks a run to finish. It is idempotent: stopping a stopped run returns
// the row rather than an error, because the caller's intent is already satisfied.
//
// A run with a contract in flight goes to 'stopping' rather than straight to
// stopped, so it keeps the account's live-run slot until that contract settles
// and its result is counted.
func (s *BotService) StopRun(ctx context.Context, userID, runID uuid.UUID, reason string) (*models.BotRun, error) {
	run, owner, err := s.runAndOwner(ctx, runID)
	if err != nil {
		return nil, err
	}
	if owner != userID {
		return nil, ErrForbidden
	}
	return s.stopRun(ctx, run, owner, reason)
}

// runAndOwner reads a run and whose it is, which every stop path needs: the
// ownership check for a user's own stop, and the broadcast topic for both.
func (s *BotService) runAndOwner(ctx context.Context, runID uuid.UUID) (*models.BotRun, uuid.UUID, error) {
	run, err := s.store.BotRuns.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, uuid.Nil, ErrNotFound
		}
		return nil, uuid.Nil, err
	}
	account, err := s.store.Accounts.GetByID(ctx, run.AccountID)
	if err != nil {
		return nil, uuid.Nil, err
	}
	return run, account.UserID, nil
}

// SignalStop tells a run's goroutine to stop and republishes the row, without
// deciding anything about the row itself.
//
// This is for a caller that has already written the ending in its own
// transaction — the console's per-run stop, which commits the status change and
// its audit row together the way the kill switch does. It needs two things
// afterwards: the loop to notice now rather than on its next check, and the user
// to be told. Neither belongs in the caller's transaction.
func (s *BotService) SignalStop(ctx context.Context, runID uuid.UUID, reason string) {
	s.mu.Lock()
	live := s.runs[runID]
	s.mu.Unlock()
	if live != nil {
		live.requestStop(reason)
	}

	s.announce(ctx, runID)
}

// stopRun is the decision, shared by every path that stops one run.
func (s *BotService) stopRun(ctx context.Context, run *models.BotRun, owner uuid.UUID, reason string) (*models.BotRun, error) {
	runID := run.ID
	if run.Status == models.BotStopped {
		return run, nil
	}
	if run.Status == models.BotStopping {
		// Already waiting on the in-flight contract; make sure the loop knows,
		// then return the row as it is.
		s.mu.Lock()
		live := s.runs[runID]
		s.mu.Unlock()
		if live != nil {
			live.requestStop(reason)
		}
		return run, nil
	}

	s.mu.Lock()
	live := s.runs[runID]
	s.mu.Unlock()
	if live != nil {
		live.requestStop(reason)
	}

	var (
		stopped *models.BotRun
		won     bool
	)
	if err := s.store.WithTx(ctx, func(st *repository.Store) error {
		var err error
		stopped, won, err = applyStop(ctx, st, runID, reason)
		return err
	}); err != nil {
		return nil, err
	}

	// Only whoever wrote the ending announces it. The run's own goroutine will
	// reach finalise on its way out and try to write the same ending; without
	// this both would broadcast and the user would be told twice that a run they
	// stopped once had stopped.
	if won {
		s.broadcast(owner, stopped)
	}
	return stopped, nil
}

// applyStop is the stop decision, made under the run row's lock.
//
// The lock is the whole point, and it is the same one a placement takes inside
// DigitService.Open. Without it the count below cannot see a placement whose
// transaction has not committed, so a stop arriving mid-placement reads zero,
// writes the ending, and lets the contract commit under a run that is already
// stopped — releasing the account's one-live-run slot with a contract still in
// flight. Holding the row makes the two orderings the only two: either the
// placement committed and is counted here, or it has not started and will find
// this ending when it tries.
//
// Returns the row as it now stands, and whether this call was the one that ended
// the run — which is what decides who announces it.
//
// Takes a tx-scoped store rather than doing its own WithTx, so a caller that has
// more to commit atomically — the console's stop, which writes an audit row —
// can put it in the same transaction.
func applyStop(ctx context.Context, st *repository.Store, runID uuid.UUID, reason string) (*models.BotRun, bool, error) {
	if _, err := st.BotRuns.GetForUpdate(ctx, runID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, false, ErrNotFound
		}
		return nil, false, err
	}

	open, err := st.DigitContracts.CountOpenByRun(ctx, runID)
	if err != nil {
		return nil, false, err
	}
	if open > 0 {
		// Persist 'stopping' so a second StartRun cannot slip into the gap, and
		// so the UI can show "Stopping after the current contract…". The run's
		// own goroutine writes the ending when that contract settles.
		if err := st.BotRuns.SetStopping(ctx, runID); err != nil {
			return nil, false, err
		}
	} else if won, err := st.BotRuns.Finalise(ctx, runID, reason, nil); err != nil {
		return nil, false, err
	} else {
		// Idle: nothing in flight, so the ending is written now. Finalise's guard
		// keeps it idempotent, and its answer says whether this call was the one
		// that wrote it.
		row, err := st.BotRuns.Get(ctx, runID)
		return row, won, err
	}

	row, err := st.BotRuns.Get(ctx, runID)
	return row, false, err
}

// Get returns one run, ownership-checked.
func (s *BotService) Get(ctx context.Context, userID, runID uuid.UUID) (*models.BotRun, error) {
	run, err := s.store.BotRuns.Get(ctx, runID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	account, err := s.store.Accounts.GetByID(ctx, run.AccountID)
	if err != nil {
		return nil, err
	}
	if account.UserID != userID {
		return nil, ErrForbidden
	}
	return run, nil
}

// List pages an account's runs, ownership-checked on the account.
func (s *BotService) List(ctx context.Context, userID, accountID uuid.UUID, status string, limit, offset int) ([]models.BotRun, int, error) {
	account, err := s.store.Accounts.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	if account.UserID != userID {
		return nil, 0, ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	return s.store.BotRuns.ListByAccount(ctx, accountID, status, limit, offset)
}

// --- the loop ---------------------------------------------------------------

func (s *BotService) launch(run *models.BotRun, userID uuid.UUID) {
	live := &liveRun{userID: userID, stop: make(chan struct{})}

	s.mu.Lock()
	s.runs[run.ID] = live
	s.mu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// The goroutine owns its own bookkeeping, so a run that ends any way at
		// all — cleanly, by panic, by shutdown — leaves no entry behind.
		defer func() {
			s.mu.Lock()
			delete(s.runs, run.ID)
			s.mu.Unlock()
		}()
		// A panic here would otherwise take down the whole API: middleware.Recovery
		// only wraps HTTP handlers, and nothing supervises a bare service goroutine.
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("bot run panicked", "run", run.ID, "panic", r)
				msg := fmt.Sprintf("panic: %v", r)
				s.finalise(run.ID, live, models.BotStopError, &msg)
			}
		}()
		s.loop(run, live)
	}()
}

func (s *BotService) loop(run *models.BotRun, live *liveRun) {
	runID := run.ID
	coldStarts := 0

	for {
		// The checks that need no database come first. On shutdown the root context
		// is already cancelled, so the read below would fail — and finalising on
		// that error would record a restart as a fault, with "context canceled" as
		// its last_error, which is noise that reads like a bug.
		if reason, stop := s.externalStop(live); stop {
			s.finalise(runID, live, reason, nil)
			return
		}

		// Refuse to place before every contract, not once at the start: the flag
		// can be turned off, the clock runs, and the run's own totals change with
		// each settlement.
		current, err := s.store.BotRuns.Get(s.rootCtx, runID)
		if err != nil {
			s.log.Error("bot run read failed", "run", runID, "err", err)
			msg := err.Error()
			s.finalise(runID, live, models.BotStopError, &msg)
			return
		}

		if reason, stop := s.shouldStop(live, current); stop {
			s.finalise(runID, live, reason, nil)
			return
		}

		// Belt and braces against the one way a sequential loop can double up: an
		// Open whose transaction committed and whose reply was then lost. If a
		// contract of ours is still open, wait for it instead of placing another.
		if open, err := s.store.DigitContracts.CountOpenByRun(s.rootCtx, runID); err == nil && open > 0 {
			s.log.Warn("bot run found an unexpected open contract; awaiting it", "run", runID, "open", open)
			s.awaitRunSettled(runID, live)
			continue
		}

		contract, err := s.place(current, live.userID)
		// Counted here rather than in each branch below, so a branch added later
		// cannot quietly stop being counted. Every one of them is a placement
		// that did not happen, which is what the metric is for.
		if err != nil && s.onPlaceError != nil {
			s.onPlaceError(placementErrorCode(err))
		}
		switch {
		case err == nil:
			// placed

		case errors.Is(err, ErrInsufficientFunds):
			s.finalise(runID, live, models.BotStopInsufficientFunds, nil)
			return
		case errors.Is(err, ErrAccountFrozen):
			s.finalise(runID, live, models.BotStopAccountFrozen, nil)
			return
		case errors.Is(err, ErrMarketClosed):
			s.finalise(runID, live, models.BotStopMarketClosed, nil)
			return
		case errors.Is(err, ErrTooManyOpen):
			// Someone is trading this account by hand at the same time. Not the
			// run's fault and not fatal: wait a beat and try again.
			if !s.sleep(live, s.interval(current)) {
				s.finalise(runID, live, s.stopReasonOf(live), nil)
				return
			}
			continue

		case errors.Is(err, ErrNoPrice):
			// Cold start only. A feed that stalled after delivering a price still
			// has a last price, so Open succeeds and the contract voids instead —
			// consecutive voids are what notice that.
			coldStarts++
			if coldStarts >= botColdStartRetries {
				s.finalise(runID, live, models.BotStopFeedDown, nil)
				return
			}
			if !s.sleep(live, s.interval(current)) {
				s.finalise(runID, live, s.stopReasonOf(live), nil)
				return
			}
			continue

		case errors.Is(err, ErrBotRunNotLive):
			// The run was stopped between this iteration's checks and the lock
			// the placement takes, so no contract was opened. Whoever stopped it
			// already wrote the ending; finalise here loses its guard and stays
			// quiet, which is what should happen — this is not a fault, and
			// recording it as one would put "error" on a run a person stopped.
			s.finalise(runID, live, s.stopReasonOf(live), nil)
			return

		default:
			// Never retried. An unknown error may have committed its contract and
			// lost the reply, and retrying that is how a sequential loop places
			// twice. Stopping is the safe reading of an ambiguous outcome.
			s.log.Error("bot placement failed", "run", runID, "err", err)
			msg := err.Error()
			s.finalise(runID, live, models.BotStopError, &msg)
			return
		}

		coldStarts = 0
		if s.onPlacement != nil {
			s.onPlacement()
		}

		outcome := s.awaitSettlement(contract.ID, live)
		if s.onContract != nil && outcome != "" {
			s.onContract(outcome)
		}

		// Read the row rather than tracking totals here: the settlement
		// transaction wrote them, so this is the only version that agrees with the
		// contracts and the ledger.
		if reason, stop := s.externalStop(live); stop {
			s.finalise(runID, live, reason, nil)
			return
		}
		after, err := s.store.BotRuns.Get(s.rootCtx, runID)
		if err != nil {
			msg := err.Error()
			s.finalise(runID, live, models.BotStopError, &msg)
			return
		}
		s.broadcast(live.userID, after)

		if reason, stop := s.shouldStop(live, after); stop {
			s.finalise(runID, live, reason, nil)
			return
		}
		if !s.sleep(live, s.interval(after)) {
			s.finalise(runID, live, s.stopReasonOf(live), nil)
			return
		}
	}
}

// shouldStop is the whole stop policy in one pure-ish place, so the order of
// precedence is visible: an external stop first, then the flag, then the limits.
func (s *BotService) shouldStop(live *liveRun, run *models.BotRun) (string, bool) {
	if reason, stop := s.externalStop(live); stop {
		return reason, true
	}
	// The row was ended underneath us — by the kill switch's UPDATE, or by a stop
	// this process did not own.
	if run.Status == models.BotStopped {
		reason := models.BotStopAdminKill
		if run.StopReason != nil && *run.StopReason != "" {
			reason = *run.StopReason
		}
		return reason, true
	}
	if run.Status == models.BotStopping {
		return models.BotStopUser, true
	}

	// The global flag, re-read every iteration. The settings cache means this can
	// lag a SQL flip by its TTL; the admin kill endpoint exists for immediacy.
	enabled, err := s.flags.BotEnabledFor(s.rootCtx, live.userID)
	if err == nil && !enabled {
		return models.BotStopDisabled, true
	}

	// Everything that can be decided from the row alone.
	return botLimitReached(run, time.Now())
}

// botLimitReached is the stop policy that depends on nothing but the run's own
// row and the clock: the conditions the user asked for, plus the fault threshold
// that notices a dead feed.
//
// Pure on purpose. It is the part worth testing exhaustively, and it can be,
// without a database, a feed or a goroutine — the surrounding checks in
// shouldStop are about the process and the flags and need none of that rigour.
func botLimitReached(run *models.BotRun, now time.Time) (string, bool) {
	if run.RealisedPnL.LessThanOrEqual(run.Limits.StopLoss.Neg()) {
		return models.BotStopStopLoss, true
	}
	if tp := run.Limits.TakeProfit; tp != nil && run.RealisedPnL.GreaterThanOrEqual(*tp) {
		return models.BotStopTakeProfit, true
	}
	if n := run.Limits.MaxConsecutiveLosses; n != nil && run.ConsecutiveLosses >= *n {
		return models.BotStopMaxConsecutiveLosses, true
	}
	// Counts voids, because a stalled feed would otherwise place without bound:
	// a voided contract costs nothing but still consumed an attempt.
	if n := run.Limits.MaxContracts; n != nil && run.ContractsPlaced >= *n {
		return models.BotStopMaxContracts, true
	}
	// Derived from the row rather than captured when the loop started, so the
	// deadline has one source of truth. An earlier version computed it once at
	// launch and defaulted to StartedAt when unset, which put the deadline in the
	// past and stopped every unlimited run immediately.
	if d := run.Limits.MaxDurationSeconds; d != nil {
		if now.After(run.StartedAt.Add(time.Duration(*d) * time.Second)) {
			return models.BotStopMaxDuration, true
		}
	}
	// A feed that stopped delivering: contracts still price off the stale last
	// tick, open fine, and then void.
	if run.ConsecutiveVoids >= botMaxConsecutiveVoids {
		return models.BotStopFeedDown, true
	}
	return "", false
}

func (s *BotService) place(run *models.BotRun, userID uuid.UUID) (*models.DigitContract, error) {
	// Deliberately hung off the root context, not the run's stop channel: a
	// cancellation landing between commit and return would leave a contract the
	// loop never learns about.
	ctx, cancel := context.WithTimeout(s.rootCtx, botOpenTimeout)
	defer cancel()

	runID := run.ID
	return s.digits.Open(ctx, OpenDigitInput{
		UserID:       userID,
		AccountID:    run.AccountID,
		SymbolCode:   run.SymbolCode,
		ContractType: run.ContractType,
		Selection:    run.Selection,
		Stake:        run.Stake,
		BotRunID:     &runID,
	})
}

// awaitSettlement polls until the contract is no longer open, and reports the
// outcome for the metric hook.
//
// Polling calls DigitService.Get, which runs EnsureSettled — so a poll can settle
// the contract itself, on this goroutine, which takes work off the feed goroutine
// rather than adding to it.
func (s *BotService) awaitSettlement(contractID uuid.UUID, live *liveRun) string {
	deadline := time.Now().Add(DigitVoidGrace + botSettleSlack)
	ticker := time.NewTicker(s.pollEvery)
	defer ticker.Stop()

	for {
		c, err := s.digits.Get(s.rootCtx, live.userID, contractID)
		switch {
		case err == nil && c.Status != models.DigitOpen:
			return c.Status
		case errors.Is(err, ErrDigitNotOpen):
			// Lost the settle race to onTick, the sweep, or another reader. The
			// contract is settled; this is a re-read, not a failure.
		case err != nil:
			s.log.Warn("bot settlement poll failed", "contract", contractID, "err", err)
		}

		if time.Now().After(deadline) {
			// Past the void grace plus slack means settlement itself is wedged,
			// which is worth a loud line: the stake is neither lost nor returned.
			s.log.Error("bot contract did not settle within grace",
				"contract", contractID, "waited", DigitVoidGrace+botSettleSlack)
			return ""
		}

		select {
		case <-ticker.C:
		case <-s.rootCtx.Done():
			// Shutdown. The contract is left open for the next process's boot
			// sweep; its settlement will still fold into this run's totals.
			return ""
		}
		// A stop request does not abandon the wait: the contract is in flight and
		// its result belongs to this run, which is what 'stopping' means.
	}
}

// awaitRunSettled waits out any contract this run has open, used on the
// belt-and-braces path where the loop found one it did not expect.
func (s *BotService) awaitRunSettled(runID uuid.UUID, live *liveRun) {
	deadline := time.Now().Add(DigitVoidGrace + botSettleSlack)
	ticker := time.NewTicker(s.pollEvery)
	defer ticker.Stop()
	for {
		n, err := s.store.DigitContracts.CountOpenByRun(s.rootCtx, runID)
		if err == nil && n == 0 {
			return
		}
		if time.Now().After(deadline) {
			return
		}
		select {
		case <-ticker.C:
		case <-s.rootCtx.Done():
			return
		}
	}
}

// finalise writes the run's ending exactly once and tells the user.
func (s *BotService) finalise(runID uuid.UUID, live *liveRun, reason string, lastError *string) {
	// During shutdown the root context is already cancelled, so a write on it
	// would fail silently and leave the row live forever. onTick has the same
	// problem and solves it the same way.
	ctx := s.rootCtx
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.Background(), botFinaliseTimeout)
		defer cancel()
	}

	won, err := s.store.BotRuns.Finalise(ctx, runID, reason, lastError)
	if err != nil {
		s.log.Error("bot run finalise failed", "run", runID, "reason", reason, "err", err)
		return
	}
	// Someone else — a stop request, the kill switch — already wrote the ending,
	// so the run is stopped but this goroutine is not what stopped it. It still
	// exits; it just does not get to say so, or the stop would be counted twice
	// in bot_runs_stopped_total and announced twice to the user.
	if !won {
		return
	}
	if s.onStop != nil {
		s.onStop(reason)
	}

	run, err := s.store.BotRuns.Get(ctx, runID)
	if err != nil {
		return
	}
	// The row's reason, not the one passed in: Finalise is guarded so whoever
	// stopped the run first owns the reason, and the log should say what is true.
	logged := reason
	if run.StopReason != nil && *run.StopReason != "" {
		logged = *run.StopReason
	}
	s.log.Info("bot run stopped",
		"run", runID, "reason", logged,
		"placed", run.ContractsPlaced, "wins", run.Wins, "losses", run.Losses,
		"voids", run.Voids, "realised", run.RealisedPnL.String())
	s.broadcast(live.userID, run)
}

// stopReasonOf answers "we are stopping, but why" for the paths that only know
// that something interrupted them — chiefly sleep, which returns on either a stop
// request or process shutdown.
//
// The root-context check is not decoration. Without it a shutdown that landed
// while a run was between contracts was recorded as a user stop, which would tell
// an operator reading stop reasons that their users had quit when in fact the
// service had been restarted underneath them.
// externalStop reports a stop that comes from outside the run's own limits: a
// stop request, or the process going away. It touches no database, so it is safe
// to consult when the pool may already be closing.
func (s *BotService) externalStop(live *liveRun) (string, bool) {
	select {
	case <-live.stop:
		return s.stopReasonOf(live), true
	default:
	}
	if s.rootCtx.Err() != nil {
		return models.BotStopShutdown, true
	}
	return "", false
}

func (s *BotService) stopReasonOf(live *liveRun) string {
	if r := live.stopReason(); r != "" {
		return r
	}
	if s.rootCtx.Err() != nil {
		return models.BotStopShutdown
	}
	return models.BotStopUser
}

// sleep waits out the run's interval, returning false if it was asked to stop.
func (s *BotService) sleep(live *liveRun, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-live.stop:
		return false
	case <-s.rootCtx.Done():
		return false
	}
}

func (s *BotService) interval(run *models.BotRun) time.Duration {
	ms := run.IntervalMS
	if ms < s.limits.MinIntervalMS {
		ms = s.limits.MinIntervalMS
	}
	return time.Duration(ms) * time.Millisecond
}

func (s *BotService) broadcast(userID uuid.UUID, run *models.BotRun) {
	if s.hub == nil || userID == uuid.Nil {
		return
	}
	// Broadcast directly rather than through DigitService.notify, which looks the
	// account up on every event; the run already knows whose it is.
	s.hub.Broadcast(UserTopic(userID.String()), "bot_run", run)
}
