//go:build integration

package service_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// jpegBytes is a minimal JPEG header plus padding — enough for content sniffing
// to identify it as image/jpeg.
var jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F'},
	make([]byte, 64)...)

// seedPendingSubmission puts a submission in the review queue without going
// through the confirm endpoint, so a presign test can set up "already under
// review" without depending on Stage 4's code.
func seedPendingSubmission(t *testing.T, h *harness, userID uuid.UUID) {
	t.Helper()
	id := uuid.New()
	sub := &models.KYCSubmission{
		ID:          id,
		UserID:      userID,
		IDNumber:    "21234567",
		DocumentKey: "kyc/" + userID.String() + "/" + id.String() + ".jpg",
		ContentType: "image/jpeg",
		SizeBytes:   int64(len(jpegBytes)),
	}
	if err := h.store.KYC.Create(h.ctx, sub); err != nil {
		t.Fatalf("seed submission: %v", err)
	}
	if err := h.store.Users.SetKYCStatus(h.ctx, userID, models.KYCPending); err != nil {
		t.Fatalf("seed status: %v", err)
	}
}

func TestPresignIssuesAUrlAndRecordsTheAttempt(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", int64(len(jpegBytes)))
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if target.Upload == nil || target.Upload.URL == "" {
		t.Fatal("no upload URL returned")
	}
	if target.Upload.Method != "PUT" {
		t.Fatalf("method %q, want PUT", target.Upload.Method)
	}

	// The key is non-deterministic and scoped to the user: a resubmission can
	// never overwrite an earlier document, and "is this key yours?" is answerable
	// from the key itself without a lookup.
	if !strings.HasPrefix(target.ObjectKey, "kyc/"+userID.String()+"/") {
		t.Fatalf("key %q is not scoped to the user", target.ObjectKey)
	}
	if !strings.HasSuffix(target.ObjectKey, ".jpg") {
		t.Fatalf("key %q does not carry the type's extension", target.ObjectKey)
	}
	if !strings.Contains(target.ObjectKey, target.AttemptID.String()) {
		t.Fatalf("key %q does not embed the attempt id", target.ObjectKey)
	}

	// The attempt row is what makes an abandoned upload discoverable. Without it
	// the object would be invisible to both the user and the orphan sweep.
	attempt, err := h.store.KYC.GetAttempt(h.ctx, target.AttemptID)
	if err != nil {
		t.Fatalf("the attempt was not recorded: %v", err)
	}
	if attempt.UserID != userID {
		t.Fatalf("attempt belongs to %s, want %s", attempt.UserID, userID)
	}
	if attempt.ObjectKey != target.ObjectKey {
		t.Fatalf("attempt key %q does not match the signed key %q", attempt.ObjectKey, target.ObjectKey)
	}
	if attempt.ConsumedAt != nil {
		t.Fatal("a fresh attempt is already marked consumed")
	}

	// No submission and no status change yet: that is confirm's job, and the
	// review queue must not see an upload nobody finished.
	if pending, err := h.store.KYC.PendingForUser(h.ctx, userID); err != nil || pending {
		t.Fatalf("presign created a pending submission (pending=%v err=%v)", pending, err)
	}
	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.KYCStatus != models.KYCUnverified {
		t.Fatalf("kyc_status moved to %q at presign; only confirm may change it", user.KYCStatus)
	}
}

// TestPresignRefusesDisallowedTypes is the allowlist doing its job. A denylist
// would accept anything unanticipated, which is exactly what would be sent.
func TestPresignRefusesDisallowedTypes(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	for _, ct := range []string{
		"text/html", "image/gif", "image/svg+xml", "application/zip",
		"application/octet-stream", "", "image/jpeg\x00",
	} {
		if _, err := h.kyc.PresignUpload(h.ctx, userID, ct, 1024); err == nil {
			t.Fatalf("content type %q was accepted", ct)
		}
	}

	// And the three that are allowed, including one carrying a parameter and odd
	// case — normalisation must not itself become a way past the allowlist.
	for _, ct := range []string{"image/jpeg", "image/png", "application/pdf", "IMAGE/JPEG; charset=binary"} {
		if _, err := h.kyc.PresignUpload(h.ctx, userID, ct, 1024); err != nil {
			t.Fatalf("content type %q was refused: %v", ct, err)
		}
	}
}

func TestPresignRefusesBadSizes(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	for _, size := range []int64{0, -1, h.cfg.KYCMaxDocumentBytes + 1} {
		if _, err := h.kyc.PresignUpload(h.ctx, userID, "image/png", size); err == nil {
			t.Fatalf("size %d was accepted", size)
		}
	}
	// Exactly at the cap is allowed: the limit is inclusive, and a document of
	// exactly the stated maximum should not fall to an off-by-one.
	if _, err := h.kyc.PresignUpload(h.ctx, userID, "image/png", h.cfg.KYCMaxDocumentBytes); err != nil {
		t.Fatalf("a document of exactly the cap was refused: %v", err)
	}
}

// TestPresignRefusedWhileAReviewIsPending — one open submission per user, so an
// operator decides once rather than working through duplicates.
func TestPresignRefusedWhileAReviewIsPending(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	seedPendingSubmission(t, h, userID)

	_, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", 1024)
	if !errors.Is(err, service.ErrKYCReviewPending) {
		t.Fatalf("a second upload while one is under review returned %v, want ErrKYCReviewPending", err)
	}
}

// TestPresignRefusedOnceVerified — a verified user has nothing to prove, and a
// further document would be review work that changes nothing.
func TestPresignRefusedOnceVerified(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	if err := h.store.Users.SetKYCStatus(h.ctx, userID, models.KYCVerified); err != nil {
		t.Fatal(err)
	}
	_, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", 1024)
	if !errors.Is(err, service.ErrKYCAlreadyVerified) {
		t.Fatalf("got %v, want ErrKYCAlreadyVerified", err)
	}
}

// TestPresignAllowsAnotherAttemptAfterRejection is the resubmission rule: a
// rejected user is not at a dead end.
func TestPresignAllowsAnotherAttemptAfterRejection(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	if err := h.store.Users.SetKYCStatus(h.ctx, userID, models.KYCRejected); err != nil {
		t.Fatal(err)
	}
	if _, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", 1024); err != nil {
		t.Fatalf("a rejected user could not start again: %v", err)
	}
}

// TestPresignFailsClosedWithNoStore is the D14 behaviour: a deployment with no
// bucket refuses rather than pretending, and says it was us.
//
// Fail-closed matters here the way it does for withdrawals — the outcome a user
// must never get is one that looks like success.
func TestPresignFailsClosedWithNoStore(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	unconfigured := service.NewKYCService(h.store, nil, h.cfg, h.log)
	_, err := unconfigured.PresignUpload(h.ctx, userID, "image/jpeg", 1024)
	if !errors.Is(err, service.ErrKYCStorageUnavailable) {
		t.Fatalf("got %v, want ErrKYCStorageUnavailable", err)
	}

	// And nothing was written: a refused request must not leave an attempt behind
	// for the sweep to clean up or the UI to offer as unfinished.
	if _, err := h.store.KYC.OpenAttempt(h.ctx, userID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatalf("a refused presign left an attempt row: %v", err)
	}
}

// TestStateReportsAnUnfinishedUpload pins the product behaviour the attempt row
// exists for: an interrupted upload is offered back rather than forgotten.
func TestStateReportsAnUnfinishedUpload(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	state, err := h.kyc.State(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != models.KYCUnverified || state.Unfinished {
		t.Fatalf("a fresh user reads %+v, want unverified with nothing unfinished", state)
	}

	if _, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", int64(len(jpegBytes))); err != nil {
		t.Fatal(err)
	}
	state, err = h.kyc.State(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Unfinished {
		t.Fatal("an issued-but-unconfirmed upload does not read as unfinished")
	}
}

// TestStateDoesNotOfferContinueWhileUnderReview — "continue" must not appear when
// pressing it could do nothing. A pending submission blocks a new attempt, so
// offering to resume one would be a dead control.
func TestStateDoesNotOfferContinueWhileUnderReview(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	if _, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", int64(len(jpegBytes))); err != nil {
		t.Fatal(err)
	}
	seedPendingSubmission(t, h, userID)

	state, err := h.kyc.State(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Unfinished {
		t.Fatal("continue is offered while a submission is under review")
	}
	if state.Status != models.KYCPending {
		t.Fatalf("status %q, want pending", state.Status)
	}
}
