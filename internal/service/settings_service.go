package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// settingStore is the app_settings persistence SettingsService needs.
type settingStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// SettingsDefaults seeds missing rows at Bootstrap and answers a read whose row
// is absent or unparseable. Every one comes from the environment, and every one
// only ever *seeds*: an existing row is never overwritten on startup, so a flag
// flipped in production survives a deploy.
type SettingsDefaults struct {
	DigitContractsEnabled bool
	BotEnabled            bool
	BotAllowlist          string
	BotRealMoneyEnabled   bool
	WithdrawalsEnabled    bool
}

func (d SettingsDefaults) asMap() map[string]string {
	return map[string]string{
		repository.SettingDigitContractsEnabled: strconv.FormatBool(d.DigitContractsEnabled),
		repository.SettingBotEnabled:            strconv.FormatBool(d.BotEnabled),
		repository.SettingBotAllowlist:          d.BotAllowlist,
		repository.SettingBotRealMoneyEnabled:   strconv.FormatBool(d.BotRealMoneyEnabled),
		repository.SettingWithdrawalsEnabled:    strconv.FormatBool(d.WithdrawalsEnabled),
	}
}

type cachedSetting struct {
	value string
	at    time.Time
}

// SettingsService reads runtime feature flags. Values are cached briefly so
// /auth/me does not hit the database on every request, but a SQL flip still
// becomes visible without restarting the process.
//
// The cache is per key. It used to be a single field for the one flag that
// existed; three more arrived with automated trading, and four hand-rolled cache
// slots is three opportunities for one of them to be forgotten.
type SettingsService struct {
	settings settingStore
	log      *slog.Logger
	defaults map[string]string

	mu    sync.Mutex
	cache map[string]cachedSetting
	ttl   time.Duration
}

func NewSettingsService(store *repository.Store, defaults SettingsDefaults, log *slog.Logger) *SettingsService {
	return newSettingsService(store.Settings, defaults, log)
}

func newSettingsService(settings settingStore, defaults SettingsDefaults, log *slog.Logger) *SettingsService {
	if log == nil {
		log = slog.Default()
	}
	return &SettingsService{
		settings: settings,
		log:      log,
		defaults: defaults.asMap(),
		cache:    map[string]cachedSetting{},
		ttl:      10 * time.Second,
	}
}

// Bootstrap seeds every flag whose row is missing. An existing value — true or
// false — is never overwritten.
func (s *SettingsService) Bootstrap(ctx context.Context) error {
	// Ordered, so the log reads the same way on every boot.
	for _, key := range []string{
		repository.SettingDigitContractsEnabled,
		repository.SettingBotEnabled,
		repository.SettingBotAllowlist,
		repository.SettingBotRealMoneyEnabled,
		repository.SettingWithdrawalsEnabled,
	} {
		if _, err := s.settings.Get(ctx, key); err == nil {
			continue
		} else if !errors.Is(err, repository.ErrNotFound) {
			return err
		}
		value := s.defaults[key]
		if err := s.settings.Set(ctx, key, value); err != nil {
			return err
		}
		s.log.Info("seeded app setting", "key", key, "value", value)
	}
	return nil
}

// cachedString reads a flag through the cache. A missing row or a DB read error
// distinguishes itself: missing falls back to the default (and is cached, so a
// never-seeded flag does not query on every request), while a real error is
// returned so a caller does not mistake a broken database for a disabled feature.
func (s *SettingsService) cachedString(ctx context.Context, key string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if hit, ok := s.cache[key]; ok && time.Since(hit.at) < s.ttl {
		return hit.value, nil
	}

	raw, err := s.settings.Get(ctx, key)
	if errors.Is(err, repository.ErrNotFound) {
		raw = s.defaults[key]
	} else if err != nil {
		return "", err
	}
	s.cache[key] = cachedSetting{value: raw, at: time.Now()}
	return raw, nil
}

// cachedBool is cachedString plus parsing. An unparseable stored value is a
// misconfiguration, not a reason to fail a request, so it logs and takes the
// default — the same behaviour the single digit flag had.
func (s *SettingsService) cachedBool(ctx context.Context, key string) (bool, error) {
	raw, err := s.cachedString(ctx, key)
	if err != nil {
		return false, err
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		s.log.Error("invalid boolean app setting", "key", key, "value", raw, "err", err)
		def, _ := strconv.ParseBool(s.defaults[key])
		return def, nil
	}
	return b, nil
}

// DigitContractsEnabled is the global trading-surface flag.
// Missing row → the env default. Stored "true"/"false" → that value. Invalid
// stored value → the env default.
func (s *SettingsService) DigitContractsEnabled(ctx context.Context) (bool, error) {
	return s.cachedBool(ctx, repository.SettingDigitContractsEnabled)
}

// BotEnabled is the global automated-trading switch. Unlike the digit flag it
// gates the service, not just the surface: StartRun refuses when it is off, and a
// live run stops on its next check.
// WithdrawalsEnabled reports whether the withdrawal path is open. Read on every
// withdrawal attempt and on /auth/me, through the same cache as the other flags,
// so turning it on takes effect without a deploy.
func (s *SettingsService) WithdrawalsEnabled(ctx context.Context) (bool, error) {
	return s.cachedBool(ctx, repository.SettingWithdrawalsEnabled)
}

func (s *SettingsService) BotEnabled(ctx context.Context) (bool, error) {
	return s.cachedBool(ctx, repository.SettingBotEnabled)
}

// BotRealMoneyEnabled is what keeps v1 demo-only. Off means StartRun refuses a
// real account.
func (s *SettingsService) BotRealMoneyEnabled(ctx context.Context) (bool, error) {
	return s.cachedBool(ctx, repository.SettingBotRealMoneyEnabled)
}

// BotAllowlist is the raw comma-separated setting.
func (s *SettingsService) BotAllowlist(ctx context.Context) (string, error) {
	return s.cachedString(ctx, repository.SettingBotAllowlist)
}

// BotEnabledFor answers the only question callers actually have: may this user
// run a bot? Global switch, narrowed by an allowlist when one is set.
//
// An empty allowlist means everyone rather than nobody. That direction is chosen
// deliberately: the switch that turns the feature off is bot_enabled, and a
// second way to disable it — forgetting to fill in a list — would be a foot-gun
// during a rollout.
func (s *SettingsService) BotEnabledFor(ctx context.Context, userID uuid.UUID) (bool, error) {
	enabled, err := s.BotEnabled(ctx)
	if err != nil || !enabled {
		return false, err
	}
	raw, err := s.BotAllowlist(ctx)
	if err != nil {
		return false, err
	}
	return allowlistPermits(raw, userID), nil
}

// allowlistPermits is pure so the parsing is testable without a store: blank
// entries and stray whitespace are tolerated, because this value is typed by a
// human into a SQL statement.
func allowlistPermits(raw string, userID uuid.UUID) bool {
	if strings.TrimSpace(raw) == "" {
		return true
	}
	want := userID.String()
	for _, part := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}
	return false
}

// bustCache drops cached values so the next read hits the store. For tests, and
// for the admin kill switch, which must not leave a stale "enabled" in place for
// the cache's lifetime after it has just turned the feature off.
func (s *SettingsService) bustCache() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache = map[string]cachedSetting{}
}

// BustCache is the exported form, used after a write that must take effect now.
func (s *SettingsService) BustCache() { s.bustCache() }
