package service

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// settingStore is the app_settings persistence SettingsService needs.
type settingStore interface {
	Get(ctx context.Context, key string) (string, error)
	Set(ctx context.Context, key, value string) error
}

// SettingsService reads runtime feature flags. Values are cached briefly so
// /auth/me does not hit the database on every request, but a SQL flip still
// becomes visible without restarting the process.
type SettingsService struct {
	settings settingStore
	log      *slog.Logger

	// digitDefault is used when the DB row is missing (and as Bootstrap seed).
	// Comes from DIGIT_CONTRACTS_ENABLED, which itself defaults to true.
	digitDefault bool

	mu       sync.Mutex
	digit    bool
	digitAt  time.Time
	digitTTL time.Duration
}

func NewSettingsService(store *repository.Store, digitDefault bool, log *slog.Logger) *SettingsService {
	return newSettingsService(store.Settings, digitDefault, log)
}

func newSettingsService(settings settingStore, digitDefault bool, log *slog.Logger) *SettingsService {
	if log == nil {
		log = slog.Default()
	}
	return &SettingsService{
		settings:     settings,
		log:          log,
		digitDefault: digitDefault,
		digitTTL:     10 * time.Second,
	}
}

// Bootstrap ensures digit_contracts_enabled exists. Env/default only seeds a
// missing row; an existing true or false is never overwritten on startup.
func (s *SettingsService) Bootstrap(ctx context.Context) error {
	_, err := s.settings.Get(ctx, repository.SettingDigitContractsEnabled)
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	value := strconv.FormatBool(s.digitDefault)
	if err := s.settings.Set(ctx, repository.SettingDigitContractsEnabled, value); err != nil {
		return err
	}
	s.log.Info("seeded app setting", "key", repository.SettingDigitContractsEnabled, "value", value)
	return nil
}

// DigitContractsEnabled is the global trading-surface flag.
// Missing row → digitDefault (true unless DIGIT_CONTRACTS_ENABLED=false).
// Stored "true"/"false" → that value. Invalid stored value → digitDefault.
func (s *SettingsService) DigitContractsEnabled(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.digitAt.IsZero() && time.Since(s.digitAt) < s.digitTTL {
		return s.digit, nil
	}

	raw, err := s.settings.Get(ctx, repository.SettingDigitContractsEnabled)
	if errors.Is(err, repository.ErrNotFound) {
		s.digit = s.digitDefault
		s.digitAt = time.Now()
		return s.digit, nil
	}
	if err != nil {
		return false, err
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		s.log.Error("invalid digit_contracts_enabled value", "value", raw, "err", err)
		s.digit = s.digitDefault
		s.digitAt = time.Now()
		return s.digit, nil
	}
	s.digit = b
	s.digitAt = time.Now()
	return s.digit, nil
}
