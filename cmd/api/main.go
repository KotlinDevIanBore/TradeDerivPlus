// Command api is the TradeDerivPlus HTTP and WebSocket server.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/handlers"
	"github.com/KotlinDevIanBore/tradederivplus/internal/observability"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/routes"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
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
	trading := service.NewTradingService(store, feed, hub, log)
	signals := service.NewSignalService(store, feed, hub, cfg, log)
	admin := service.NewAdminService(store, hub, cfg, log)
	admin.SetConnectionCounter(hub)
	admin.OnAction(observability.RecordAdminAction)
	wallet.OnDepositSettled(observability.RecordDeposit)

	go feed.Run(ctx)
	trading.Start(ctx)
	go signals.Run(ctx)

	// Refresh the gauges, including the ledger drift number that says whether
	// any balance has moved without a matching ledger row.
	go observability.Collect(ctx, observability.NewStoreSampler(store, hub.ClientCount), log, time.Minute)

	// --- HTTP ---------------------------------------------------------------
	deps := routes.Deps{Config: cfg, Log: log, Auth: auth, Admin: admin, Mpesa: mpesa}
	deps.Handler.Auth = handlers.NewAuthHandler(auth, cfg)
	deps.Handler.Account = handlers.NewAccountHandler(wallet)
	deps.Handler.Wallet = handlers.NewWalletHandler(wallet)
	deps.Handler.Trade = handlers.NewTradeHandler(trading)
	deps.Handler.Market = handlers.NewMarketHandler(store, feed, signals)
	deps.Handler.WS = handlers.NewWSHandler(hub, feed, wallet, cfg.CORSOrigins, log)
	deps.Handler.Admin = handlers.NewAdminHandler(admin, cfg)

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
			"mock_feed", cfg.UseMockFeed, "mock_mpesa", cfg.UseMockMpesa)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("listen failed", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
