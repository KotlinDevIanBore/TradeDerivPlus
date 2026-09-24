package service

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

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
	s := newSettingsService(newMemSettings(), SettingsDefaults{DigitContractsEnabled: true}, testLog())
	got, err := s.DigitContractsEnabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatal("missing setting with default true → want true")
	}

	sFalse := newSettingsService(newMemSettings(), SettingsDefaults{DigitContractsEnabled: false}, testLog())
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
	s := newSettingsService(mem, SettingsDefaults{DigitContractsEnabled: false} /* default ignored when row exists */, testLog())
	got, err := s.DigitContractsEnabled(context.Background())
	if err != nil || !got {
		t.Fatalf("explicit true → want true, got %v err %v", got, err)
	}

	// Bust cache so the next read hits the store.
	s.bustCache()
	_ = mem.Set(context.Background(), repository.SettingDigitContractsEnabled, "false")
	got, err = s.DigitContractsEnabled(context.Background())
	if err != nil || got {
		t.Fatalf("explicit false → want false, got %v err %v", got, err)
	}
}

func TestBootstrap_seedsMissingOnly(t *testing.T) {
	mem := newMemSettings()
	s := newSettingsService(mem, SettingsDefaults{DigitContractsEnabled: true}, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := mem.Get(context.Background(), repository.SettingDigitContractsEnabled)
	if err != nil || raw != "true" {
		t.Fatalf("bootstrap seed = %q err %v, want true", raw, err)
	}

	// Existing false must not be overwritten when default is true.
	_ = mem.Set(context.Background(), repository.SettingDigitContractsEnabled, "false")
	s2 := newSettingsService(mem, SettingsDefaults{DigitContractsEnabled: true}, testLog())
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
	s := newSettingsService(mem, SettingsDefaults{DigitContractsEnabled: false}, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, _ := mem.Get(context.Background(), repository.SettingDigitContractsEnabled)
	if raw != "false" {
		t.Fatalf("DIGIT_CONTRACTS_ENABLED=false seed = %q", raw)
	}
}

// --- Automated-trading flags ------------------------------------------------

func TestBotEnabled_defaultsOff(t *testing.T) {
	// Off unless someone says otherwise: the payout table is still a placeholder,
	// so arriving enabled would be the wrong default.
	s := newSettingsService(newMemSettings(), SettingsDefaults{}, testLog())
	on, err := s.BotEnabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if on {
		t.Fatal("bot_enabled with no row and no env default → want false")
	}
	real, err := s.BotRealMoneyEnabled(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if real {
		t.Fatal("bot_real_money_enabled → want false, so v1 stays demo-only")
	}
}

func TestBootstrap_seedsEveryFlag(t *testing.T) {
	mem := newMemSettings()
	s := newSettingsService(mem, SettingsDefaults{
		DigitContractsEnabled: true,
		BotEnabled:            true,
		BotAllowlist:          "abc",
		BotRealMoneyEnabled:   false,
	}, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		repository.SettingDigitContractsEnabled: "true",
		repository.SettingBotEnabled:            "true",
		repository.SettingBotAllowlist:          "abc",
		repository.SettingBotRealMoneyEnabled:   "false",
	} {
		got, err := mem.Get(context.Background(), key)
		if err != nil {
			t.Fatalf("%s: %v", key, err)
		}
		if got != want {
			t.Errorf("%s seeded %q, want %q", key, got, want)
		}
	}
}

func TestBootstrap_doesNotOverwriteStoredBotFlags(t *testing.T) {
	mem := newMemSettings()
	// An operator turned it off in production; a deploy must not turn it back on.
	_ = mem.Set(context.Background(), repository.SettingBotEnabled, "false")

	s := newSettingsService(mem, SettingsDefaults{BotEnabled: true}, testLog())
	if err := s.Bootstrap(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, _ := mem.Get(context.Background(), repository.SettingBotEnabled)
	if got != "false" {
		t.Fatalf("bootstrap overwrote a stored false with %q", got)
	}
}

func TestBotEnabledFor_allowlist(t *testing.T) {
	alice := uuid.New()
	bob := uuid.New()

	cases := []struct {
		name      string
		enabled   string
		allowlist string
		user      uuid.UUID
		want      bool
	}{
		{"off for everyone when the switch is off", "false", "", alice, false},
		{"off even for a listed user when the switch is off", "false", alice.String(), alice, false},
		// Empty means everyone, not nobody: bot_enabled is the off switch, and a
		// half-filled rollout list should not silently become a second one.
		{"an empty list admits everyone", "true", "", alice, true},
		{"whitespace only is still empty", "true", "   ", alice, true},
		{"a listed user is admitted", "true", alice.String(), alice, true},
		{"an unlisted user is refused", "true", alice.String(), bob, false},
		{"one of several is admitted", "true", bob.String() + "," + alice.String(), alice, true},
		// The value is typed by a human into a SQL statement, so it is read
		// forgivingly.
		{"spaces and blanks around entries are tolerated", "true", " " + alice.String() + " , ,", alice, true},
		{"case does not matter", "true", strings.ToUpper(alice.String()), alice, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mem := newMemSettings()
			_ = mem.Set(context.Background(), repository.SettingBotEnabled, tc.enabled)
			_ = mem.Set(context.Background(), repository.SettingBotAllowlist, tc.allowlist)
			s := newSettingsService(mem, SettingsDefaults{}, testLog())

			got, err := s.BotEnabledFor(context.Background(), tc.user)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("BotEnabledFor = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBustCache_makesTheNextReadHitTheStore(t *testing.T) {
	mem := newMemSettings()
	_ = mem.Set(context.Background(), repository.SettingBotEnabled, "true")
	s := newSettingsService(mem, SettingsDefaults{}, testLog())

	if on, _ := s.BotEnabled(context.Background()); !on {
		t.Fatal("want true from the store")
	}

	// The kill switch turns it off and must not leave a stale yes behind for the
	// cache's lifetime — that is what BustCache is for.
	_ = mem.Set(context.Background(), repository.SettingBotEnabled, "false")
	if on, _ := s.BotEnabled(context.Background()); !on {
		t.Fatal("the cache should still be serving true here")
	}
	s.BustCache()
	if on, _ := s.BotEnabled(context.Background()); on {
		t.Fatal("after BustCache the next read must see false")
	}
}

func TestInvalidBooleanFlagFallsBackToTheDefault(t *testing.T) {
	mem := newMemSettings()
	_ = mem.Set(context.Background(), repository.SettingBotEnabled, "yes-please")
	s := newSettingsService(mem, SettingsDefaults{BotEnabled: true}, testLog())

	// A misconfiguration is not a reason to fail a request, and the safe reading
	// is the configured default rather than a guess.
	on, err := s.BotEnabled(context.Background())
	if err != nil {
		t.Fatalf("an unparseable value should not error: %v", err)
	}
	if !on {
		t.Fatal("want the env default, which is true here")
	}
}
