package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config is loaded once at boot. Nothing else in the app reads os.Getenv.
type Config struct {
	Env  string
	Port string

	DatabaseURL string

	JWTSecret       string
	AccessTokenTTL  time.Duration
	RefreshTokenTTL time.Duration

	// Deriv market feed
	DerivWSURL  string
	DerivAppID  string
	UseMockFeed bool

	// DigitContractsEnabled seeds app_settings.digit_contracts_enabled when
	// the row is missing. Default true when DIGIT_CONTRACTS_ENABLED is unset.
	// Explicit false/true from env seeds that value; an existing DB row is
	// never overwritten on startup (see SettingsService).
	DigitContractsEnabled bool

	// Bounds on one digit contract and one account, enforced server-side in
	// DigitService.Open. Placing a contract moves money, so these are the
	// equivalent of the deposit and withdrawal limits below: the client's own
	// checks are advice, and anything driving the API directly ignores them.
	DigitMaxStake          float64
	DigitMaxOpenPerAccount int

	// Automated trading. These seed app_settings once and are then owned by the
	// database, so a flag flipped in production survives a deploy. Defaults are
	// deliberately off: the payout table is still a placeholder, so a bot must be
	// switched on by a person rather than arrive enabled.
	BotEnabled          bool
	BotAllowlist        string
	BotRealMoneyEnabled bool

	// Bounds on every run, enforced in BotService. The bot calls the service
	// directly and so never meets the HTTP rate limiter; these take its place.
	BotMinIntervalMS int
	BotMaxRuns       int

	// Daraja / M-Pesa
	MpesaBaseURL        string
	MpesaConsumerKey    string
	MpesaConsumerSecret string
	MpesaShortcode      string
	MpesaPasskey        string
	MpesaCallbackURL    string
	UseMockMpesa        bool

	// Money
	// Deposits arrive in KES, wallets are denominated in USD (gap G8).
	KESPerUSD           float64
	MinDepositKES       float64
	MaxDepositKES       float64
	DemoStartingBalance float64

	// Signal bot
	SignalInterval time.Duration
	SignalSymbols  []string

	CORSOrigins []string

	// Rate limits. The defaults are the production ones and are deliberately
	// tight on the unauthenticated auth surface. They are configurable only so
	// a test suite that registers dozens of users in a row does not spend its
	// time fighting the limiter; the limiter itself is still exercised by its
	// own check in scripts/smoke.py.
	AuthRateEvery time.Duration
	AuthRateBurst int

	// Refresh tokens are delivered as httpOnly cookies rather than in the
	// JSON body, so a long-lived credential is never reachable from JS.
	// CookieDomain is left empty for host-only cookies in development; in
	// production set it to the registrable domain shared by the web app and
	// the API (see the SameSite note in docs/round-2-plan.md).
	CookieDomain string
	CookieSecure bool

	// Observability
	SentryDSN         string
	SentryEnvironment string
	SentrySampleRate  float64
	MetricsToken      string
}

func Load() (*Config, error) {
	_ = godotenv.Load()

	c := &Config{
		Env:  getEnv("APP_ENV", "development"),
		Port: getEnv("PORT", "8080"),

		DatabaseURL: getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/tradederivplus?sslmode=disable"),

		JWTSecret:       getEnv("JWT_SECRET", ""),
		AccessTokenTTL:  getDuration("ACCESS_TOKEN_TTL", 15*time.Minute),
		RefreshTokenTTL: getDuration("REFRESH_TOKEN_TTL", 720*time.Hour),

		DerivWSURL:  getEnv("DERIV_WS_URL", "wss://ws.derivws.com/websockets/v3"),
		DerivAppID:  getEnv("DERIV_APP_ID", "1089"),
		UseMockFeed: getBool("USE_MOCK_FEED", true),

		DigitContractsEnabled: getBool("DIGIT_CONTRACTS_ENABLED", true),

		DigitMaxStake:          getFloat("DIGIT_MAX_STAKE", 100),
		DigitMaxOpenPerAccount: int(getFloat("DIGIT_MAX_OPEN_PER_ACCOUNT", 5)),

		BotEnabled:          getBool("BOT_ENABLED", false),
		BotAllowlist:        getEnv("BOT_ALLOWLIST", ""),
		BotRealMoneyEnabled: getBool("BOT_REAL_MONEY_ENABLED", false),
		BotMinIntervalMS:    int(getFloat("BOT_MIN_INTERVAL_MS", 1000)),
		BotMaxRuns:          int(getFloat("BOT_MAX_RUNS", 50)),

		MpesaBaseURL:        getEnv("MPESA_BASE_URL", "https://sandbox.safaricom.co.ke"),
		MpesaConsumerKey:    getEnv("MPESA_CONSUMER_KEY", ""),
		MpesaConsumerSecret: getEnv("MPESA_CONSUMER_SECRET", ""),
		MpesaShortcode:      getEnv("MPESA_SHORTCODE", "174379"),
		MpesaPasskey:        getEnv("MPESA_PASSKEY", ""),
		MpesaCallbackURL:    getEnv("MPESA_CALLBACK_URL", "http://localhost:8080/api/v1/mpesa/callback"),
		UseMockMpesa:        getBool("USE_MOCK_MPESA", true),

		KESPerUSD:           getFloat("KES_PER_USD", 129.50),
		MinDepositKES:       getFloat("MIN_DEPOSIT_KES", 50),
		MaxDepositKES:       getFloat("MAX_DEPOSIT_KES", 150000),
		DemoStartingBalance: getFloat("DEMO_STARTING_BALANCE", 10000),

		SignalInterval: getDuration("SIGNAL_INTERVAL", 2*time.Minute),
		SignalSymbols:  []string{"R_10", "R_25", "R_50", "R_75", "R_100", "BOOM500", "CRASH500", "EURUSD"},

		// Comma-separated. The default covers both the app a developer runs and
		// the one the browser suite starts on its own port — see the webServer
		// note in web/playwright.config.ts for why the suite does not share 5173.
		CORSOrigins: getList("CORS_ORIGIN", "http://localhost:5173,http://localhost:5174"),

		AuthRateEvery: getDuration("AUTH_RATE_EVERY", 3*time.Second),
		AuthRateBurst: int(getFloat("AUTH_RATE_BURST", 10)),

		CookieDomain: getEnv("COOKIE_DOMAIN", ""),
		CookieSecure: getBool("COOKIE_SECURE", false),

		SentryDSN:         getEnv("SENTRY_DSN", ""),
		SentryEnvironment: getEnv("SENTRY_ENVIRONMENT", getEnv("APP_ENV", "development")),
		SentrySampleRate:  getFloat("SENTRY_SAMPLE_RATE", 1.0),
		MetricsToken:      getEnv("METRICS_TOKEN", ""),
	}

	if c.JWTSecret == "" {
		if c.Env == "production" {
			return nil, fmt.Errorf("JWT_SECRET must be set in production")
		}
		c.JWTSecret = "dev-only-insecure-secret-change-me"
	}
	if c.Env == "production" {
		// A refresh cookie without Secure is sent over plaintext HTTP, which
		// defeats the point of moving it out of localStorage.
		if !c.CookieSecure {
			return nil, fmt.Errorf("COOKIE_SECURE must be true in production")
		}
		if c.MetricsToken == "" {
			return nil, fmt.Errorf("METRICS_TOKEN must be set in production: /metrics leaks deposit volumes")
		}
	}
	if !c.UseMockMpesa && (c.MpesaConsumerKey == "" || c.MpesaPasskey == "") {
		return nil, fmt.Errorf("MPESA_CONSUMER_KEY and MPESA_PASSKEY required when USE_MOCK_MPESA=false")
	}
	return c, nil
}

func getEnv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func getBool(k string, def bool) bool {
	if v := os.Getenv(k); v != "" {
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	}
	return def
}

func getFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f
		}
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
	}
	return def
}

// getList reads a comma-separated value into a slice, dropping blanks and
// trimming each entry. Kept forgiving because these are typed by hand into
// deployment configuration, where a trailing comma or a stray space is common and
// is never what the author meant.
func getList(k, def string) []string {
	raw := getEnv(k, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
