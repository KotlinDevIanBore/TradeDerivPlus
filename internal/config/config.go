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

	// Payouts (B2C). These authenticate as an "API user" created on the M-PESA
	// organisation portal, not with the STK passkey.
	//
	// MpesaSecurityCredential is the initiator's password ALREADY encrypted under
	// Safaricom's certificate — generate it once with `go run ./cmd/gencred` and
	// set the result here. The server deliberately never holds the plaintext
	// password and never reads a certificate.
	//
	// The two callback URLs must differ. A queue timeout means "we do not know",
	// and one handler for both channels cannot tell that from a failure — which
	// would refund customers who were in fact paid. Load refuses them equal.
	MpesaInitiatorName      string
	MpesaSecurityCredential string
	MpesaB2CResultURL       string
	MpesaB2CTimeoutURL      string

	// STK reconciliation: the pass that asks Daraja about deposits whose
	// callback has not arrived. The numbers are PROVISIONAL — guesses at what
	// Daraja tolerates, to be replaced once scripts/daraja_probe.py has been run
	// against the sandbox (stk-reconciliation-plan.md §2.5). The shape is not
	// provisional: never query immediately, widen, stop eventually.
	//
	// MinAge is floored at 60s in Load. In mock mode the simulated callback
	// settles a deposit 4–9s after the push; the floor is what keeps the pass off
	// every deposit the mock is about to resolve, and so keeps smoke.py and the
	// browser suite untouched.
	MpesaReconcileEvery  time.Duration
	MpesaReconcileMinAge time.Duration
	MpesaReconcileGiveUp time.Duration

	// Money
	// Deposits arrive in KES, wallets are denominated in USD (gap G8).
	KESPerUSD           float64
	MinDepositKES       float64
	MaxDepositKES       float64
	DemoStartingBalance float64

	// Withdrawals. Seeds withdrawals_enabled once, like the bot flags: default
	// FALSE because a withdrawal debits the balance and waits on an M-Pesa payout
	// that does not exist yet.
	WithdrawalsEnabled bool
	// Per-transaction and per-account-per-day ceilings, in KES. PROVISIONAL and a
	// product decision, not an engineering one: the figures below sit under
	// Daraja's own B2C limits (a daily 500,000 per the API's limit-rule result
	// codes) so a refusal happens here, at validation, rather than as a B2C
	// failure after the customer has already been debited. Deposits are capped at
	// MAX_DEPOSIT_KES; withdrawals had no ceiling at all before this.
	MaxWithdrawKES    float64
	MaxWithdrawDayKES float64

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

// MpesaReconcileMinAgeFloor is the least MPESA_RECONCILE_MIN_AGE Load will
// accept. Below it the pass could reach a deposit the mock callback is still
// about to settle; above it there is nothing to protect, so it is a floor and
// not a fixed value.
const MpesaReconcileMinAgeFloor = 60 * time.Second

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

		MpesaInitiatorName:      getEnv("MPESA_INITIATOR_NAME", ""),
		MpesaSecurityCredential: getEnv("MPESA_SECURITY_CREDENTIAL", ""),
		MpesaB2CResultURL:       getEnv("MPESA_B2C_RESULT_URL", ""),
		MpesaB2CTimeoutURL:      getEnv("MPESA_B2C_TIMEOUT_URL", ""),

		MpesaReconcileEvery:  getDuration("MPESA_RECONCILE_EVERY", time.Minute),
		MpesaReconcileMinAge: getDuration("MPESA_RECONCILE_MIN_AGE", 3*time.Minute),
		MpesaReconcileGiveUp: getDuration("MPESA_RECONCILE_GIVE_UP", 24*time.Hour),

		KESPerUSD:           getFloat("KES_PER_USD", 129.50),
		MinDepositKES:       getFloat("MIN_DEPOSIT_KES", 50),
		MaxDepositKES:       getFloat("MAX_DEPOSIT_KES", 150000),
		DemoStartingBalance: getFloat("DEMO_STARTING_BALANCE", 10000),

		WithdrawalsEnabled: getBool("WITHDRAWALS_ENABLED", false),
		MaxWithdrawKES:     getFloat("MAX_WITHDRAW_KES", 150000),
		MaxWithdrawDayKES:  getFloat("MAX_WITHDRAW_DAY_KES", 300000),

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
	// Payout configuration is validated when any of it is present, so a partial
	// or collapsed setup fails at boot rather than at the first withdrawal. It is
	// allowed to be entirely absent: a deployment that only takes deposits needs
	// none of it, and withdrawals_enabled is off by default.
	if c.MpesaInitiatorName != "" || c.MpesaSecurityCredential != "" ||
		c.MpesaB2CResultURL != "" || c.MpesaB2CTimeoutURL != "" {
		if c.MpesaInitiatorName == "" || c.MpesaSecurityCredential == "" ||
			c.MpesaB2CResultURL == "" || c.MpesaB2CTimeoutURL == "" {
			return nil, fmt.Errorf("payouts need all of MPESA_INITIATOR_NAME, MPESA_SECURITY_CREDENTIAL, " +
				"MPESA_B2C_RESULT_URL and MPESA_B2C_TIMEOUT_URL, or none of them")
		}
		if c.MpesaB2CResultURL == c.MpesaB2CTimeoutURL {
			return nil, fmt.Errorf("MPESA_B2C_RESULT_URL and MPESA_B2C_TIMEOUT_URL must differ: " +
				"a queue timeout means 'we do not know', and one handler for both cannot tell it from a failure")
		}
	}
	if c.MaxWithdrawKES <= 0 || c.MaxWithdrawDayKES <= 0 {
		return nil, fmt.Errorf("MAX_WITHDRAW_KES and MAX_WITHDRAW_DAY_KES must be positive")
	}
	if c.MaxWithdrawDayKES < c.MaxWithdrawKES {
		return nil, fmt.Errorf("MAX_WITHDRAW_DAY_KES (%.0f) must be at least MAX_WITHDRAW_KES (%.0f), or no withdrawal of the maximum size could ever be made",
			c.MaxWithdrawDayKES, c.MaxWithdrawKES)
	}
	if c.MpesaReconcileMinAge < MpesaReconcileMinAgeFloor {
		c.MpesaReconcileMinAge = MpesaReconcileMinAgeFloor
	}
	if c.MpesaReconcileEvery <= 0 {
		return nil, fmt.Errorf("MPESA_RECONCILE_EVERY must be positive")
	}
	if c.MpesaReconcileGiveUp <= c.MpesaReconcileMinAge {
		return nil, fmt.Errorf("MPESA_RECONCILE_GIVE_UP (%s) must be longer than MPESA_RECONCILE_MIN_AGE (%s), or nothing is ever queried",
			c.MpesaReconcileGiveUp, c.MpesaReconcileMinAge)
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
