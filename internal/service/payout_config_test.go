package service

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
)

// payoutConfig is a fully-populated payout configuration, shaped the way
// config.Load returns one when all five MPESA_* payout variables are set in the
// environment. The three callback URLs are deliberately distinct: a client that
// received the same URL twice is as broken as one that received none, and
// ValidateB2CConfig refuses both.
func payoutConfig() *config.Config {
	return &config.Config{
		UseMockMpesa:            false,
		MpesaBaseURL:            "https://api.safaricom.co.ke",
		MpesaConsumerKey:        "consumer-key",
		MpesaConsumerSecret:     "consumer-secret",
		MpesaShortcode:          "123456",
		MpesaPasskey:            "passkey",
		MpesaCallbackURL:        "https://example.invalid/api/v1/mpesa/callback",
		MpesaInitiatorName:      "apiuser",
		MpesaSecurityCredential: "base64-security-credential",
		MpesaB2CResultURL:       "https://example.invalid/api/v1/mpesa/b2c/result",
		MpesaB2CTimeoutURL:      "https://example.invalid/api/v1/mpesa/b2c/timeout",
		MpesaB2CStatusResultURL: "https://example.invalid/api/v1/mpesa/b2c/status",
	}
}

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestPayoutConfigReachesTheClient builds the service the way main.go does and
// asserts the boot-time check passes. This is the test that was missing when the
// constructor silently dropped cfg.MpesaB2CStatusResultURL: pkg/mpesaclient's
// own suite proved ValidateB2CConfig rejects an empty status-result URL, but it
// built Options by hand, so nothing exercised the config -> Options path and a
// compile-clean omission reached production as a crash loop. Every deployment
// with USE_MOCK_MPESA=false failed to boot no matter what the environment held.
func TestPayoutConfigReachesTheClient(t *testing.T) {
	svc := NewMpesaPayoutService(nil, payoutConfig(), quietLog())
	if err := svc.ValidateConfig(); err != nil {
		t.Fatalf("a fully-populated payout configuration was refused at boot: %v", err)
	}
}

// TestEveryPayoutConfigFieldIsCarried blanks one field at a time and requires
// the boot check to notice. Asserting only that a full configuration passes
// would catch a dropped field, but not a field wired to the wrong source or to a
// constant — either of those still passes with everything populated. Blanking
// each in turn pins that the value the client validates is the value config
// supplied, one field at a time.
func TestEveryPayoutConfigFieldIsCarried(t *testing.T) {
	cases := []struct {
		envVar string
		blank  func(*config.Config)
	}{
		{"MPESA_INITIATOR_NAME", func(c *config.Config) { c.MpesaInitiatorName = "" }},
		{"MPESA_SECURITY_CREDENTIAL", func(c *config.Config) { c.MpesaSecurityCredential = "" }},
		{"MPESA_B2C_RESULT_URL", func(c *config.Config) { c.MpesaB2CResultURL = "" }},
		{"MPESA_B2C_TIMEOUT_URL", func(c *config.Config) { c.MpesaB2CTimeoutURL = "" }},
		{"MPESA_B2C_STATUS_RESULT_URL", func(c *config.Config) { c.MpesaB2CStatusResultURL = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.envVar, func(t *testing.T) {
			cfg := payoutConfig()
			tc.blank(cfg)
			err := NewMpesaPayoutService(nil, cfg, quietLog()).ValidateConfig()
			if err == nil {
				t.Fatalf("%s was empty in config and the boot check passed anyway: "+
					"the constructor is not carrying it to the client", tc.envVar)
			}
			if !strings.Contains(err.Error(), "b2c is not configured") {
				t.Fatalf("%s empty produced an unrelated error: %v", tc.envVar, err)
			}
		})
	}
}

// TestPayoutConfigRefusesTwoChannelsOnOneURL guards the other way a client can
// be mis-wired and still start: a copy-paste that points two of the three
// callbacks at one URL. That loses the distinction between an outcome and "we do
// not know", which is the whole reason the URLs are separate.
func TestPayoutConfigRefusesTwoChannelsOnOneURL(t *testing.T) {
	cfg := payoutConfig()
	cfg.MpesaB2CStatusResultURL = cfg.MpesaB2CResultURL
	if err := NewMpesaPayoutService(nil, cfg, quietLog()).ValidateConfig(); err == nil {
		t.Fatal("the status-result URL was allowed to equal the payout result URL")
	}
}

// TestMockModeNeedsNoPayoutConfig pins that the check stays skippable in mock
// mode: a developer running the app locally has none of these values, and the
// boot check must not demand them.
func TestMockModeNeedsNoPayoutConfig(t *testing.T) {
	cfg := &config.Config{UseMockMpesa: true}
	if err := NewMpesaPayoutService(nil, cfg, quietLog()).ValidateConfig(); err != nil {
		t.Fatalf("mock mode demanded payout configuration: %v", err)
	}
}
