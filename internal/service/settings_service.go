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

// SettingsService reads runtime feature flags. Values are cached briefly so
// /auth/me does not hit the database on every request, but a SQL flip still
// becomes visible without restarting the process.
type SettingsService struct {
	store *repository.Store
	log   *slog.Logger

	// Bootstrap default when the row is missing — from DIGIT_CONTRACTS_ENABLED.
	digitDefault bool

	mu       sync.Mutex
	digit    bool
	digitAt  time.Time
	digitTTL time.Duration
}

func NewSettingsService(store *repository.Store, digitDefault bool, log *slog.Logger) *SettingsService {
	return &SettingsService{
		store:        store,
		log:          log,
		digitDefault: digitDefault,
		digitTTL:     10 * time.Second,
	}
}

// Bootstrap ensures digit_contracts_enabled exists. Env is only the seed for a
// missing row; after that the DB wins.
func (s *SettingsService) Bootstrap(ctx context.Context) error {
	_, err := s.store.Settings.Get(ctx, repository.SettingDigitContractsEnabled)
	if err == nil {
		return nil
	}
	if !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	value := strconv.FormatBool(s.digitDefault)
	if err := s.store.Settings.Set(ctx, repository.SettingDigitContractsEnabled, value); err != nil {
		return err
	}
	s.log.Info("seeded app setting", "key", repository.SettingDigitContractsEnabled, "value", value)
	return nil
}

// DigitContractsEnabled is the global trading-surface flag.
func (s *SettingsService) DigitContractsEnabled(ctx context.Context) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.digitAt.IsZero() && time.Since(s.digitAt) < s.digitTTL {
		return s.digit, nil
	}

	raw, err := s.store.Settings.Get(ctx, repository.SettingDigitContractsEnabled)
	if errors.Is(err, repository.ErrNotFound) {
		// Should not happen after Bootstrap; fall back rather than fail /me.
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
		return s.digitDefault, nil
	}
	s.digit = b
	s.digitAt = time.Now()
	return s.digit, nil
}
