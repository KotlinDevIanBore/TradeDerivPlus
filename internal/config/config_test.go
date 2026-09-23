package config

import (
	"testing"
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
