package config

import (
	"testing"
	"time"
)

func TestGetBool_unsetUsesDefault(t *testing.T) {
	t.Setenv("DIGIT_CONTRACTS_ENABLED_TEST_UNSET", "")
	// Empty string means unset for getBool — falls through to default.
	if getBool("DIGIT_CONTRACTS_ENABLED_TEST_UNSET", true) != true {
		t.Fatal("unset + default true → want true")
	}
	if getBool("DIGIT_CONTRACTS_ENABLED_TEST_UNSET", false) != false {
		t.Fatal("unset + default false → want false")
	}
}

func TestGetBool_explicitOverrides(t *testing.T) {
	t.Setenv("DIGIT_CONTRACTS_ENABLED_TEST", "false")
	if getBool("DIGIT_CONTRACTS_ENABLED_TEST", true) != false {
		t.Fatal("explicit false must win over default true")
	}
	t.Setenv("DIGIT_CONTRACTS_ENABLED_TEST", "true")
	if getBool("DIGIT_CONTRACTS_ENABLED_TEST", false) != true {
		t.Fatal("explicit true must win over default false")
	}
}

func TestLoad_digitContractsEnabledDefaultsTrue(t *testing.T) {
	t.Setenv("DIGIT_CONTRACTS_ENABLED", "")
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.DigitContractsEnabled {
		t.Fatal("DIGIT_CONTRACTS_ENABLED unset → DigitContractsEnabled should default true")
	}
}

// STK reconciliation schedule. The values are provisional; the floor is not.

func TestLoad_reconcileDefaults(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	for _, k := range []string{"MPESA_RECONCILE_EVERY", "MPESA_RECONCILE_MIN_AGE", "MPESA_RECONCILE_GIVE_UP"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MpesaReconcileEvery != time.Minute || cfg.MpesaReconcileMinAge != 3*time.Minute || cfg.MpesaReconcileGiveUp != 24*time.Hour {
		t.Fatalf("defaults every=%s min_age=%s give_up=%s, want 1m/3m/24h",
			cfg.MpesaReconcileEvery, cfg.MpesaReconcileMinAge, cfg.MpesaReconcileGiveUp)
	}
}

// Below the floor the pass could reach a deposit the mock callback is still
// about to settle (4–9s). Whatever the operator asks for, MinAge is at least 60s.
func TestLoad_reconcileMinAgeIsFloored(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	t.Setenv("MPESA_RECONCILE_GIVE_UP", "")

	for _, v := range []string{"5s", "0s", "59s"} {
		t.Setenv("MPESA_RECONCILE_MIN_AGE", v)
		cfg, err := Load()
		if err != nil {
			t.Fatalf("MIN_AGE=%s: %v", v, err)
		}
		if cfg.MpesaReconcileMinAge != MpesaReconcileMinAgeFloor {
			t.Fatalf("MIN_AGE=%s loaded as %s, want the %s floor", v, cfg.MpesaReconcileMinAge, MpesaReconcileMinAgeFloor)
		}
	}

	t.Setenv("MPESA_RECONCILE_MIN_AGE", "10m")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MpesaReconcileMinAge != 10*time.Minute {
		t.Fatalf("MIN_AGE=10m loaded as %s — the floor is a floor, not a fixed value", cfg.MpesaReconcileMinAge)
	}
}

// A give-up shorter than the min-age means every row is abandoned before it
// is ever queried. That is a misconfiguration, so Load refuses rather than
// booting a reconciler that reconciles nothing.
func TestLoad_reconcileGiveUpMustOutlastMinAge(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	t.Setenv("MPESA_RECONCILE_MIN_AGE", "3m")
	t.Setenv("MPESA_RECONCILE_GIVE_UP", "2m")
	if _, err := Load(); err == nil {
		t.Fatal("GIVE_UP=2m with MIN_AGE=3m loaded without error")
	}

	// The floor participates: a 30s min-age becomes 60s, and a 45s give-up is
	// then too short even though it was longer than what was typed.
	t.Setenv("MPESA_RECONCILE_MIN_AGE", "30s")
	t.Setenv("MPESA_RECONCILE_GIVE_UP", "45s")
	if _, err := Load(); err == nil {
		t.Fatal("GIVE_UP=45s with a floored MIN_AGE of 60s loaded without error")
	}
}

func TestLoad_reconcileEveryMustBePositive(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	t.Setenv("MPESA_RECONCILE_EVERY", "0s")
	if _, err := Load(); err == nil {
		t.Fatal("EVERY=0s loaded without error — a zero ticker panics at Start")
	}
}

// An unparseable value falls back, and says so. The silent half of that pair is
// what made DIGIT_MAX_STAKE="1,000,000" a $100 ceiling with nothing in the logs.

func TestGetters_recordAValueTheyCouldNotParse(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		value    string
		read     func(string) any
		want     any
		fallback string
	}{
		{
			// The one that actually happened: ParseFloat has no locale awareness,
			// so a thousands separator is not a large number, it is not a number.
			name:     "a thousands separator is not a number",
			key:      "TEST_IGNORED_FLOAT",
			value:    "1,000,000",
			read:     func(k string) any { return getFloat(k, 100) },
			want:     float64(100),
			fallback: "100",
		},
		{
			// ParseBool takes true/false/1/0/t/f, not the words a person reaches
			// for. "yes" on a money flag reads as off, and reads as deliberate.
			name:     "yes is not a bool",
			key:      "TEST_IGNORED_BOOL",
			value:    "yes",
			read:     func(k string) any { return getBool(k, false) },
			want:     false,
			fallback: "false",
		},
		{
			name:     "a bare number is not a duration",
			key:      "TEST_IGNORED_DURATION",
			value:    "30",
			read:     func(k string) any { return getDuration(k, time.Minute) },
			want:     time.Minute,
			fallback: "1m0s",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ignored = nil
			t.Setenv(tc.key, tc.value)

			if got := tc.read(tc.key); got != tc.want {
				t.Errorf("value %v, want the fallback %v", got, tc.want)
			}
			if len(ignored) != 1 {
				t.Fatalf("recorded %d ignored values, want 1: a silent fallback is the bug", len(ignored))
			}
			got := ignored[0]
			if got.Key != tc.key || got.Value != tc.value || got.Fallback != tc.fallback {
				t.Errorf("recorded %+v, want key %s value %q fallback %q",
					got, tc.key, tc.value, tc.fallback)
			}
		})
	}
}

func TestGetters_recordNothingWhenTheValueParses(t *testing.T) {
	ignored = nil
	t.Setenv("TEST_QUIET_FLOAT", "1000000")
	t.Setenv("TEST_QUIET_BOOL", "true")
	t.Setenv("TEST_QUIET_DURATION", "30s")

	if got := getFloat("TEST_QUIET_FLOAT", 100); got != 1_000_000 {
		t.Errorf("float %v, want 1000000", got)
	}
	if !getBool("TEST_QUIET_BOOL", false) {
		t.Error("bool should be true")
	}
	if got := getDuration("TEST_QUIET_DURATION", time.Minute); got != 30*time.Second {
		t.Errorf("duration %v, want 30s", got)
	}
	// An unset variable is not a mistake either: the default is the design.
	if got := getFloat("TEST_NEVER_SET_AT_ALL", 7); got != 7 {
		t.Errorf("unset float %v, want 7", got)
	}
	if len(ignored) != 0 {
		t.Errorf("recorded %+v, want nothing: these all parsed", ignored)
	}
}

// Load carries the report, and starts clean each time so one boot's mistakes are
// not attributed to the next.
func TestLoad_reportsIgnoredValuesAndResetsBetweenLoads(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-thirty-two-bytes!!")
	t.Setenv("DIGIT_MAX_STAKE", "1,000,000")
	t.Setenv("BOT_MAX_STAKE_BALANCE_FRACTION", "10%")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DigitMaxStake != 100 {
		t.Errorf("DigitMaxStake %v, want the 100 default", cfg.DigitMaxStake)
	}
	if cfg.BotMaxStakeBalanceFraction != 0.1 {
		t.Errorf("BotMaxStakeBalanceFraction %v, want the 0.1 default", cfg.BotMaxStakeBalanceFraction)
	}

	keys := map[string]string{}
	for _, bad := range cfg.Ignored {
		keys[bad.Key] = bad.Value
	}
	if keys["DIGIT_MAX_STAKE"] != "1,000,000" {
		t.Errorf("Ignored is %+v, want DIGIT_MAX_STAKE in it", cfg.Ignored)
	}
	if keys["BOT_MAX_STAKE_BALANCE_FRACTION"] != "10%" {
		t.Errorf("Ignored is %+v, want BOT_MAX_STAKE_BALANCE_FRACTION in it", cfg.Ignored)
	}

	// A second Load with the values fixed must report nothing, or the warning
	// becomes noise that outlives the mistake.
	t.Setenv("DIGIT_MAX_STAKE", "1000000")
	t.Setenv("BOT_MAX_STAKE_BALANCE_FRACTION", "0.1")
	clean, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(clean.Ignored) != 0 {
		t.Errorf("a clean load reports %+v, want nothing", clean.Ignored)
	}
	if clean.DigitMaxStake != 1_000_000 {
		t.Errorf("DigitMaxStake %v, want 1000000", clean.DigitMaxStake)
	}
}
