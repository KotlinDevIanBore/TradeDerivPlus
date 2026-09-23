package service

import (
	"context"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

type memSettings struct {
	mu   sync.Mutex
	data map[string]string
}

func newMemSettings() *memSettings {
	return &memSettings{data: map[string]string{}}
}

func (m *memSettings) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.data[key]
	if !ok {
		return "", repository.ErrNotFound
	}
	return v, nil
}

func (m *memSettings) Set(_ context.Context, key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data[key] = value
	return nil
}

func testLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestDigitContractsEnabled_missingUsesDefault(t *testing.T) {
	s := newSettingsService(newMemSettings(), true, testLog())
	got, err := s.DigitContractsEnabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("missing setting with default true → want true")
	}

	sFalse := newSettingsService(newMemSettings(), false, testLog())
	got, err = sFalse.DigitContractsEnabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got {
		t.Fatal("missing setting with default false → want false")
	}
}

func TestDigitContractsEnabled_explicitValues(t *testing.T) {
	mem := newMemSettings()
	_ = mem.Set(context.Background(), repository.SettingDigitContractsEnabled, "true")
	s := newSettingsService(mem, false /* default ignored when row exists */, testLog())
	got, err := s.DigitContractsEnabled(context.Background())
	if err != nil || !got {
		t.Fatalf("explicit true → want true, got %v err %v", got, err)
	}

	// Bust cache so the next read hits the store.
	s.digitAt = time.Time{}
	_ = mem.Set(context.Background(), repository.SettingDigitContractsEnabled, "false")
	got, err = s.DigitContractsEnabled(context.Background())
	if err != nil || got {
		t.Fatalf("explicit false → want false, got %v err %v", got, err)
	}
}

func TestBootstrap_seedsMissingOnly(t *testing.T) {
	mem := newMemSettings()
	s := newSettingsService(mem, true, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := mem.Get(context.Background(), repository.SettingDigitContractsEnabled)
	if err != nil || raw != "true" {
		t.Fatalf("bootstrap seed = %q err %v, want true", raw, err)
	}

	// Existing false must not be overwritten when default is true.
	_ = mem.Set(context.Background(), repository.SettingDigitContractsEnabled, "false")
	s2 := newSettingsService(mem, true, testLog())
	if err := s2.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err = mem.Get(context.Background(), repository.SettingDigitContractsEnabled)
	if err != nil || raw != "false" {
		t.Fatalf("bootstrap must not overwrite stored false, got %q err %v", raw, err)
	}
}

func TestBootstrap_seedsFalseFromEnvDefault(t *testing.T) {
	mem := newMemSettings()
	s := newSettingsService(mem, false, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, _ := mem.Get(context.Background(), repository.SettingDigitContractsEnabled)
	if raw != "false" {
		t.Fatalf("DIGIT_CONTRACTS_ENABLED=false seed = %q", raw)
	}
}
