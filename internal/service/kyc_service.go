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
