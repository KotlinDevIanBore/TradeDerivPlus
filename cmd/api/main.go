// Command api is the TradeDerivPlus HTTP and WebSocket server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/handlers"
	"github.com/KotlinDevIanBore/tradederivplus/internal/observability"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/routes"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/internal/storage"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/derivclient"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/ws"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	level := slog.LevelDebug
	if cfg.Env == "production" {
		level = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	flushSentry, err := observability.Init(cfg, log)
	if err != nil {
		return err
	}
	defer flushSentry()

	// --- Data ---------------------------------------------------------------
	pool, err := repository.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := repository.Migrate(ctx, pool, "migrations"); err != nil {
		return err
	}
	log.Info("database ready")

	store := repository.NewStore(pool)

	// --- Market feed --------------------------------------------------------
	symbols, err := store.Symbols.List(ctx, true)
	if err != nil {
		return err
	}
	codes := make([]string, 0, len(symbols))
	for _, s := range symbols {
		codes = append(codes, s.Code)
	}

	var feedClient derivclient.Feed
	if cfg.UseMockFeed {
		feedClient = derivclient.NewMock(codes, log)
	} else {
		feedClient = derivclient.New(cfg.DerivWSURL, cfg.DerivAppID, codes, log)
	}

	// --- Services -----------------------------------------------------------
	hub := ws.NewHub(log)
	feed := service.NewFeedService(feedClient, hub, log)
	feed.SetPrecision(symbols)
	auth := service.NewAuthService(store, cfg)
	mpesa := service.NewMpesaService(store, cfg, log)
	wallet := service.NewWalletService(store, mpesa, hub, cfg, log)
	payouts := service.NewMpesaPayoutService(store, cfg, log)
	// Both directions at once: the wallet asks payouts to send, and payouts
	// reports outcomes back to the wallet, which is the only thing that moves
	// money. Wiring one without the other would leave a payout able to win its
	// guard with nowhere to send the result.
	wallet.SetPayouts(payouts)
	// A collapsed or half-configured payout setup is a failed boot, not a failed
	// withdrawal. Mock mode has nothing to validate.
	if err := payouts.ValidateConfig(); err != nil {
		return err
	}
	payouts.OnSettled(observability.RecordPayout)
	payouts.OnQueried(observability.RecordPayoutQuery)
	payouts.OnFloat(observability.RecordB2CUtilityFloat)
	trading := service.NewTradingService(store, feed, hub, log)
	digits := service.NewDigitService(store, feed, hub, service.DigitLimits{
		MaxStake:          decimal.NewFromFloat(cfg.DigitMaxStake),
		MaxOpenPerAccount: cfg.DigitMaxOpenPerAccount,
	}, log)
	signals := service.NewSignalService(store, feed, hub, cfg, log)
	admin := service.NewAdminService(store, hub, cfg, log)
	admin.SetConnectionCounter(hub)
	admin.OnAction(observability.RecordAdminAction)
	wallet.OnDepositSettled(observability.RecordDeposit)
	mpesa.OnReconcile(observability.RecordReconcile)

	// Object storage for identity documents. Absent credentials are not a fault:
	// the deployment simply has no bucket, so an in-memory stub keeps seed
	// fixtures, CI and local development working while the upload endpoints answer
	// 503 in production. The same shape as USE_MOCK_MPESA, chosen on configuration
	// being present rather than on a separate boolean, because there is no reason
	// to run a stub against a configured bucket.
	var docs service.DocumentStore
	var documentStub *storage.Stub
	if cfg.R2AccountID != "" {
		r2, err := storage.New(storage.Config{
			AccountID:       cfg.R2AccountID,
			AccessKeyID:     cfg.R2AccessKeyID,
			SecretAccessKey: cfg.R2SecretAccessKey,
			BucketName:      cfg.R2BucketName,
			Endpoint:        cfg.R2Endpoint,
		})
		if err != nil {
			return err
		}
		docs = r2
		log.Info("object storage ready", "bucket", cfg.R2BucketName)
	} else {
		stub := storage.NewStub()
		// Point the stub's presigned URLs at this API's own development upload
		// route, so a browser can complete the real three-step flow with no bucket.
		stub.BaseURL = strings.TrimSuffix(cfg.PublicAPIURL, "/") + storage.StubUploadPath
		docs = stub
		documentStub = stub
		log.Warn("object storage not configured: KYC documents use an in-memory stub and do not persist",
			"upload_route", stub.BaseURL)
	}
	kyc := service.NewKYCService(store, docs, cfg, log)
	// The console asks the KYC service for a document URL rather than reaching into
	// the object store itself, so one place decides how documents are exposed.
	admin.SetDocuments(kyc)

	settings := service.NewSettingsService(store, service.SettingsDefaults{
		DigitContractsEnabled: cfg.DigitContractsEnabled,
		BotEnabled:            cfg.BotEnabled,
		BotAllowlist:          cfg.BotAllowlist,
		BotRealMoneyEnabled:   cfg.BotRealMoneyEnabled,
		WithdrawalsEnabled:    cfg.WithdrawalsEnabled,
	}, log)
	if err := settings.Bootstrap(ctx); err != nil {
		return err
	}

	bots := service.NewBotService(store, digits, feed, settings, hub, service.BotLimits{
		MinIntervalMS: cfg.BotMinIntervalMS,
		MaxRuns:       cfg.BotMaxRuns,
	}, log)
	bots.OnContract(observability.RecordBotContract)
	bots.OnPlacement(observability.RecordBotPlacement)
	bots.OnPlacementError(observability.RecordBotPlacementError)
	bots.OnStop(observability.RecordBotRunStopped)
	// The kill switch's in-process half: without these the rows still update and
	// every loop stops on its next check, with these it is immediate.
	admin.SetBotStopper(bots)
	admin.SetSettingsCache(settings)
	// The console settles a stuck deposit through the M-Pesa service, never on
	// its own: same guards, same lock order as a callback.
	admin.SetDepositResolver(mpesa)
	admin.SetPayoutResolver(payouts)
	// Withdrawals read their flag through the settings cache. Without this the
	// wallet refuses every withdrawal, which is the safe direction.
	wallet.SetWithdrawalFlags(settings)

	go feed.Run(ctx)
	trading.Start(ctx)
	digits.Start(ctx)
	// The reconciliation pass. After NewWalletService above, which registered
	// the settlement handler: a pass with nowhere to send a result refuses to
	// settle rather than consume the guard, so the order is what makes the boot
	// pass able to credit anything.
	mpesa.Start(ctx)
	// The payout pass. After wallet.SetPayouts above, which registered the
	// settlement handler: a pass that settled with nowhere to send the result
	// would consume the guard and leave the ledger pending for ever.
	payouts.Start(ctx)

	// Two jobs on one ticker: abandoned uploads always, reviewed documents only
	// once a retention period has been set. See section 1 of
	// docs/kyc-r2-upload-plan.md -- that period is a compliance decision, and zero
	// (the default) means never delete.
	service.NewKYCRetentionService(kyc).Start(ctx)
	// After digits.Start, whose boot sweep has already settled or voided what it
	// could: runs left live by the previous process are ended here rather than
	// resumed, and a contract still open folds into its (now stopped) run through
	// the settlement transaction.
	if err := bots.Start(ctx); err != nil {
		return err
	}
	go signals.Run(ctx)

	// Refresh the gauges, including the ledger drift number that says whether
	// any balance has moved without a matching ledger row.
	go observability.Collect(ctx, observability.NewStoreSampler(store, hub.ClientCount, cfg.MpesaReconcileMinAge), log, time.Minute)

	// --- HTTP ---------------------------------------------------------------
	deps := routes.Deps{Config: cfg, Log: log, Auth: auth, Admin: admin, Mpesa: mpesa, Payouts: payouts,
		DocumentStub: documentStub}
	deps.Handler.Auth = handlers.NewAuthHandler(auth, settings, cfg)
	deps.Handler.Account = handlers.NewAccountHandler(wallet)
	deps.Handler.Wallet = handlers.NewWalletHandler(wallet)
	deps.Handler.Trade = handlers.NewTradeHandler(trading)
	deps.Handler.Digit = handlers.NewDigitHandler(digits)
	deps.Handler.Bot = handlers.NewBotHandler(bots)
	deps.Handler.Market = handlers.NewMarketHandler(store, feed, signals)
	deps.Handler.WS = handlers.NewWSHandler(hub, feed, wallet, cfg.CORSOrigins, log)
	deps.Handler.Admin = handlers.NewAdminHandler(admin, cfg)
	deps.Handler.KYC = handlers.NewKYCHandler(kyc)

	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           routes.New(deps),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout: it would cut long-lived WebSocket connections.
		IdleTimeout: 120 * time.Second,
	}

	go func() {
		log.Info("api listening",
			"port", cfg.Port, "env", cfg.Env,
			"mock_feed", cfg.UseMockFeed, "mock_mpesa", cfg.UseMockMpesa,
			"mpesa_reconcile_every", cfg.MpesaReconcileEvery,
			"mpesa_reconcile_min_age", cfg.MpesaReconcileMinAge,
			"mpesa_reconcile_give_up", cfg.MpesaReconcileGiveUp,
			"payouts_configured", cfg.MpesaInitiatorName != "" || cfg.UseMockMpesa)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Bot runs are the one thing here that holds durable state of its own, so it
	// is drained before the pool closes: a run that is not finalised leaves a row
	// saying it is live, which would block that account's next run until a
	// restart cleared it. Each loop writes its ending on a detached context,
	// because the root one is already cancelled by now.
	bots.Shutdown(shutdownCtx)

	return srv.Shutdown(shutdownCtx)
}
