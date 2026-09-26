package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// BadValue is one environment variable that was set to something this process
// could not parse, and the value it fell back to instead.
type BadValue struct {
	Key      string
	Value    string
	Fallback string
}

// Config is loaded once at boot. Nothing else in the app reads os.Getenv.
type Config struct {
	Env  string
	Port string

	// Ignored is every variable that was set but unparseable. Collected rather
	// than logged here, because Load runs before the logger exists; main.go
	// reports them the moment it can.
	//
	// This exists because a silent fallback is indistinguishable from a
	// deliberate setting. DIGIT_MAX_STAKE="1,000,000" became a $100 ceiling with
	// nothing in the logs and nothing wrong with the deployment to look at:
	// strconv.ParseFloat has no locale awareness, the error was discarded, and
	// the default looked chosen. Every variable reaching these parsers is a
	// bound, a flag or an interval — several of them money-critical, none of
	// them a secret — so the offending value is safe to print, and printing it
	// is the whole point.
	Ignored []BadValue

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

	// BotMaxStakeBalanceFraction is the largest share of the account balance one
	// automated contract may stake, as a fraction: 0.1 is a tenth. A run places
	// repeatedly, so a stake that is a large share of the balance is a different
	// risk from the same stake placed once by hand — this is that limit, and it is
	// a product choice rather than a figure derived from the payout table. It is
	// not a loss limit: the per-run stop loss is what bounds total loss.
	BotMaxStakeBalanceFraction float64

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
	// Status-query answers land here. A third URL because a Transaction Status
	// result is a different document from a payout result, and a handler that has
	// to guess which it holds can classify a failed QUERY as a failed payout.
	MpesaB2CStatusResultURL string

	// The payout reconciliation pass. PROVISIONAL, like the deposit schedule.
	// GiveUp does NOT refund — it stops asking and hands the row to a person,
	// because the customer's money is already out and we still do not know.
	MpesaPayoutQueryEvery time.Duration
	MpesaPayoutGiveUp     time.Duration

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

	// PublicAPIURL is this API's own externally reachable base, used ONLY to build
	// upload URLs for the in-memory document stub. Real R2 presigned URLs point at
	// Cloudflare and never at us, so this is unused whenever a bucket is
	// configured.
	PublicAPIURL string

	// Object storage for KYC documents (Cloudflare R2, S3-compatible).
	//
	// All five may be absent: a deployment that does not take verification
	// documents needs none of them, and the upload endpoints answer 503 rather
	// than failing obscurely. Present-but-partial is refused at boot, the same
	// all-or-none rule the payout block uses, because half a credential set is a
	// misconfiguration that would otherwise surface as a failed upload.
	R2AccountID       string
	R2AccessKeyID     string
	R2SecretAccessKey string
	R2BucketName      string
	// R2Endpoint overrides the account-derived endpoint. Normally empty.
	R2Endpoint string

	// KYC document handling.
	//
	// KYCMaxDocumentBytes caps a single document. Enforced twice: against the
	// browser's declared size before a URL is signed, and against the object's
	// actual size at confirm.
	KYCMaxDocumentBytes int64
	// KYCUploadURLExpiry is how long a presigned PUT stays valid. Short, because
	// the user is at the file picker when it is issued.
	KYCUploadURLExpiry time.Duration
	// KYCReviewURLExpiry is how long an admin's presigned GET stays valid. Minted
	// per click and never stored, so this wants to be short: an identity document
	// should not sit behind a URL that outlives the person looking at it.
	KYCReviewURLExpiry time.Duration
	// KYCAbandonedUploadAfter is when an unconsumed upload attempt and its object
	// become orphans to be deleted. Unrelated to document retention below: an
	// upload nobody finished is not a record of anything.
	KYCAbandonedUploadAfter time.Duration
	// KYCDocumentRetentionDays is how long a REVIEWED document is kept.
	//
	// ZERO MEANS NEVER DELETE, and zero is the default, deliberately. The
	// retention period for identity documents is a legal and compliance decision
	// that engineering cannot supply — see section 1 of
	// docs/kyc-r2-upload-plan.md. Shipping with this unset is shipping with
	// retention visibly unconfigured, which is honest; shipping with a number
	// somebody guessed would not be.
	KYCDocumentRetentionDays int
	// KYCRetentionEvery is how often the sweep runs. It does both jobs: orphan
	// cleanup always, document retention only when the days above are non-zero.
	KYCRetentionEvery time.Duration

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
	// The floor, in KES. Two jobs, and the second is the one that is easy to
	// miss: it keeps a withdrawal large enough that rounding the payout up to a
	// whole shilling (Daraja takes integers only) is immaterial against it, and
	// it stops a request so small that the debit rounds to nothing while a whole
	// shilling still leaves the business. See investigation-money-mechanics.md
	// finding 4.
	//
	// PROVISIONAL, like the ceilings above. Safaricom's B2C-specific minimum
	// could NOT be confirmed from Daraja's own documentation — the public API
	// page 404s and the portal requires an account — so this is set to match
	// MIN_DEPOSIT_KES and the documented KES 50 M-Pesa minimum agent withdrawal
	// rather than to a figure anyone has verified for B2C. Confirm it with
	// Safaricom before withdrawals are turned on.
	MinWithdrawKES float64

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

	ignored = nil

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

		BotMaxStakeBalanceFraction: getFloat("BOT_MAX_STAKE_BALANCE_FRACTION", 0.1),

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
		MpesaB2CStatusResultURL: getEnv("MPESA_B2C_STATUS_RESULT_URL", ""),

		PublicAPIURL: getEnv("PUBLIC_API_URL", "http://localhost:8080/api/v1"),

		R2AccountID:       getEnv("R2_ACCOUNT_ID", ""),
		R2AccessKeyID:     getEnv("R2_ACCESS_KEY_ID", ""),
		R2SecretAccessKey: getEnv("R2_SECRET_ACCESS_KEY", ""),
		R2BucketName:      getEnv("R2_BUCKET_NAME", ""),
		R2Endpoint:        getEnv("R2_ENDPOINT", ""),

		KYCMaxDocumentBytes:     int64(getFloat("KYC_MAX_DOCUMENT_BYTES", 10<<20)),
		KYCUploadURLExpiry:      getDuration("KYC_UPLOAD_URL_EXPIRY", 10*time.Minute),
		KYCReviewURLExpiry:      getDuration("KYC_REVIEW_URL_EXPIRY", 5*time.Minute),
		KYCAbandonedUploadAfter: getDuration("KYC_ABANDONED_UPLOAD_AFTER", 48*time.Hour),
		// Zero: never delete. See the field's comment.
		KYCDocumentRetentionDays: int(getFloat("KYC_DOCUMENT_RETENTION_DAYS", 0)),
		KYCRetentionEvery:        getDuration("KYC_RETENTION_EVERY", time.Hour),

		MpesaPayoutQueryEvery: getDuration("MPESA_PAYOUT_QUERY_EVERY", time.Minute),
		MpesaPayoutGiveUp:     getDuration("MPESA_PAYOUT_GIVE_UP", 24*time.Hour),

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
		MinWithdrawKES:     getFloat("MIN_WITHDRAW_KES", 50),

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

	// Attached before the validations below, so a config that fails one still
	// carries the reason a bound is not what the deployment says it is.
	c.Ignored = ignored

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
	payoutVars := []string{c.MpesaInitiatorName, c.MpesaSecurityCredential,
		c.MpesaB2CResultURL, c.MpesaB2CTimeoutURL, c.MpesaB2CStatusResultURL}
	anySet, allSet := false, true
	for _, v := range payoutVars {
		if v != "" {
			anySet = true
		} else {
			allSet = false
		}
	}
	if anySet {
		if !allSet {
			return nil, fmt.Errorf("payouts need all of MPESA_INITIATOR_NAME, MPESA_SECURITY_CREDENTIAL, " +
				"MPESA_B2C_RESULT_URL, MPESA_B2C_TIMEOUT_URL and MPESA_B2C_STATUS_RESULT_URL, or none of them")
		}
		// Three URLs, three meanings, and each pair must differ. Sharing any two
		// loses the distinction that tells an outcome from "we do not know", or a
		// payout's failure from a status query's own.
		urls := map[string]string{
			"MPESA_B2C_RESULT_URL":        c.MpesaB2CResultURL,
			"MPESA_B2C_TIMEOUT_URL":       c.MpesaB2CTimeoutURL,
			"MPESA_B2C_STATUS_RESULT_URL": c.MpesaB2CStatusResultURL,
		}
		seen := map[string]string{}
		for name, url := range urls {
			if other, dup := seen[url]; dup {
				return nil, fmt.Errorf("%s and %s must differ: each carries a different meaning, "+
					"and one handler for two of them cannot tell an outcome from 'we do not know'", other, name)
			}
			seen[url] = name
		}
	}
	if err := validateKYCStorage(c); err != nil {
		return nil, err
	}
	if c.MpesaPayoutQueryEvery <= 0 {
		return nil, fmt.Errorf("MPESA_PAYOUT_QUERY_EVERY must be positive")
	}
	if c.MpesaPayoutGiveUp <= 0 {
		return nil, fmt.Errorf("MPESA_PAYOUT_GIVE_UP must be positive")
	}
	if c.MaxWithdrawKES <= 0 || c.MaxWithdrawDayKES <= 0 {
		return nil, fmt.Errorf("MAX_WITHDRAW_KES and MAX_WITHDRAW_DAY_KES must be positive")
	}
	if c.MaxWithdrawDayKES < c.MaxWithdrawKES {
		return nil, fmt.Errorf("MAX_WITHDRAW_DAY_KES (%.0f) must be at least MAX_WITHDRAW_KES (%.0f), or no withdrawal of the maximum size could ever be made",
			c.MaxWithdrawDayKES, c.MaxWithdrawKES)
	}
	// Refused rather than defaulted: a zero or negative floor is not "no
	// minimum", it is the guard silently absent, which is the state finding 4
	// described.
	if c.MinWithdrawKES <= 0 {
		return nil, fmt.Errorf("MIN_WITHDRAW_KES must be positive")
	}
	if c.MinWithdrawKES > c.MaxWithdrawKES {
		return nil, fmt.Errorf("MIN_WITHDRAW_KES (%.0f) must not exceed MAX_WITHDRAW_KES (%.0f), or no withdrawal could ever be made",
			c.MinWithdrawKES, c.MaxWithdrawKES)
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

// ignored accumulates unparseable values for the Load in progress. A package
// variable because the getters are plain functions called from one composite
// literal; Load resets it and is not safe to call concurrently, which was
// already true of it (godotenv.Load and os.Getenv are process-wide).
var ignored []BadValue

// note records a value this process could not parse. The fallback is recorded
// alongside it: "BOT_MAX_RUNS was ignored" leaves a reader guessing what is
// actually in force, which is the same problem one layer along.
func note(key, value string, fallback any) {
	ignored = append(ignored, BadValue{Key: key, Value: value, Fallback: fmt.Sprint(fallback)})
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
		note(k, v, def)
	}
	return def
}

func getFloat(k string, def float64) float64 {
	if v := os.Getenv(k); v != "" {
		f, err := strconv.ParseFloat(v, 64)
		if err == nil {
			return f
		}
		note(k, v, def)
	}
	return def
}

func getDuration(k string, def time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		d, err := time.ParseDuration(v)
		if err == nil {
			return d
		}
		note(k, v, def)
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

// validateKYCStorage checks the object-storage and document-handling
// configuration. Split out of Load so a test can reach it with a Config literal
// instead of staging two dozen unrelated environment variables — and so the rule
// is stated in one place rather than inline among a dozen others.
func validateKYCStorage(c *Config) error {
	// R2 is validated on the same all-or-none rule as payouts, and for the same
	// reason: a partial set is a misconfiguration, and the failure should happen
	// at boot rather than when a customer tries to upload an ID. Absent entirely
	// is legal — the upload endpoints answer 503 and say so.
	r2Vars := map[string]string{
		"R2_ACCOUNT_ID":        c.R2AccountID,
		"R2_ACCESS_KEY_ID":     c.R2AccessKeyID,
		"R2_SECRET_ACCESS_KEY": c.R2SecretAccessKey,
		"R2_BUCKET_NAME":       c.R2BucketName,
	}
	r2Any, r2Missing := false, []string{}
	for name, v := range r2Vars {
		if v != "" {
			r2Any = true
		} else {
			r2Missing = append(r2Missing, name)
		}
	}
	if r2Any && len(r2Missing) > 0 {
		sort.Strings(r2Missing)
		return fmt.Errorf("object storage needs all of R2_ACCOUNT_ID, R2_ACCESS_KEY_ID, "+
			"R2_SECRET_ACCESS_KEY and R2_BUCKET_NAME, or none of them; missing: %s",
			strings.Join(r2Missing, ", "))
	}
	if c.KYCMaxDocumentBytes <= 0 {
		return fmt.Errorf("KYC_MAX_DOCUMENT_BYTES must be positive")
	}
	if c.KYCUploadURLExpiry <= 0 || c.KYCReviewURLExpiry <= 0 {
		return fmt.Errorf("KYC_UPLOAD_URL_EXPIRY and KYC_REVIEW_URL_EXPIRY must be positive")
	}
	if c.KYCAbandonedUploadAfter <= 0 {
		return fmt.Errorf("KYC_ABANDONED_UPLOAD_AFTER must be positive")
	}
	if c.KYCRetentionEvery <= 0 {
		return fmt.Errorf("KYC_RETENTION_EVERY must be positive")
	}
	// Negative is a typo, not a policy. Zero is the documented "never delete".
	if c.KYCDocumentRetentionDays < 0 {
		return fmt.Errorf("KYC_DOCUMENT_RETENTION_DAYS cannot be negative (0 means never delete)")
	}
	return nil
}
