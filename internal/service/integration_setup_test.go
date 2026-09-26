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
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/internal/storage"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/derivclient"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// harness is everything a test needs: a live database, a store, and the
// services wired the same way main.go wires them.
type harness struct {
	t       *testing.T
	ctx     context.Context
	pool    *pgxpool.Pool
	store   *repository.Store
	cfg     *config.Config
	feed    *service.FeedService
	trading *service.TradingService
	digits  *service.DigitService
	wallet  *service.WalletService
	admin   *service.AdminService
	mpesa   *service.MpesaService
	payouts *service.MpesaPayoutService

	// payoutSender records what was sent and answers however a test scripts it.
	payoutSender  *stubPayoutSender
	payoutQuerier *stubPayoutQuerier
	bots          *service.BotService
	settings      *service.SettingsService

	// kyc is the verification service, wired against an in-memory object store so
	// the whole upload flow is exercisable with no bucket and no credentials.
	kyc    *service.KYCService
	docs   *storage.Stub
	events *captureBroadcaster
	log    *slog.Logger
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
// freshPool connects to the shared container and empties it: each test starts
// from no schema and no rows, and applies whatever migrations it needs.
func freshPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	pool, err := repository.Connect(ctx, sharedDSN)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(ctx, `
		DROP SCHEMA public CASCADE;
		CREATE SCHEMA public;`); err != nil {
		t.Fatalf("reset schema: %v", err)
	}
	return pool
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()

	pool := freshPool(t)
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

		// The production defaults. As with the reconciliation values below, the
		// harness builds Config directly so Load's validation never runs on it —
		// a test that wants a different ceiling sets it itself.
		MaxWithdrawKES:    150000,
		MaxWithdrawDayKES: 300000,
		// Set for the same reason as everything else in this block: left at zero
		// the floor is absent, and every test asserting a small withdrawal is
		// refused would pass whether the guard existed or not.
		MinWithdrawKES: 50,

		// The payout pass's schedule. Omitting these left GiveUp at zero, which
		// made every payout instantly past the horizon — and the give-up test
		// passed anyway, because it could not tell a working horizon from one at
		// zero. The three tests that assert the pass ASKS are what caught it.
		MpesaPayoutQueryEvery: time.Minute,
		MpesaPayoutGiveUp:     24 * time.Hour,

		// The production defaults. Load's 60s floor is not applied here — the
		// harness builds the struct directly — so a test that wants the pass
		// to reach a fresh deposit lowers MinAge itself, deliberately.
		MpesaReconcileEvery:  time.Minute,
		MpesaReconcileMinAge: 3 * time.Minute,
		MpesaReconcileGiveUp: 24 * time.Hour,

		// Document handling. Set here for the same reason as the values above: the
		// harness builds Config directly, so Load's validation and defaults never
		// run. Leaving KYCMaxDocumentBytes at zero would make every upload
		// oversize, and every size test would pass for the wrong reason.
		KYCMaxDocumentBytes:     10 << 20,
		KYCUploadURLExpiry:      10 * time.Minute,
		KYCReviewURLExpiry:      5 * time.Minute,
		KYCAbandonedUploadAfter: 48 * time.Hour,
		KYCRetentionEvery:       time.Hour,
		// Zero: never delete. A retention test sets it explicitly.
		KYCDocumentRetentionDays: 0,
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
	payouts := service.NewMpesaPayoutService(store, cfg, log)
	wallet.SetPayouts(payouts)
	// A sender that accepts and never reports back, replacing mock mode's
	// simulator for tests.
	//
	// Mock mode's own sender delivers a result a few seconds later on a timer,
	// which is right for a dev server and wrong for a test: every assertion about
	// a withdrawal that is still in flight would be racing that timer, and would
	// pass or fail by how fast the machine is. Tests deliver results explicitly
	// through h.deliverPayoutResult instead, so what is under test is the
	// classification and the guards rather than the scheduler.
	sender := &stubPayoutSender{}
	payouts.SetSenderForTest(sender)
	// A querier that records questions and never answers them: the answer arrives
	// on its own channel, which a test delivers explicitly.
	querier := &stubPayoutQuerier{}
	payouts.SetQuerierForTest(querier)
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
		// On in the harness, off in production: the existing withdrawal tests are
		// about the KYC gate, the overdraw guard and the freeze, and the platform
		// flag would short-circuit all three. A test that wants the flag off sets
		// it off itself (TestWithdrawalsAreRefusedWhileTheFeatureIsOff).
		WithdrawalsEnabled: true,
	}, log)
	// Same wiring main.go does: the wallet refuses every withdrawal without it.
	wallet.SetWithdrawalFlags(settings)
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
	admin.SetDepositResolver(mpesa)
	admin.SetPayoutResolver(payouts)
	// Poll fast: settlement in a test is driven by an injected tick, not by a
	// clock, so the only thing a real poll interval would add is waiting.
	bots.SetPollInterval(5 * time.Millisecond)

	// Same wiring main.go does for the kill switch: without these the rows still
	// stop, but the cache and the goroutines would lag.
	admin.SetBotStopper(bots)
	admin.SetSettingsCache(settings)

	docs := storage.NewStub()
	kyc := service.NewKYCService(store, docs, cfg, log)
	// Wired the way main.go wires it: the console asks the KYC service for a
	// document URL rather than reaching into the store itself.
	admin.SetDocuments(kyc)

	return &harness{
		t: t, ctx: ctx, store: store, cfg: cfg,
		pool: pool,
		feed: feed, trading: trading, digits: digits, wallet: wallet, admin: admin, mpesa: mpesa,
		payouts: payouts, payoutSender: sender, payoutQuerier: querier,
		bots: bots, settings: settings, events: events, log: log,
		kyc: kyc, docs: docs,
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

// countWithdrawals and countRows are for asserting that a refused call wrote
// nothing, and that a refund wrote no extra row.
func (h *harness) countWithdrawals(accountID uuid.UUID) int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM transactions WHERE account_id = $1 AND type = 'withdrawal'`,
		accountID).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func (h *harness) countRows(accountID uuid.UUID) int {
	h.t.Helper()
	var n int
	if err := h.pool.QueryRow(h.ctx,
		`SELECT count(*) FROM transactions WHERE account_id = $1`, accountID).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

// pendingWithdrawalID is the account's one in-flight withdrawal. There can only
// be one (migration 0008), which is what makes this unambiguous.
func (h *harness) pendingWithdrawalID(accountID uuid.UUID) uuid.UUID {
	h.t.Helper()
	var id uuid.UUID
	if err := h.pool.QueryRow(h.ctx,
		`SELECT id FROM transactions WHERE account_id = $1 AND type = 'withdrawal' AND status = 'pending'`,
		accountID).Scan(&id); err != nil {
		h.t.Fatalf("read the pending withdrawal: %v", err)
	}
	return id
}

// stubPayoutSender is Daraja's payout side, under a test's control. It records
// every request, answers however the test scripts it, and never delivers a
// result on its own.
type stubPayoutSender struct {
	mu   sync.Mutex
	sent []mpesaclient.B2CRequest
	// err, when set, is what SendB2C returns instead of accepting.
	err error
}

func (p *stubPayoutSender) SendB2C(_ context.Context, r mpesaclient.B2CRequest) (*mpesaclient.B2CAccepted, error) {
	p.mu.Lock()
	p.sent = append(p.sent, r)
	err := p.err
	p.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &mpesaclient.B2CAccepted{
		ConversationID:           "AG_TEST_" + r.OriginatorConversationID,
		OriginatorConversationID: r.OriginatorConversationID,
		ResponseDescription:      "Accept the service request successfully.",
	}, nil
}

func (p *stubPayoutSender) fail(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.err = err
}

func (p *stubPayoutSender) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.sent)
}

func (p *stubPayoutSender) last() (mpesaclient.B2CRequest, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.sent) == 0 {
		return mpesaclient.B2CRequest{}, false
	}
	return p.sent[len(p.sent)-1], true
}

// stubPayoutQuerier is the Transaction Status endpoint under a test's control. It
// records what was asked and never answers: the answer is asynchronous in
// reality, and a test delivers it through h.deliverStatusResult.
type stubPayoutQuerier struct {
	mu    sync.Mutex
	asked []string
	err   error
}

func (q *stubPayoutQuerier) QueryTransactionStatus(_ context.Context, r mpesaclient.StatusRequest) (*mpesaclient.StatusAccepted, error) {
	q.mu.Lock()
	q.asked = append(q.asked, r.OriginatorConversationID)
	err := q.err
	q.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &mpesaclient.StatusAccepted{ConversationID: "AG_STATUS", ResponseDescription: "accepted"}, nil
}

func (q *stubPayoutQuerier) fail(err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.err = err
}

func (q *stubPayoutQuerier) countFor(originatorID string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, id := range q.asked {
		if id == originatorID {
			n++
		}
	}
	return n
}

// statusResultBody is the document Daraja posts to the status channel. Note that
// its ResultCode is about the QUERY, and the payout's own state is in the
// TransactionStatus parameter — the distinction the separate channel exists for.
func statusResultBody(originatorID, queryCode, txStatus, receipt string) []byte {
	params := ""
	if txStatus != "" {
		params = `{"Key":"TransactionStatus","Value":"` + txStatus + `"}`
		if receipt != "" {
			params += `,{"Key":"ReceiptNo","Value":"` + receipt + `"}`
		}
	}
	return []byte(`{"Result":{"ResultType":0,"ResultCode":"` + queryCode + `","ResultDesc":"query result",` +
		`"OriginatorConversationID":"` + originatorID + `","ConversationID":"AG_TEST_` + originatorID + `",` +
		`"ResultParameters":{"ResultParameter":[` + params + `]}}}`)
}

// deliverStatusResult posts a status answer the way the status route would.
func (h *harness) deliverStatusResult(originatorID, queryCode, txStatus, receipt string, arrival mpesaclient.PayoutArrival) error {
	h.t.Helper()
	return h.payouts.HandleStatusResult(h.ctx, statusResultBody(originatorID, queryCode, txStatus, receipt), arrival)
}

// payoutResultBody builds the JSON Daraja posts, so a test drives the real
// parser and the real classification rather than a shortcut past them.
func payoutResultBody(originatorID, code, desc, receipt string) []byte {
	params := ""
	if receipt != "" {
		params = `{"Key":"TransactionReceipt","Value":"` + receipt + `"},` +
			`{"Key":"B2CUtilityAccountAvailableFunds","Value":425000.00}`
	}
	return []byte(`{"Result":{"ResultType":0,"ResultCode":"` + code + `","ResultDesc":"` + desc + `",` +
		`"OriginatorConversationID":"` + originatorID + `","ConversationID":"AG_TEST_` + originatorID + `",` +
		`"TransactionID":"` + receipt + `","ResultParameters":{"ResultParameter":[` + params + `]}}}`)
}

// deliverPayoutResult posts a result to the payout service the way a route
// would, with the arrival channel the caller chooses — which is the thing that
// decides whether an outcome may be acted on.
func (h *harness) deliverPayoutResult(originatorID, code, desc, receipt string, arrival mpesaclient.PayoutArrival) error {
	h.t.Helper()
	return h.payouts.HandleResult(h.ctx, payoutResultBody(originatorID, code, desc, receipt), arrival)
}

// payoutFor reads the payout row belonging to a withdrawal.
func (h *harness) payoutFor(txID uuid.UUID) *models.MpesaPayout {
	h.t.Helper()
	p, err := h.store.Payouts.GetByTransactionID(h.ctx, txID)
	if err != nil {
		h.t.Fatalf("read the payout for %s: %v", txID, err)
	}
	return p
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
