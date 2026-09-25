//go:build integration

package service_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

// These are repository-level tests, kept beside the service's because that is
// where the database harness lives. Their subject is KYCRepo's guards.

// TestConsumeAttemptIsSingleUse pins the guard KYCService.Submit relies on.
//
// It is tested here at the repository rather than through Submit because the
// service cannot isolate it: Submit prechecks both the attempt's consumption and
// any pending submission, and the submission's primary key is the attempt's id
// reused -- so a duplicate confirm is refused by any of three layers, and removing
// this guard leaves every service-level test passing. That does not make the guard
// pointless; it makes it the innermost of several, and the one whose contract has
// to be stated somewhere it can actually be observed.
func TestConsumeAttemptIsSingleUse(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	attempt := &models.KYCUploadAttempt{
		ID:          uuid.New(),
		UserID:      userID,
		ObjectKey:   "kyc/" + userID.String() + "/once.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   1024,
	}
	if err := h.store.KYC.CreateAttempt(h.ctx, attempt); err != nil {
		t.Fatalf("create attempt: %v", err)
	}

	won, err := h.store.KYC.ConsumeAttempt(h.ctx, attempt.ID)
	if err != nil {
		t.Fatalf("first consume: %v", err)
	}
	if !won {
		t.Fatal("the first consume did not win")
	}

	// The second must lose rather than silently succeed: whoever loses owes the
	// caller a refusal, and a guard reporting success twice would let two
	// submissions come from one upload.
	won, err = h.store.KYC.ConsumeAttempt(h.ctx, attempt.ID)
	if err != nil {
		t.Fatalf("second consume: %v", err)
	}
	if won {
		t.Fatal("the second consume also won: the attempt is not single-use")
	}

	// An attempt that does not exist is not a win either.
	won, err = h.store.KYC.ConsumeAttempt(h.ctx, uuid.New())
	if err != nil {
		t.Fatalf("consume of an unknown attempt: %v", err)
	}
	if won {
		t.Fatal("consuming an attempt that does not exist reported a win")
	}
}

// TestOpenAttemptIgnoresConsumedOnes -- "continue" must not be offered for an
// upload that already became a submission.
func TestOpenAttemptIgnoresConsumedOnes(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	older := &models.KYCUploadAttempt{
		ID: uuid.New(), UserID: userID, ObjectKey: "kyc/a.jpg",
		ContentType: "image/jpeg", SizeBytes: 10,
	}
	newer := &models.KYCUploadAttempt{
		ID: uuid.New(), UserID: userID, ObjectKey: "kyc/b.jpg",
		ContentType: "image/png", SizeBytes: 20,
	}
	for _, a := range []*models.KYCUploadAttempt{older, newer} {
		if err := h.store.KYC.CreateAttempt(h.ctx, a); err != nil {
			t.Fatal(err)
		}
	}

	got, err := h.store.KYC.OpenAttempt(h.ctx, userID)
	if err != nil {
		t.Fatalf("open attempt: %v", err)
	}
	if got.ID != newer.ID {
		t.Fatalf("returned %s, want the most recent attempt %s", got.ID, newer.ID)
	}

	if _, err := h.store.KYC.ConsumeAttempt(h.ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	got, err = h.store.KYC.OpenAttempt(h.ctx, userID)
	if err != nil {
		t.Fatalf("open attempt after consuming the newer: %v", err)
	}
	if got.ID != older.ID {
		t.Fatalf("returned %s, want the still-open %s", got.ID, older.ID)
	}
}

// TestListAbandonedAttemptsOnlyReturnsOldOpenOnes is the orphan sweep's query. A
// consumed attempt records a real submission and must never be swept.
func TestListAbandonedAttemptsOnlyReturnsOldOpenOnes(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	fresh := &models.KYCUploadAttempt{
		ID: uuid.New(), UserID: userID, ObjectKey: "kyc/fresh.jpg",
		ContentType: "image/jpeg", SizeBytes: 10,
	}
	stale := &models.KYCUploadAttempt{
		ID: uuid.New(), UserID: userID, ObjectKey: "kyc/stale.jpg",
		ContentType: "image/jpeg", SizeBytes: 10,
	}
	consumed := &models.KYCUploadAttempt{
		ID: uuid.New(), UserID: userID, ObjectKey: "kyc/consumed.jpg",
		ContentType: "image/jpeg", SizeBytes: 10,
	}
	for _, a := range []*models.KYCUploadAttempt{fresh, stale, consumed} {
		if err := h.store.KYC.CreateAttempt(h.ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []uuid.UUID{stale.ID, consumed.ID} {
		if _, err := h.pool.Exec(h.ctx,
			`UPDATE kyc_upload_attempts SET created_at = now() - interval '72 hours' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.store.KYC.ConsumeAttempt(h.ctx, consumed.ID); err != nil {
		t.Fatal(err)
	}

	got, err := h.store.KYC.ListAbandonedAttempts(h.ctx, time.Now().Add(-48*time.Hour), 100)
	if err != nil {
		t.Fatalf("list abandoned: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("%d abandoned attempts, want 1 (%+v)", len(got), got)
	}
	if got[0].ID != stale.ID {
		t.Fatalf("returned %s, want the stale open attempt %s", got[0].ID, stale.ID)
	}
}
