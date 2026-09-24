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
