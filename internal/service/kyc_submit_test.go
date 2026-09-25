//go:build integration

package service_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

var (
	pngBytes  = append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, make([]byte, 64)...)
	pdfBytes  = append([]byte("%PDF-1.7\n1 0 obj\n"), make([]byte, 64)...)
	htmlBytes = []byte("<html><body>not an identity document at all</body></html>")
)

// uploaded walks presign then the browser's PUT, returning the attempt id and key.
func uploaded(t *testing.T, h *harness, userID uuid.UUID, contentType string, body []byte) (uuid.UUID, string) {
	t.Helper()
	target, err := h.kyc.PresignUpload(h.ctx, userID, contentType, int64(len(body)))
	if err != nil {
		t.Fatalf("presign: %v", err)
	}
	if err := h.docs.AcceptUpload(target.ObjectKey, contentType, body); err != nil {
		t.Fatalf("upload: %v", err)
	}
	return target.AttemptID, target.ObjectKey
}

func TestSubmitCreatesTheSubmissionAndMovesTheStatus(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, key := uploaded(t, h, userID, "image/jpeg", jpegBytes)

	sub, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	// One identifier across the lifecycle: the attempt's id is the submission's,
	// so the object key names the row it belongs to.
	if sub.ID != attemptID {
		t.Fatalf("submission id %s does not reuse the attempt id %s", sub.ID, attemptID)
	}
	if sub.DocumentKey != key {
		t.Fatalf("submission key %q, want %q", sub.DocumentKey, key)
	}
	// The ACTUAL size and type are recorded, not what was declared.
	if sub.SizeBytes != int64(len(jpegBytes)) || sub.ContentType != "image/jpeg" {
		t.Fatalf("recorded %d bytes of %q", sub.SizeBytes, sub.ContentType)
	}
	if sub.Status != models.KYCSubmissionPending {
		t.Fatalf("status %q, want pending", sub.Status)
	}

	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.KYCStatus != models.KYCPending {
		t.Fatalf("kyc_status %q after confirm, want pending", user.KYCStatus)
	}

	// The attempt is spent, so it is no longer offered as unfinished.
	attempt, err := h.store.KYC.GetAttempt(h.ctx, attemptID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.ConsumedAt == nil {
		t.Fatal("the attempt was not consumed")
	}
}

func TestSubmitAcceptsAllThreeAllowedTypes(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		body        []byte
	}{
		{"image/jpeg", jpegBytes},
		{"image/png", pngBytes},
		{"application/pdf", pdfBytes},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			h := newHarness(t)
			userID, _ := h.newUser(decimal.Zero)
			attemptID, _ := uploaded(t, h, userID, tc.contentType, tc.body)
			if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
				t.Fatalf("submit: %v", err)
			}
		})
	}
}

// TestSubmitRefusesAMislabelledDocument is the sniff, and the most important test
// in this stage.
//
// An HTML file uploaded as image/jpeg satisfies the signed Content-Type — the
// signature covers the header, not the bytes — so without the sniff it would
// become an identity document and be served to a reviewer as an image.
func TestSubmitRefusesAMislabelledDocument(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", int64(len(htmlBytes)))
	if err != nil {
		t.Fatal(err)
	}
	// The store records image/jpeg, as the signature demanded; the bytes are HTML.
	h.docs.PutRawForTest(target.ObjectKey, "image/jpeg", htmlBytes)

	_, err = h.kyc.Submit(h.ctx, userID, target.AttemptID, "21234567")
	if !errors.Is(err, service.ErrKYCDocumentRejected) {
		t.Fatalf("a mislabelled document returned %v, want ErrKYCDocumentRejected", err)
	}

	// Nothing became a submission, the status did not move, and the object was
	// cleaned up rather than left in the bucket.
	if pending, _ := h.store.KYC.PendingForUser(h.ctx, userID); pending {
		t.Fatal("a rejected document produced a pending submission")
	}
	user, _ := h.store.Users.GetByID(h.ctx, userID)
	if user.KYCStatus != models.KYCUnverified {
		t.Fatalf("kyc_status moved to %q on a rejected document", user.KYCStatus)
	}
	if h.docs.HasForTest(target.ObjectKey) {
		t.Fatal("the rejected object was left in the bucket")
	}
	// The attempt stays open: the user did try, and "continue" beats silently
	// resetting to "start".
	if _, err := h.store.KYC.OpenAttempt(h.ctx, userID); err != nil {
		t.Fatalf("the attempt was closed after a rejection: %v", err)
	}
}

// TestSubmitRefusesAPngRenamedAsPdf — the sniff must compare against the stored
// type, not merely check that the bytes are *some* allowed type.
func TestSubmitRefusesAPngRenamedAsPdf(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "application/pdf", int64(len(pngBytes)))
	if err != nil {
		t.Fatal(err)
	}
	h.docs.PutRawForTest(target.ObjectKey, "application/pdf", pngBytes)

	if _, err := h.kyc.Submit(h.ctx, userID, target.AttemptID, "21234567"); !errors.Is(err, service.ErrKYCDocumentRejected) {
		t.Fatalf("a PNG stored as PDF returned %v, want ErrKYCDocumentRejected", err)
	}
}

// TestSubmitRefusesWhenTheObjectIsNotThere — the upload never landed. Not our
// fault, and the attempt stays open so the user can continue.
func TestSubmitRefusesWhenTheObjectIsNotThere(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", 1024)
	if err != nil {
		t.Fatal(err)
	}
	// No PUT at all.
	if _, err := h.kyc.Submit(h.ctx, userID, target.AttemptID, "21234567"); !errors.Is(err, service.ErrKYCDocumentMissing) {
		t.Fatalf("got %v, want ErrKYCDocumentMissing", err)
	}
	if _, err := h.store.KYC.OpenAttempt(h.ctx, userID); err != nil {
		t.Fatalf("the attempt was closed: %v", err)
	}
}

// TestSubmitRefusesAnOversizeObject checks the cap against what is really stored.
// The signature makes this hard to reach, but "hard" is not "checked".
func TestSubmitRefusesAnOversizeObject(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)

	target, err := h.kyc.PresignUpload(h.ctx, userID, "image/jpeg", 1024)
	if err != nil {
		t.Fatal(err)
	}
	big := append(append([]byte{}, jpegBytes...), make([]byte, h.cfg.KYCMaxDocumentBytes+1)...)
	h.docs.PutRawForTest(target.ObjectKey, "image/jpeg", big)

	if _, err := h.kyc.Submit(h.ctx, userID, target.AttemptID, "21234567"); err == nil {
		t.Fatal("an oversize stored object was accepted")
	}
	if h.docs.HasForTest(target.ObjectKey) {
		t.Fatal("the oversize object was left in the bucket")
	}
}

// TestSubmitRefusesAnotherUsersAttempt — the key embeds a user id, and an attempt
// belongs to exactly one person.
func TestSubmitRefusesAnotherUsersAttempt(t *testing.T) {
	h := newHarness(t)
	ownerID, _ := h.newUser(decimal.Zero)
	intruderID, _ := h.newUser(decimal.Zero)

	attemptID, _ := uploaded(t, h, ownerID, "image/jpeg", jpegBytes)

	// Not-found rather than forbidden: the caller has no business learning
	// whether that id exists.
	if _, err := h.kyc.Submit(h.ctx, intruderID, attemptID, "21234567"); !errors.Is(err, service.ErrKYCUploadNotFound) {
		t.Fatalf("got %v, want ErrKYCUploadNotFound", err)
	}
	if pending, _ := h.store.KYC.PendingForUser(h.ctx, intruderID); pending {
		t.Fatal("submitting someone else's attempt created a submission")
	}
}

// TestSubmitIsNotReplayable — a confirmed attempt is spent, so a repeated request
// cannot produce a second submission.
func TestSubmitIsNotReplayable(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)

	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}
	_, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567")
	if err == nil {
		t.Fatal("a confirmed attempt was confirmed a second time")
	}

	page, _, err := h.store.KYC.List(h.ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1 {
		t.Fatalf("%d submissions exist after a replayed confirm, want 1", len(page))
	}
}

func TestSubmitRequiresAnIDNumber(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)

	for _, id := range []string{"", "   "} {
		if _, err := h.kyc.Submit(h.ctx, userID, attemptID, id); err == nil {
			t.Fatalf("id number %q was accepted", id)
		}
	}
	// Longer than the column allows must be refused here rather than by the
	// database, so the user gets a message instead of a 500.
	long := make([]byte, 40)
	for i := range long {
		long[i] = '1'
	}
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, string(long)); err == nil {
		t.Fatal("an over-long id number was accepted")
	}
}

// TestSubmitAfterRejectionStartsAFreshSubmission is the resubmission rule end to
// end: a rejected user uploads again, gets a new key, and lands back in the queue.
func TestSubmitAfterRejectionStartsAFreshSubmission(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)

	firstID, firstKey := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, firstID, "21234567"); err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, firstID, false, "the document is too blurry to read"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	user, _ := h.store.Users.GetByID(h.ctx, userID)
	if user.KYCStatus != models.KYCRejected {
		t.Fatalf("status %q after rejection", user.KYCStatus)
	}

	secondID, secondKey := uploaded(t, h, userID, "image/png", pngBytes)
	if secondKey == firstKey {
		t.Fatal("the second attempt reused the first key: a resubmission would overwrite")
	}
	if _, err := h.kyc.Submit(h.ctx, userID, secondID, "21234567"); err != nil {
		t.Fatalf("resubmission refused: %v", err)
	}
	user, _ = h.store.Users.GetByID(h.ctx, userID)
	if user.KYCStatus != models.KYCPending {
		t.Fatalf("status %q after resubmitting, want pending", user.KYCStatus)
	}
	// Both documents survive: the rejected one is evidence of a decision until
	// retention removes it.
	if !h.docs.HasForTest(firstKey) || !h.docs.HasForTest(secondKey) {
		t.Fatal("a resubmission destroyed an earlier document")
	}
}

// --- the admin document view ---------------------------------------------

// TestDocumentViewIsAudited is the D12 guarantee: no URL without a record of it.
func TestDocumentViewIsAudited(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}

	url, err := h.admin.KYCDocumentURL(h.ctx, adminID, attemptID)
	if err != nil {
		t.Fatalf("document url: %v", err)
	}
	if url == "" {
		t.Fatal("no URL returned")
	}

	entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionKYCDocumentView, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d audit rows for a document view, want 1", len(entries))
	}
	if entries[0].AdminID != adminID {
		t.Fatalf("audit row names admin %s, want %s", entries[0].AdminID, adminID)
	}
	if entries[0].TargetID != userID {
		t.Fatalf("audit row targets %s, want the user %s", entries[0].TargetID, userID)
	}

	// A second view is a second row: the question "who looked at this, and when"
	// must not collapse to one answer.
	if _, err := h.admin.KYCDocumentURL(h.ctx, adminID, attemptID); err != nil {
		t.Fatal(err)
	}
	entries, _, err = h.store.Audit.List(h.ctx, nil, models.ActionKYCDocumentView, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("%d audit rows after two views, want 2", len(entries))
	}
}

func TestDocumentViewRefusesAnUnknownSubmission(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()

	if _, err := h.admin.KYCDocumentURL(h.ctx, adminID, uuid.New()); !errors.Is(err, service.ErrNotFound) {
		t.Fatalf("got %v, want ErrNotFound", err)
	}
	entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionKYCDocumentView, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("a refused view wrote %d audit rows", len(entries))
	}
}

// TestDocumentViewRefusesWhenTheKeyIsGone — after retention there is nothing to
// show, and saying so beats a URL that 404s.
func TestDocumentViewRefusesWhenTheKeyIsGone(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}
	if err := h.store.KYC.ClearDocumentKey(h.ctx, attemptID); err != nil {
		t.Fatal(err)
	}

	if _, err := h.admin.KYCDocumentURL(h.ctx, adminID, attemptID); !errors.Is(err, service.ErrKYCDocumentMissing) {
		t.Fatalf("got %v, want ErrKYCDocumentMissing", err)
	}
}

// TestDocumentViewFailsClosedWithNoStore — an unwired console must refuse rather
// than reach into the bucket some other way.
func TestDocumentViewFailsClosedWithNoStore(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}

	// A fresh AdminService with no documents seam wired.
	unwired := service.NewAdminService(h.store, noopBroadcaster{}, h.cfg, h.log)
	if _, err := unwired.KYCDocumentURL(h.ctx, adminID, attemptID); !errors.Is(err, service.ErrKYCStorageUnavailable) {
		t.Fatalf("got %v, want ErrKYCStorageUnavailable", err)
	}
}

// TestConcurrentSubmitsProduceExactlyOneSubmission exercises the guard that the
// sequential replay test cannot see.
//
// Submit prechecks attempt.ConsumedAt, which refuses an obviously-replayed
// confirm — but a precheck is a read, and two requests can both pass it before
// either writes. The guarded ConsumeAttempt inside the transaction is what makes
// "one attempt, one submission" true under a race rather than merely likely.
//
// Written because removing that guard left the replay test passing: the precheck
// was covering for it, so the test could not tell a working guard from an absent
// one.
//
// It should be read for what it proves and what it does not. It proves the
// end-to-end property — four simultaneous confirms yield exactly one submission.
// It does NOT isolate the guard: with the guard removed this still passes, because
// the two prechecks and the submission's primary key (the attempt's id, reused)
// each also refuse a duplicate. The guard's own contract is pinned directly at the
// repository instead, in TestConsumeAttemptIsSingleUse.
func TestConcurrentSubmitsProduceExactlyOneSubmission(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)

	const racers = 4
	var wg sync.WaitGroup
	errs := make([]error, racers)
	start := make(chan struct{})
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = h.kyc.Submit(h.ctx, userID, attemptID, "21234567")
		}(i)
	}
	close(start)
	wg.Wait()

	won := 0
	for _, err := range errs {
		if err == nil {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("%d of %d concurrent confirms succeeded, want exactly 1 (errors: %v)", won, racers, errs)
	}

	page, total, err := h.store.KYC.List(h.ctx, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(page) != 1 {
		t.Fatalf("%d submissions exist after a concurrent confirm, want 1", total)
	}

	// And the user is pending exactly once, not left in some half-moved state.
	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.KYCStatus != models.KYCPending {
		t.Fatalf("kyc_status %q, want pending", user.KYCStatus)
	}
}

// TestDocumentViewWithholdsTheURLWhenTheAuditFails is the ordering guarantee
// itself: no document URL is handed out without a record of it.
//
// The audit failure is forced through admin_audit_log's foreign key on admin_id —
// an unknown admin cannot be audited, so the transaction fails after the URL has
// already been signed. That is precisely the window the ordering exists to close,
// and the only way to reach it without inventing a seam.
func TestDocumentViewWithholdsTheURLWhenTheAuditFails(t *testing.T) {
	h := newHarness(t)
	userID, _ := h.newUser(decimal.Zero)
	attemptID, _ := uploaded(t, h, userID, "image/jpeg", jpegBytes)
	if _, err := h.kyc.Submit(h.ctx, userID, attemptID, "21234567"); err != nil {
		t.Fatal(err)
	}

	// No such admin, so the audit row cannot be written.
	url, err := h.admin.KYCDocumentURL(h.ctx, uuid.New(), attemptID)
	if err == nil {
		t.Fatal("a document URL was returned although the view could not be audited")
	}
	if url != "" {
		t.Fatalf("a URL leaked alongside the error: %q", url)
	}

	if entries, _, err := h.store.Audit.List(h.ctx, nil, models.ActionKYCDocumentView, 50, 0); err != nil {
		t.Fatal(err)
	} else if len(entries) != 0 {
		t.Fatalf("%d audit rows were written by a failed view", len(entries))
	}
}
