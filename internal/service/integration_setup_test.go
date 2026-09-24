//go:build integration

// Integration tests run against a real PostgreSQL instance in a throwaway
// container. They exist to exercise the row locks in the money path under
// actual concurrency, which unit tests with a fake store cannot do.
//
//	go test -tags integration ./internal/service/...
package service_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/derivclient"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// harness is everything a test needs: a live database, a store, and the
// services wired the same way main.go wires them.
type harness struct {
	t        *testing.T
	ctx      context.Context
	store    *repository.Store
	cfg      *config.Config
	feed     *service.FeedService
	trading  *service.TradingService
	digits   *service.DigitService
	wallet   *service.WalletService
	admin    *service.AdminService
	mpesa    *service.MpesaService
	bots     *service.BotService
	settings *service.SettingsService
	events   *captureBroadcaster
	log      *slog.Logger
}

// container is started once for the whole package; each test gets clean data
// rather than a clean container, because starting Postgres per test would
// dominate the runtime.
var sharedDSN string

func TestMain(m *testing.M) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx,
		"postgres:16-alpine",
		tcpostgres.WithDatabase("tdp_test"),
		tcpostgres.WithUsername("tdp"),
		tcpostgres.WithPassword("tdp"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second)),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not start postgres: %v\n", err)
		os.Exit(1)
	}

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "could not get dsn: %v\n", err)
		os.Exit(1)
	}
	sharedDSN = dsn

	code := m.Run()

	if err := pg.Terminate(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "could not terminate postgres: %v\n", err)
	}
	os.Exit(code)
}

// newHarness gives the test a fresh schema and wired services.
func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	pool, err := repository.Connect(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	// Drop and re-apply so each test starts from a known schema and no rows.
	if _, err := pool.Exec(ctx, `
		DROP SCHEMA public CASCADE;
		CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}

	root := repoRoot(t)
	if err := repository.Migrate(ctx, pool, filepath.Join(root, "migrations")); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	cfg := &config.Config{
		Env:                 "test",
		JWTSecret:           "test-secret-not-used-for-anything-real",
		AccessTokenTTL:      15 * time.Minute,
		RefreshTokenTTL:     time.Hour,
		KESPerUSD:           129.50,
		MinDepositKES:       50,
		MaxDepositKES:       150000,
		DemoStartingBalance: 10000,
		UseMockMpesa:        true,
	}

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	store := repository.NewStore(pool)

	// A feed with no upstream: tests set prices explicitly so a position's
	// outcome is deterministic rather than at the mercy of a random walk.
	feed := service.NewFeedService(&staticFeed{}, noopBroadcaster{}, log)
	symbols, err := store.Symbols.List(ctx, true)
	if err != nil {
		t.Fatalf("list symbols: %v", err)
	}
	feed.SetPrecision(symbols)

	mpesa := service.NewMpesaService(store, cfg, log)
	wallet := service.NewWalletService(store, mpesa, noopBroadcaster{}, cfg, log)
	trading := service.NewTradingService(store, feed, noopBroadcaster{}, log)
	// The production defaults, stated rather than inherited: a test that needs
	// different limits constructs its own service with its own DigitLimits.
	digits := service.NewDigitService(store, feed, noopBroadcaster{}, service.DigitLimits{
		MaxStake:          decimal.NewFromInt(100),
		MaxOpenPerAccount: 5,
	}, log)
	admin := service.NewAdminService(store, noopBroadcaster{}, cfg, log)

	// Bot flags default on for tests, so a test that is about the loop does not
	// have to turn the feature on first. Tests about the gates set them
	// explicitly through store.Settings.
	settings := service.NewSettingsService(store, service.SettingsDefaults{
		DigitContractsEnabled: true,
		BotEnabled:            true,
		BotRealMoneyEnabled:   true,
	}, log)
	if err := settings.Bootstrap(ctx); err != nil {
		t.Fatalf("settings bootstrap: %v", err)
	}

	events := &captureBroadcaster{}
	bots := service.NewBotService(store, digits, feed, settings, events, service.BotLimits{
		MinIntervalMS: 1, // real time is not what these tests are about
		MaxRuns:       50,
	}, log)
	// The kill switch's in-process half, wired as main.go wires it, so a test of
	// the switch exercises the immediate stop rather than only the row update.
	admin.SetBotStopper(bots)
	admin.SetSettingsCache(settings)
	// Poll fast: settlement in a test is driven by an injected tick, not by a
	// clock, so the only thing a real poll interval would add is waiting.
	bots.SetPollInterval(5 * time.Millisecond)

	// Same wiring main.go does for the kill switch: without these the rows still
	// stop, but the cache and the goroutines would lag.
	admin.SetBotStopper(bots)
	admin.SetSettingsCache(settings)

	return &harness{
		t: t, ctx: ctx, store: store, cfg: cfg,
		feed: feed, trading: trading, digits: digits, wallet: wallet, admin: admin, mpesa: mpesa,
		bots: bots, settings: settings, events: events, log: log,
	}
}

// repoRoot walks up from the test's working directory to find the module root,
// so migrations are found regardless of where `go test` is invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("could not locate module root")
	return ""
}

// --- fixtures --------------------------------------------------------------

// newUser creates a user with a demo account funded with `bonus`, and returns
// the ids the tests need.
func (h *harness) newUser(bonus decimal.Decimal) (userID, accountID uuid.UUID) {
	h.t.Helper()

	user := &models.User{
		PhoneNumber:  fmt.Sprintf("2547%08d", time.Now().UnixNano()%100000000),
		PasswordHash: "x",
	}
	if err := h.store.Users.Create(h.ctx, user); err != nil {
		h.t.Fatalf("create user: %v", err)
	}

	account := &models.Account{UserID: user.ID, AccountType: models.AccountReal, Currency: "USD"}
	if err := h.store.Accounts.Create(h.ctx, account); err != nil {
		h.t.Fatalf("create account: %v", err)
	}
	if err := h.store.Wallets.Create(h.ctx, account.ID, decimal.Zero, bonus); err != nil {
		h.t.Fatalf("create wallet: %v", err)
	}

	ref := "test-seed"
	if err := h.store.Transactions.Insert(h.ctx, &models.Transaction{
		AccountID:   account.ID,
		Type:        models.TxBonusCredit,
		BalanceType: models.BalanceBonus,
		Amount:      bonus,
		Status:      models.StatusCompleted,
		Reference:   &ref,
	}); err != nil {
		h.t.Fatalf("seed ledger: %v", err)
	}
	return user.ID, account.ID
}

// newAdmin creates an admin to attribute audited actions to.
func (h *harness) newAdmin() uuid.UUID {
	h.t.Helper()
	admin := &models.AdminUser{
		Username:     fmt.Sprintf("admin%d", time.Now().UnixNano()),
		PasswordHash: "x",
		Role:         models.RoleAdmin,
	}
	if err := h.store.Admins.Create(h.ctx, admin); err != nil {
		h.t.Fatalf("create admin: %v", err)
	}
	return admin.ID
}

// newDemoAccount adds a demo account to an existing user.
func (h *harness) newDemoAccount(userID uuid.UUID) uuid.UUID {
	h.t.Helper()
	account, err := h.wallet.CreateAccount(h.ctx, userID, models.AccountDemo)
	if err != nil {
		h.t.Fatalf("create demo account: %v", err)
	}
	return account.ID
}

// asValidation unwraps a service validation error, which carries the field
// name the UI puts the message under.
func asValidation(err error, target **service.ValidationError) bool {
	return errors.As(err, target)
}

// newUserToken mints a user-scope access token, for asserting that a user
// token cannot open an admin route.
func (h *harness) newUserToken() (string, error) {
	auth := service.NewAuthService(h.store, h.cfg)
	result, err := auth.Register(h.ctx, service.RegisterInput{
		PhoneNumber: fmt.Sprintf("07%08d", time.Now().UnixNano()%100000000),
		Password:    "hunter2024",
	})
	if err != nil {
		return "", err
	}
	return result.Tokens.AccessToken, nil
}

// pendingDeposit starts a deposit and returns its ledger id and the Daraja
// checkout id, so a test can drive the callback itself.
func (h *harness) pendingDeposit(userID, accountID uuid.UUID, kes int64) (uuid.UUID, string) {
	h.t.Helper()

	resp, err := h.wallet.Deposit(h.ctx, service.DepositInput{
		UserID: userID, AccountID: accountID,
		AmountKES: decimal.NewFromInt(kes), PhoneNumber: "0712345678",
	})
	if err != nil {
		h.t.Fatalf("start deposit: %v", err)
	}
	return resp.TransactionID, resp.CheckoutRequestID
}

// darajaCallback builds the payload Safaricom would POST for an outcome.
func darajaCallback(checkoutID string, code int, desc, receipt string) mpesaclient.Callback {
	var cb mpesaclient.Callback
	cb.Body.STKCallback.CheckoutRequestID = checkoutID
	cb.Body.STKCallback.ResultCode = code
	cb.Body.STKCallback.ResultDesc = desc
	if receipt != "" {
		cb.Body.STKCallback.CallbackMetadata.Item = []struct {
			Name  string `json:"Name"`
			Value any    `json:"Value"`
		}{{Name: "MpesaReceiptNumber", Value: receipt}}
	}
	return cb
}

// setPrice pins a symbol's price so trade outcomes are deterministic.
func (h *harness) setPrice(code string, price float64) {
	h.t.Helper()
	h.feed.InjectTick(code, decimal.NewFromFloat(price))
}

func (h *harness) wallet_(accountID uuid.UUID) *models.Wallet {
	h.t.Helper()
	w, err := h.store.Wallets.Get(h.ctx, accountID)
	if err != nil {
		h.t.Fatalf("read wallet: %v", err)
	}
	return w
}

// assertReconciled checks this account's ledger against its cached balance,
// using the same predicate the dashboard and the drift gauge use. Every money
// test ends with this: it is the invariant the whole design rests on.
func (h *harness) assertReconciled(accountID uuid.UUID) {
	h.t.Helper()

	ledger, err := h.store.Transactions.LedgerTotal(h.ctx, accountID)
	if err != nil {
		h.t.Fatalf("ledger total: %v", err)
	}

	w := h.wallet_(accountID)
	cached := w.RealBalance.Add(w.BonusBalance)

	if !ledger.Equal(cached) {
		h.t.Fatalf("ledger %s disagrees with wallet cache %s", ledger, cached)
	}

	// And the global view must agree that nothing has drifted.
	drifted, total, err := h.store.Transactions.Drift(h.ctx, 10)
	if err != nil {
		h.t.Fatalf("drift query: %v", err)
	}
	if total != 0 {
		h.t.Fatalf("%d account(s) drifted: %+v", total, drifted)
	}
}

// --- test doubles ----------------------------------------------------------

type noopBroadcaster struct{}

func (noopBroadcaster) Broadcast(string, string, any) {}

// captureBroadcaster records what would have reached a client, so a test can
// assert that an event was published without standing up a WebSocket.
type captureBroadcaster struct {
	mu   sync.Mutex
	sent []captured
}

type captured struct {
	Topic   string
	Type    string
	Payload any
}

func (b *captureBroadcaster) Broadcast(topic, msgType string, payload any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.sent = append(b.sent, captured{Topic: topic, Type: msgType, Payload: payload})
}

// ofType returns every captured event of one type.
func (b *captureBroadcaster) ofType(msgType string) []captured {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []captured{}
	for _, e := range b.sent {
		if e.Type == msgType {
			out = append(out, e)
		}
	}
	return out
}

// staticFeed satisfies derivclient.Feed without producing any ticks, so tests
// drive prices explicitly through FeedService.InjectTick and a position's
// outcome is a fact about the test rather than about a random walk.
type staticFeed struct{ ch chan derivclient.Tick }

func (f *staticFeed) Run(ctx context.Context) { <-ctx.Done() }

func (f *staticFeed) Ticks() <-chan derivclient.Tick {
	if f.ch == nil {
		f.ch = make(chan derivclient.Tick)
	}
	return f.ch
}
