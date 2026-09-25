package routes

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/time/rate"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/handlers"
	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/observability"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// Deps is everything the router needs. main builds it; routes only wires it.
type Deps struct {
	Config  *config.Config
	Log     *slog.Logger
	Auth    *service.AuthService
	Admin   *service.AdminService
	Mpesa   *service.MpesaService
	Payouts *service.MpesaPayoutService
	Handler struct {
		Auth    *handlers.AuthHandler
		Account *handlers.AccountHandler
		Wallet  *handlers.WalletHandler
		Trade   *handlers.TradeHandler
		Digit   *handlers.DigitHandler
		Bot     *handlers.BotHandler
		Market  *handlers.MarketHandler
		WS      *handlers.WSHandler
		Admin   *handlers.AdminHandler
	}
}

func New(d Deps) *gin.Engine {
	if d.Config.Env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	r := gin.New()
	r.Use(middleware.Recovery(d.Log), middleware.Logger(d.Log), observability.Middleware())
	r.Use(cors.New(cors.Config{
		AllowOrigins:     d.Config.CORSOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		AllowCredentials: true,
		MaxAge:           12 * time.Hour,
	}))

	// mock_mpesa is here so a test script can refuse to run a deposit against an
	// API wired to real Daraja credentials. A live STK push charges somebody real
	// money, and the scripts that exercise deposits invent their phone numbers —
	// so the only safe default is for them to fail closed when they cannot tell.
	// It discloses nothing: on a real deployment the answer is false, which is
	// what anyone would assume anyway.
	r.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "mock_mpesa": d.Config.UseMockMpesa})
	})

	// Metrics are authenticated: the numbers here include deposit volumes and
	// user counts, which is not something to publish.
	r.GET("/metrics", observability.MetricsHandler(d.Config.MetricsToken,
		gin.WrapH(promhttp.Handler())))

	v1 := r.Group("/api/v1")

	// Auth is rate limited hard: it is the one unauthenticated write surface.
	authRoutes := v1.Group("/auth", middleware.RateLimit(rate.Every(d.Config.AuthRateEvery), d.Config.AuthRateBurst))
	{
		authRoutes.POST("/register", d.Handler.Auth.Register)
		authRoutes.POST("/login", d.Handler.Auth.Login)
		authRoutes.POST("/refresh", d.Handler.Auth.Refresh)
		authRoutes.POST("/logout", d.Handler.Auth.Logout)
	}

	// Daraja posts here with no token of ours; it is verified by looking the
	// CheckoutRequestID up against a request we actually made.
	v1.POST("/mpesa/callback", d.Handler.Wallet.MpesaCallback(d.Mpesa))
	// Payout callbacks. TWO routes, not one: which URL Daraja posted to is the
	// only thing that separates "here is what happened" from "our queue gave up
	// and nobody knows", and the second must never settle or refund anything.
	// Both answer 200 always, like the deposit callback.
	v1.POST("/mpesa/b2c/result", d.Handler.Wallet.B2CResult(d.Payouts))
	v1.POST("/mpesa/b2c/timeout", d.Handler.Wallet.B2CTimeout(d.Payouts))
	// And a third, for status-query answers: a different document from a payout
	// result, whose non-zero codes mean the query failed rather than the payout.
	v1.POST("/mpesa/b2c/status", d.Handler.Wallet.B2CStatusResult(d.Payouts))

	protected := v1.Group("", middleware.Auth(d.Auth), middleware.RateLimit(rate.Limit(20), 60))
	{
		protected.GET("/auth/me", d.Handler.Auth.Me)

		protected.GET("/accounts", d.Handler.Account.List)
		protected.POST("/accounts", d.Handler.Account.Create)
		protected.GET("/accounts/:id/wallet", d.Handler.Account.Wallet)

		protected.GET("/wallet/transactions", d.Handler.Wallet.Transactions)
		protected.GET("/wallet/transactions/:id", d.Handler.Wallet.Transaction)
		protected.GET("/wallet/rate", d.Handler.Wallet.Rate)

		// Money movement gets its own tighter bucket on top of the group's.
		money := protected.Group("", middleware.RateLimit(rate.Every(2*time.Second), 5))
		money.POST("/wallet/deposit", d.Handler.Wallet.Deposit)
		money.POST("/wallet/withdraw", d.Handler.Wallet.Withdraw)

		protected.GET("/symbols", d.Handler.Market.Symbols)
		protected.GET("/symbols/:code/ticks", d.Handler.Market.Ticks)
		protected.GET("/signals", d.Handler.Market.Signals)

		// Opening a position moves money, so it gets the same treatment as a
		// deposit: its own bucket rather than the group's loose one, which reads
		// and price polling also draw on. Closing is deliberately not here — a
		// user getting out of a position should never meet a limiter.
		//
		// A digit contract settles on the next tick, so nothing legitimate places
		// faster than about twice a second; the burst covers a few taps in a row.
		placement := protected.Group("", middleware.RateLimit(rate.Every(500*time.Millisecond), 5))
		placement.POST("/trades", d.Handler.Trade.Open)
		placement.POST("/digit-contracts", d.Handler.Digit.Open)
		// Starting a run commits to placing repeatedly, and stopping one is the
		// way out of that, so both sit with the other money writes. Reads about
		// runs stay in the group's loose bucket below, alongside the quote: a
		// client polling a live run must never be throttled into blindness.
		placement.POST("/bot/runs", d.Handler.Bot.Start)
		placement.POST("/bot/runs/:id/stop", d.Handler.Bot.Stop)

		protected.GET("/trades", d.Handler.Trade.List)
		protected.GET("/trades/summary", d.Handler.Trade.Summary)
		protected.POST("/trades/:id/close", d.Handler.Trade.Close)

		protected.GET("/digit-contracts", d.Handler.Digit.List)
		protected.GET("/digit-contracts/summary", d.Handler.Digit.Summary)
		// Deliberately in the loose group, not the placement bucket: a quote is a
		// read, the ticket asks for two of them on every keystroke of the stake,
		// and throttling it would blank the payout a user is reading.
		protected.GET("/digit-contracts/quote", d.Handler.Digit.Quote)
		protected.GET("/digit-contracts/:id", d.Handler.Digit.Get)

		protected.GET("/bot/runs", d.Handler.Bot.List)
		protected.GET("/bot/runs/:id", d.Handler.Bot.Get)
	}

	// Admin console. A separate login surface, a separate token scope, and a
	// separate refresh cookie path, so the two sessions never see each other.
	adminAuth := v1.Group("/admin", middleware.RateLimit(rate.Every(d.Config.AuthRateEvery*4), d.Config.AuthRateBurst/2))
	{
		adminAuth.POST("/login", d.Handler.Admin.Login)
		adminAuth.POST("/refresh", d.Handler.Admin.Refresh)
		adminAuth.POST("/logout", d.Handler.Admin.Logout)
	}

	adminSession := v1.Group("/admin", middleware.AdminAuth(d.Admin))
	{
		// Available to a restricted session, because it is the way out of one.
		adminSession.GET("/me", d.Handler.Admin.Me)
		adminSession.POST("/change-password", d.Handler.Admin.ChangePassword)
	}

	admin := v1.Group("/admin",
		middleware.AdminAuth(d.Admin),
		middleware.RequireFullAdmin(),
		middleware.RateLimit(rate.Limit(20), 60))
	{
		admin.GET("/dashboard", d.Handler.Admin.Dashboard)
		admin.GET("/users", d.Handler.Admin.Users)
		admin.GET("/users/:id", d.Handler.Admin.UserDetail)
		admin.GET("/transactions", d.Handler.Admin.Transactions)
		admin.GET("/kyc", d.Handler.Admin.KYCQueue)
		admin.GET("/audit-log", d.Handler.Admin.AuditLog)
		admin.GET("/bot/runs", d.Handler.Admin.BotRuns)

		// Money and state changes get their own tighter bucket.
		actions := admin.Group("", middleware.RateLimit(rate.Every(time.Second), 10))
		actions.POST("/accounts/:id/freeze", d.Handler.Admin.Freeze)
		actions.POST("/accounts/:id/unfreeze", d.Handler.Admin.Unfreeze)
		actions.POST("/accounts/:id/adjust", d.Handler.Admin.Adjust)
		actions.POST("/kyc/:id/approve", d.Handler.Admin.ApproveKYC)
		actions.POST("/kyc/:id/reject", d.Handler.Admin.RejectKYC)
		// Settles a stuck deposit's original row, so a late callback credits
		// nothing. With the money actions because it is one.
		actions.POST("/deposits/:id/resolve", d.Handler.Admin.ResolveDeposit)
		// The kill switch belongs with the money actions: it stops every live run
		// and turns the feature off, and it is audited like the rest.
		actions.POST("/bot/kill", d.Handler.Admin.KillBots)
		// Symmetric to the kill switch and audited the same way, because an
		// outage the log never ends is not a log of the outage.
		actions.POST("/bot/enable", d.Handler.Admin.EnableBots)
		// Stopping one run reaches into one user's account, which is what every
		// other route in this group does.
		actions.POST("/bot/runs/:id/stop", d.Handler.Admin.StopBotRun)
	}

	// WebSockets authenticate with ?token= because a browser cannot set
	// headers on the handshake.
	sockets := v1.Group("/ws", middleware.Auth(d.Auth))
	{
		sockets.GET("/prices", d.Handler.WS.Prices)
		sockets.GET("/signals", d.Handler.WS.Signals)
		sockets.GET("/account", d.Handler.WS.Account)
	}

	return r
}
