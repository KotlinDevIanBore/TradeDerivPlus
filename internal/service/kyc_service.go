package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/storage"
)

// documentStore is the object store through the five methods this service needs.
//
// Declared here rather than in the storage package, following the seam style the
// rest of this codebase uses (payoutDispatcher, stkQuerier): the consumer states
// what it requires, so a test double is a few lines and the production client is
// not the only thing that can satisfy it.
type documentStore interface {
	PresignPut(ctx context.Context, key, contentType string, contentLength int64, expiry time.Duration) (*storage.PresignedRequest, error)
	PresignGet(ctx context.Context, key, disposition string, expiry time.Duration) (string, error)
	Head(ctx context.Context, key string) (*storage.ObjectMetadata, error)
	ReadHead(ctx context.Context, key string, n int64) ([]byte, error)
	Delete(ctx context.Context, key string) error
}

// DocumentStore is documentStore exported, so main can hold one before deciding
// which implementation to build. The unexported name stays the one the service
// uses, keeping the seam's shape consistent with the rest of the package.
type DocumentStore = documentStore

// Both the real client and the stub must satisfy it, checked at compile time so a
// signature change cannot quietly leave one behind.
var (
	_ documentStore = (*storage.R2Client)(nil)
	_ documentStore = (*storage.Stub)(nil)
)

// allowedDocumentTypes is an allowlist, not a denylist.
//
// The same rule the M-Pesa result classification follows and for the same reason:
// with a denylist, anything unanticipated is accepted, and "unanticipated" is
// precisely what an attacker supplies. The extension is what the object key ends
// with, so it is fixed here rather than taken from the filename.
var allowedDocumentTypes = map[string]string{
	"image/jpeg":      "jpg",
	"image/png":       "png",
	"application/pdf": "pdf",
}

// sniffBytes is how much of the stored object is read to identify it.
// http.DetectContentType looks at no more than 512 bytes, so reading more would
// be waste.
const sniffBytes = 512

// KYCService owns self-serve identity verification: issuing upload URLs,
// confirming what was uploaded, and reporting where a user stands.
//
// It does not decide whether a verified user may withdraw. That gate lives in
// WalletService and reads users.kyc_status, which only the admin review flow
// writes to 'verified'.
type KYCService struct {
	store *repository.Store
	docs  documentStore
	cfg   *config.Config
	log   *slog.Logger
}

func NewKYCService(store *repository.Store, docs documentStore, cfg *config.Config, log *slog.Logger) *KYCService {
	return &KYCService{store: store, docs: docs, cfg: cfg, log: log}
}

// UploadTarget is what a client needs to upload one document and then confirm it.
type UploadTarget struct {
	// AttemptID is minted here, embedded in the key, and becomes the submission's
	// id at confirm. The client sends it back.
	AttemptID uuid.UUID                 `json:"attempt_id"`
	ObjectKey string                    `json:"object_key"`
	Upload    *storage.PresignedRequest `json:"upload"`
	MaxBytes  int64                     `json:"max_bytes"`
	Allowed   []string                  `json:"allowed_content_types"`
}

// VerificationState is where a user stands, for a client deciding what to offer.
type VerificationState struct {
	Status string `json:"status"`
	// Unfinished is true when an upload URL was issued and never confirmed. The
	// UI offers "continue" rather than "start", so an interrupted attempt is not
	// silently forgotten.
	Unfinished bool `json:"unfinished_upload"`
}

// PresignUpload issues a one-object upload URL and records the attempt.
//
// Order matters. Every refusal that needs no network or database work comes
// first, then the state checks, then the attempt row, then the signature — so a
// caller who is not allowed to upload never causes a write, and an attempt row
// never exists for a URL that was never issued.
func (s *KYCService) PresignUpload(ctx context.Context, userID uuid.UUID, declaredType string, declaredSize int64) (*UploadTarget, error) {
	if s.docs == nil {
		// Fail closed and say it was us. A nil store means the deployment has no
		// bucket configured; offering an upload that cannot be stored would waste
		// the customer's time and leave them thinking they had verified.
		return nil, ErrKYCStorageUnavailable
	}

	ext, ok := allowedDocumentTypes[normaliseContentType(declaredType)]
	if !ok {
		return nil, invalid("content_type", "Upload a JPEG, PNG or PDF")
	}
	if declaredSize <= 0 {
		return nil, invalid("size_bytes", "That file looks empty")
	}
	if declaredSize > s.cfg.KYCMaxDocumentBytes {
		return nil, invalid("size_bytes",
			fmt.Sprintf("Documents must be under %d MB", s.cfg.KYCMaxDocumentBytes>>20))
	}

	user, err := s.store.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	// Already verified: nothing to do, and a second document would create work
	// for an operator that changes nothing.
	if user.KYCStatus == models.KYCVerified {
		return nil, ErrKYCAlreadyVerified
	}
	// One open submission per user. The same reasoning as one in-flight
	// withdrawal: a second is never what the person meant, and it doubles the
	// review queue for one decision.
	pending, err := s.store.KYC.PendingForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrKYCReviewPending
	}

	attemptID := uuid.New()
	key := documentObjectKey(userID, attemptID, ext)

	// The attempt row goes in before the URL is handed out. If this fails, no URL
	// exists and nothing was uploaded; if it succeeds and the signature then
	// fails, an unconsumed attempt with no object is swept in 48 hours. The
	// reverse order would be the one that leaks: a URL in a client's hands with
	// no record anywhere that it was issued.
	attempt := &models.KYCUploadAttempt{
		ID:          attemptID,
		UserID:      userID,
		ObjectKey:   key,
		ContentType: normaliseContentType(declaredType),
		SizeBytes:   declaredSize,
	}
	if err := s.store.KYC.CreateAttempt(ctx, attempt); err != nil {
		return nil, err
	}

	req, err := s.docs.PresignPut(ctx, key, attempt.ContentType, declaredSize, s.cfg.KYCUploadURLExpiry)
	if err != nil {
		return nil, err
	}

	s.log.Info("kyc upload url issued", "user", userID, "attempt", attemptID,
		"content_type", attempt.ContentType, "bytes", declaredSize)

	return &UploadTarget{
		AttemptID: attemptID,
		ObjectKey: key,
		Upload:    req,
		MaxBytes:  s.cfg.KYCMaxDocumentBytes,
		Allowed:   allowedContentTypeList(),
	}, nil
}

// Submit confirms that a document was uploaded and turns the attempt into a
// submission for review.
//
// Everything the browser said at presign time is re-checked here against what is
// actually in the bucket, because the browser is not a trustworthy narrator of
// its own upload. In order:
//
//  1. the attempt must exist, belong to this caller, and be unconsumed;
//  2. no submission may already be under review;
//  3. the object must be there at all;
//  4. its ACTUAL size and content type must be within the cap and on the
//     allowlist — not the declared ones;
//  5. its first bytes must actually be that type.
//
// Only then is a row written, and the row, the attempt's consumption and the
// user's status all move in one transaction.
func (s *KYCService) Submit(ctx context.Context, userID, attemptID uuid.UUID, idNumber string) (*models.KYCSubmission, error) {
	if s.docs == nil {
		return nil, ErrKYCStorageUnavailable
	}
	idNumber = strings.TrimSpace(idNumber)
	if idNumber == "" {
		return nil, invalid("id_number", "Enter your ID number")
	}
	if len(idNumber) > 30 {
		return nil, invalid("id_number", "That ID number is too long")
	}

	attempt, err := s.store.KYC.GetAttempt(ctx, attemptID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrKYCUploadNotFound
	}
	if err != nil {
		return nil, err
	}
	// Someone else's attempt is not found rather than forbidden: the caller has no
	// business knowing whether that id exists.
	if attempt.UserID != userID {
		return nil, ErrKYCUploadNotFound
	}
	// An already-consumed attempt makes a replayed confirm a no-op rather than a
	// second submission.
	if attempt.ConsumedAt != nil {
		return nil, ErrKYCUploadNotFound
	}

	pending, err := s.store.KYC.PendingForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if pending {
		return nil, ErrKYCReviewPending
	}

	meta, err := s.docs.Head(ctx, attempt.ObjectKey)
	if err != nil {
		return nil, err
	}
	// Absent means the upload never landed — the URL expired, the tab closed, the
	// network dropped. Not an error on our side, and the attempt stays open so the
	// user is offered "continue".
	if meta == nil {
		return nil, ErrKYCDocumentMissing
	}
	if meta.Size <= 0 {
		return nil, s.rejectStoredDocument(ctx, attempt, ErrKYCDocumentMissing)
	}
	// The cap again, against the real size. The signature makes this hard to
	// violate, but "hard" is not "checked": a store that ignored the signed length
	// must not be the only thing standing between us and an unbounded document.
	if meta.Size > s.cfg.KYCMaxDocumentBytes {
		return nil, s.rejectStoredDocument(ctx, attempt, invalid("size_bytes",
			fmt.Sprintf("Documents must be under %d MB", s.cfg.KYCMaxDocumentBytes>>20)))
	}
	stored := normaliseContentType(meta.ContentType)
	if _, ok := allowedDocumentTypes[stored]; !ok {
		return nil, s.rejectStoredDocument(ctx, attempt, ErrKYCDocumentRejected)
	}

	// The sniff. A label is a claim; the bytes are the fact. This is the check the
	// codebase this was modelled on does not do, and the reason an HTML file named
	// .jpg cannot become an identity document here.
	head, err := s.docs.ReadHead(ctx, attempt.ObjectKey, sniffBytes)
	if err != nil {
		return nil, err
	}
	if sniffContentType(head) != stored {
		s.log.Warn("kyc document content does not match its type",
			"user", userID, "attempt", attemptID, "declared", stored, "sniffed", sniffContentType(head))
		return nil, s.rejectStoredDocument(ctx, attempt, ErrKYCDocumentRejected)
	}

	submission := &models.KYCSubmission{
		// The attempt's id becomes the submission's, so one identifier covers the
		// whole lifecycle and the object key names the row it belongs to.
		ID:          attempt.ID,
		UserID:      userID,
		IDNumber:    idNumber,
		DocumentKey: attempt.ObjectKey,
		ContentType: stored,
		SizeBytes:   meta.Size,
	}

	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		// Guarded, and the guard decides the transaction: two confirms racing for
		// one attempt must not both produce a submission.
		won, err := st.KYC.ConsumeAttempt(ctx, attempt.ID)
		if err != nil {
			return err
		}
		if !won {
			return ErrKYCUploadNotFound
		}
		if err := st.KYC.Create(ctx, submission); err != nil {
			return err
		}
		// In the same transaction as the insert, so a user can never read as
		// pending with nothing for an operator to review, or hold a submission
		// their status does not reflect.
		return st.Users.SetKYCStatus(ctx, userID, models.KYCPending)
	})
	if err != nil {
		return nil, err
	}

	s.log.Info("kyc submitted", "user", userID, "submission", submission.ID,
		"content_type", stored, "bytes", meta.Size)
	return submission, nil
}

// rejectStoredDocument deletes an object that will never become a submission and
// returns the caller's error unchanged.
//
// The attempt is deliberately left open: the user did try, and offering them
// "continue" is better than resetting to "start" as though nothing had happened.
// Deletion is best-effort — a failure here is logged, not surfaced, because the
// user's problem is the document, not our housekeeping, and the orphan sweep will
// come back for it.
func (s *KYCService) rejectStoredDocument(ctx context.Context, attempt *models.KYCUploadAttempt, cause error) error {
	if err := s.docs.Delete(ctx, attempt.ObjectKey); err != nil {
		s.log.Error("kyc: could not delete a rejected document",
			"attempt", attempt.ID, "key", attempt.ObjectKey, "err", err)
	}
	return cause
}

// DocumentURL mints a short-lived URL for one submission's document.
//
// Called only by the admin path, which audits the view before returning it.
func (s *KYCService) DocumentURL(ctx context.Context, submission *models.KYCSubmission) (string, error) {
	if s.docs == nil {
		return "", ErrKYCStorageUnavailable
	}
	if submission.DocumentKey == "" {
		// Retention has deleted it, or it never existed. Either way there is
		// nothing to show, and saying so beats a URL that 404s.
		return "", ErrKYCDocumentMissing
	}
	// inline: a reviewer looks at the document in the browser rather than
	// accumulating identity documents in a downloads folder.
	return s.docs.PresignGet(ctx, submission.DocumentKey, "inline", s.cfg.KYCReviewURLExpiry)
}

// State reports where a user stands, including whether an upload was started and
// never finished.
func (s *KYCService) State(ctx context.Context, userID uuid.UUID) (*VerificationState, error) {
	user, err := s.store.Users.GetByID(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &VerificationState{Status: user.KYCStatus}

	// An unfinished upload is only worth offering when a new submission is
	// actually possible. While one is under review, or once verified, "continue"
	// would be a button that cannot do anything.
	if user.KYCStatus == models.KYCVerified {
		return out, nil
	}
	pending, err := s.store.KYC.PendingForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	if pending {
		return out, nil
	}
	if _, err := s.store.KYC.OpenAttempt(ctx, userID); err == nil {
		out.Unfinished = true
	} else if !errors.Is(err, repository.ErrNotFound) {
		return nil, err
	}
	return out, nil
}

// documentObjectKey is the one place a key is built.
//
// Non-deterministic by construction: the attempt's UUID is in the path, so a
// resubmission never overwrites an earlier document and a signed URL minted for
// one document can never start serving another. A neighbouring codebase uses
// deterministic keys and has exactly that problem — a re-upload silently replaces
// what a still-valid URL points at.
//
// The user id is in the path too, which makes "is this key yours?" answerable
// without a lookup, and gives the bucket a shape a human can read.
func documentObjectKey(userID, attemptID uuid.UUID, ext string) string {
	return fmt.Sprintf("kyc/%s/%s.%s", userID, attemptID, ext)
}

// normaliseContentType strips parameters and case, so "IMAGE/JPEG; charset=x"
// matches the allowlist. Without this the allowlist would be bypassable by
// sending a type that differs only in presentation.
func normaliseContentType(v string) string {
	if i := strings.IndexByte(v, ';'); i >= 0 {
		v = v[:i]
	}
	return strings.ToLower(strings.TrimSpace(v))
}

func allowedContentTypeList() []string {
	// Fixed order so the response is stable and a client can render it.
	return []string{"image/jpeg", "image/png", "application/pdf"}
}

// sniffContentType identifies bytes by their content, ignoring any label.
//
// Go's standard library covers all three allowed types (JPEG, PNG and PDF are in
// its sniffing table), so no dependency is needed for this.
func sniffContentType(b []byte) string {
	return normaliseContentType(http.DetectContentType(b))
}
