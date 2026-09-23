package service_test

import (
	"strings"
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

func service_SeededAdminPassword() string { return service.SeededAdminPassword }

// The freeze semantics are a product decision, not just an implementation
// detail, so they are asserted in a test that reads like the decision.

func TestAccountIsFrozen(t *testing.T) {
	cases := []struct {
		status string
		frozen bool
	}{
		{models.AccountActive, false},
		{models.AccountFrozen, true},
		{"", false}, // an unset status is not a freeze
	}
	for _, c := range cases {
		a := models.Account{Status: c.status}
		if got := a.IsFrozen(); got != c.frozen {
			t.Errorf("IsFrozen() with status %q = %v, want %v", c.status, got, c.frozen)
		}
	}
}

// The seeded password must never be reachable as a "new" password. If this
// constant ever stops matching the seed command, both break together rather
// than the check silently passing.
func TestSeededPasswordIsKnownAndWeak(t *testing.T) {
	if service_SeededAdminPassword() != "admin123" {
		t.Fatalf("seeded password changed; update cmd/seed and the docs together")
	}
	if len(service_SeededAdminPassword()) >= 12 {
		t.Error("the seeded password is meant to be obviously temporary")
	}
}

func TestAdminActionConstantsAreDistinct(t *testing.T) {
	actions := []string{
		models.ActionFreeze, models.ActionUnfreeze, models.ActionAdjustment,
		models.ActionKYCApprove, models.ActionKYCReject,
	}
	seen := map[string]bool{}
	for _, a := range actions {
		if a == "" {
			t.Error("an audit action constant is empty")
		}
		if seen[a] {
			t.Errorf("duplicate audit action %q: the log could not tell them apart", a)
		}
		seen[a] = true
	}
}

// admin_adjustment must be a distinct ledger type: reusing an existing one
// would hide manual corrections among ordinary activity.
func TestAdjustmentIsItsOwnLedgerType(t *testing.T) {
	others := []string{
		models.TxDeposit, models.TxWithdrawal, models.TxTradeOpen,
		models.TxTradeClose, models.TxDigitOpen, models.TxDigitSettle, models.TxBonusCredit,
	}
	for _, o := range others {
		if models.TxAdminAdjustment == o {
			t.Fatalf("admin_adjustment collides with %q", o)
		}
	}
	if !strings.Contains(models.TxAdminAdjustment, "admin") {
		t.Error("the adjustment type should be recognisable as an admin action in the raw ledger")
	}
}
