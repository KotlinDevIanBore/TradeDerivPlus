//go:build integration

package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// age moves a row's clock back, so a sweep's cutoff can be reached without
// waiting out a real window.
func age(t *testing.T, h *harness, table, column string, id uuid.UUID, interval string) {
	t.Helper()
	if _, err := h.pool.Exec(h.ctx,
		"UPDATE "+table+" SET "+column+" = now() - interval '"+interval+"' WHERE id = $1", id); err != nil {
		t.Fatalf("age %s.%s: %v", table, column, err)
	}
}

// TestSweepRemovesAbandonedUploads is the orphan sweep: an upload nobody
// finished is not a record of anything, so both the object and the row go.
func TestSweepRemovesAbandonedUploads(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", int64(len(jpegBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.docs.AcceptUpload(target.ObjectKey, "image/jpeg", jpegBytes); err != nil {
		t.Fatal(err)
	}
	// Never confirmed.
	age(t, h, "kyc_upload_attempts", "created_at", target.AttemptID, "72 hours")

	sweeper := service.NewKYCRetentionService(h.kyc)
	sweeper.SweepAbandoned(h.ctx)

	if h.docs.HasForTest(target.ObjectKey) {
		t.Fatal("the abandoned object is still in the bucket")
	}
	if _, err := h.store.KYC.GetAttempt(h.ctx, target.AttemptID); err == nil {
		t.Fatal("the abandoned attempt row survived the sweep")
	}
}

// TestSweepLeavesFreshAndConfirmedUploadsAlone — the sweep must not reach an
// upload still in progress, nor one that became a submission.
func TestSweepLeavesFreshAndConfirmedUploadsAlone(t *testing.T) {
	h := newHarness(t)
	freshUser, _ := h.newUser(decimal.Zero)
	doneUser, _ := h.newUser(decimal.Zero)

	fresh, err := h.kyc.PresignUpload(h.ctx, freshUser, "image/jpeg", int64(len(jpegBytes)))
	if err != nil {
		t.Fatal(err)
	}
	if err := h.docs.AcceptUpload(fresh.ObjectKey, "image/jpeg", jpegBytes); err != nil {
		t.Fatal(err)
	}

	doneID, doneKey := uploaded(t, h, doneUser, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, doneUser, doneID, "21234567"); err != nil {
		t.Fatal(err)
	}
	// Old enough to be swept if consumption were ignored. It must not be.
	age(t, h, "kyc_upload_attempts", "created_at", doneID, "72 hours")

	service.NewKYCRetentionService(h.kyc).SweepAbandoned(h.ctx)

	if !h.docs.HasForTest(fresh.ObjectKey) {
		t.Fatal("the sweep deleted an upload that is still in progress")
	}
	if !h.docs.HasForTest(doneKey) {
		t.Fatal("the sweep deleted a confirmed submission's document")
	}
	if _, err := h.store.KYC.GetByID(h.ctx, doneID); err != nil {
		t.Fatalf("the confirmed submission is gone: %v", err)
	}
}

// TestRetentionDoesNothingAtZero is the launch-blocker guard.
//
// Zero means never delete, and zero is the default. If this ever starts deleting
// on its own, documents are being destroyed under a retention period nobody set.
func TestRetentionDoesNothingAtZero(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)

	attemptID, key := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, attemptID, true, ""); err != nil {
		t.Fatal(err)
	}
	// Reviewed years ago — far past any period anyone would choose.
	age(t, h, "kyc_submissions", "reviewed_at", attemptID, "3650 days")

	if h.cfg.KYCDocumentRetentionDays != 0 {
		t.Fatalf("the harness default is %d days; this test is about zero",
			h.cfg.KYCDocumentRetentionDays)
	}
	service.NewKYCRetentionService(h.kyc).SweepRetention(h.ctx)

	if !h.docs.HasForTest(key) {
		t.Fatal("a document was deleted while the retention period is unset")
	}
}

// TestRetentionDeletesTheDocumentAndKeepsTheDecision is the mechanism working
// once a period exists — and the property that matters most about it.
func TestRetentionDeletesTheDocumentAndKeepsTheDecision(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)

	attemptID, key := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, attemptID, true, ""); err != nil {
		t.Fatal(err)
	}
	age(t, h, "kyc_submissions", "reviewed_at", attemptID, "400 days")

	h.cfg.KYCDocumentRetentionDays = 365
	service.NewKYCRetentionService(h.kyc).SweepRetention(h.ctx)

	if h.docs.HasForTest(key) {
		t.Fatal("the document outlived the retention period")
	}

	// Everything that is a record of a decision survives.
	sub, err := h.store.KYC.GetByID(h.ctx, attemptID)
	if err != nil {
		t.Fatalf("the submission was deleted along with its document: %v", err)
	}
	if sub.DocumentKey != "" {
		t.Fatalf("document_key still points at a deleted object: %q", sub.DocumentKey)
	}
	if sub.Status != models.KYCSubmissionApproved {
		t.Fatalf("the decision changed to %q", sub.Status)
	}
	if sub.ReviewedBy == nil || *sub.ReviewedBy != adminID {
		t.Fatal("the reviewer was lost")
	}
	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil || user.KYCStatus != models.KYCVerified {
		t.Fatalf("the user's verification was lost (status %v, err %v)", user.KYCStatus, err)
	}
	entries, err := h.store.Audit.ListForUser(h.ctx, userID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("the audit trail was lost with the document")
	}
}

// TestRetentionLeavesUnreviewedSubmissionsAlone — the clock starts at the
// decision. A submission still waiting for one has not begun its retention.
func TestRetentionLeavesUnreviewedSubmissionsAlone(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	attemptID, key := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}
	// Submitted long ago but never reviewed.
	age(t, h, "kyc_submissions", "submitted_at", attemptID, "400 days")

	h.cfg.KYCDocumentRetentionDays = 1
	service.NewKYCRetentionService(h.kyc).SweepRetention(h.ctx)

	if !h.docs.HasForTest(key) {
		t.Fatal("a document awaiting review was deleted; an operator now has nothing to look at")
	}
}

// TestSweepIsSafeWithNoStore — a deployment with no bucket must not panic on a
// ticker it still runs.
func TestSweepIsSafeWithNoStore(t *testing.T) {
	h := newHarness(t)
	unconfigured := service.NewKYCRetentionService(
		service.NewKYCService(h.store, nil, h.cfg, h.log))
	unconfigured.SweepAbandoned(h.ctx)
	unconfigured.SweepRetention(h.ctx)
}

// TestClearDocumentKeyLeavesTheRowReadable guards the repository half: a cleared
// key must read back as empty rather than breaking the scan for every caller.
func TestClearDocumentKeyLeavesTheRowReadable(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}

	if err := h.store.KYC.ClearDocumentKey(h.ctx, attemptID); err != nil {
		t.Fatal(err)
	}
	sub, err := h.store.KYC.GetByID(h.ctx, attemptID)
	if err != nil {
		t.Fatalf("a row with a NULL document_key no longer scans: %v", err)
	}
	if sub.DocumentKey != "" {
		t.Fatalf("document_key %q, want empty", sub.DocumentKey)
	}
	// And it still appears in the queue an operator reads.
	page, _, err := h.store.KYC.List(h.ctx, "", 50, 0)
	if err != nil || len(page) != 1 {
		t.Fatalf("listing after a cleared key: %d rows, err %v", len(page), err)
	}
	if _, err := h.store.KYC.ListForUser(h.ctx, userID); err != nil {
		t.Fatalf("per-user listing after a cleared key: %v", err)
	}
}
