package service

import (
	"strings"
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// The collision that makes a separate table necessary, asserted rather than
// described: the same code must not produce the same sentence on both sides.
func TestPayoutCopyDoesNotReuseTheDepositVocabulary(t *testing.T) {
	for _, code := range []string{"1", "2001"} {
		numeric := map[string]int{"1": 1, "2001": 2001}[code]
		deposit := humanResult(numeric, "")
		payout := humanPayoutResult(code, "")
		if deposit == payout {
			t.Fatalf("code %s renders identically on both paths: %q", code, payout)
		}
	}

	// Code 1 is the clearest case: the customer's balance on a deposit, ours on a
	// payout. The payout copy must not blame theirs.
	if p := humanPayoutResult("1", ""); strings.Contains(strings.ToLower(p), "your m-pesa balance") {
		t.Fatalf("code 1 on a payout blames the customer's balance: %q", p)
	}
	// And 2001 must not mention a PIN, which is what it means on a deposit.
	if p := humanPayoutResult("2001", ""); strings.Contains(strings.ToLower(p), "pin") {
		t.Fatalf("code 2001 on a payout mentions a PIN the customer was never asked for: %q", p)
	}
}

// Every code the client will call terminal needs a sentence, and every sentence
// has to be safe to show. The fallback covers codes nobody has written copy for,
// which is why the allowlist never waits on this file.
func TestEveryTerminalPayoutCodeHasSafeCopy(t *testing.T) {
	for code := range mpesaclient.PayoutFailureCodes {
		msg := humanPayoutResult(code, "Daraja's own operator-facing words")
		if msg == "" {
			t.Errorf("code %s has no copy", code)
			continue
		}
		if strings.Contains(msg, "Daraja") || strings.Contains(msg, "DebitParty") || strings.Contains(msg, "operator-facing") {
			t.Errorf("code %s leaks Daraja's wording to the customer: %q", code, msg)
		}
		// Every failure means the money did not leave, so no sentence may suggest
		// it might have.
		if strings.Contains(strings.ToLower(msg), "may have been sent") {
			t.Errorf("code %s is ambiguous about whether money moved: %q", code, msg)
		}
	}

	// A code with no entry still gets something true.
	fallback := humanPayoutResult("9999", "")
	if fallback == "" || !strings.Contains(strings.ToLower(fallback), "balance") {
		t.Fatalf("the fallback does not reassure about the balance: %q", fallback)
	}
}

// The copy says "nothing was taken" only where that is our failure to own; a
// customer-caused failure should not apologise for us.
func TestPayoutCopySplitsFaultCorrectly(t *testing.T) {
	ours := humanPayoutResult("1", "")
	if !strings.Contains(strings.ToLower(ours), "we're on it") {
		t.Fatalf("a failure that is ours does not say so: %q", ours)
	}
	theirs := humanPayoutResult("2040", "")
	if strings.Contains(strings.ToLower(theirs), "we're on it") {
		t.Fatalf("a wrong-number failure implies we are fixing something: %q", theirs)
	}
	if !payoutFailureIsOurs("1") || payoutFailureIsOurs("2040") {
		t.Fatal("payoutFailureIsOurs disagrees with the client's list")
	}
}
