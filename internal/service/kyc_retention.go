package service

import (
	"context"
	"sync"
	"time"
)

// sweepBatch bounds one pass, so a backlog is worked through over several ticks
// rather than in one long transaction-free burst against the object store.
const sweepBatch = 100

// KYCRetentionService runs two jobs that must not be confused with each other.
//
// The orphan sweep deletes uploads nobody finished. It is always on and needs no
// permission from anyone: an upload abandoned two days ago is not a record of
// anything, and keeping it is storage cost and privacy exposure for no purpose.
//
// Document retention deletes the documents behind REVIEWED submissions. It is
// off by default and stays off until somebody supplies a lawful period, because
// how long identity documents may be kept is a legal question and engineering
// cannot answer it. Zero means never delete — see §1 of
// docs/kyc-r2-upload-plan.md, which records obtaining that number as a launch
// blocker separate from this code.
//
// They share a ticker but not a switch, deliberately. One switch would force the
// sweep that must run to wait on the decision that has not been made.
type KYCRetentionService struct {
	kyc    *KYCService
	every  time.Duration
	passMu sync.Mutex
}

func NewKYCRetentionService(kyc *KYCService) *KYCRetentionService {
	return &KYCRetentionService{kyc: kyc, every: kyc.cfg.KYCRetentionEvery}
}

// Start runs a pass now and then on the interval, in the shape the payout
// reconciler uses.
func (s *KYCRetentionService) Start(ctx context.Context) {
	go func() {
		s.tick(ctx)
		t := time.NewTicker(s.every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.tick(ctx)
			}
		}
	}()
}

func (s *KYCRetentionService) tick(ctx context.Context) {
	// Skip rather than queue: a pass still running means the previous one has
	// work left, and two concurrent sweeps would race each other's deletes.
	if !s.passMu.TryLock() {
		s.kyc.log.Warn("kyc sweep: previous pass still running, skipping this tick")
		return
	}
	defer s.passMu.Unlock()
	s.SweepAbandoned(ctx)
	s.SweepRetention(ctx)
}

// SweepAbandoned deletes uploads that were started and never confirmed.
//
// Exported so a test drives it directly rather than waiting out a ticker.
func (s *KYCRetentionService) SweepAbandoned(ctx context.Context) {
	if s.kyc.docs == nil {
		return
	}
	cutoff := time.Now().Add(-s.kyc.cfg.KYCAbandonedUploadAfter)
	attempts, err := s.kyc.store.KYC.ListAbandonedAttempts(ctx, cutoff, sweepBatch)
	if err != nil {
		s.kyc.log.Error("kyc sweep: could not list abandoned uploads", "err", err)
		return
	}
	for _, a := range attempts {
		// The object goes first. If this fails the row stays, so the next pass
		// tries again; the reverse order would lose the only pointer to the
		// object and leak it silently.
		if err := s.kyc.docs.Delete(ctx, a.ObjectKey); err != nil {
			s.kyc.log.Error("kyc sweep: could not delete an abandoned document",
				"attempt", a.ID, "err", err)
			continue
		}
		if err := s.kyc.store.KYC.DeleteAttempt(ctx, a.ID); err != nil {
			s.kyc.log.Error("kyc sweep: could not delete an abandoned attempt",
				"attempt", a.ID, "err", err)
			continue
		}
	}
	if len(attempts) > 0 {
		s.kyc.log.Info("kyc sweep: removed abandoned uploads", "count", len(attempts))
	}
}

// SweepRetention deletes the documents behind submissions reviewed longer ago
// than the configured period, and does nothing at all while that period is zero.
//
// It clears document_key and leaves everything else: the submission, its
// decision, who made it, and the audit trail all survive. Retention applies to
// the document, never to the record that a decision was made.
func (s *KYCRetentionService) SweepRetention(ctx context.Context) {
	days := s.kyc.cfg.KYCDocumentRetentionDays
	if days <= 0 || s.kyc.docs == nil {
		// Unconfigured. Nothing is deleted, and that is the documented default:
		// the period is a compliance decision nobody has made yet.
		return
	}
	cutoff := time.Now().AddDate(0, 0, -days)
	subs, err := s.kyc.store.KYC.ListDocumentsForRetention(ctx, cutoff, sweepBatch)
	if err != nil {
		s.kyc.log.Error("kyc retention: could not list documents", "err", err)
		return
	}
	for _, sub := range subs {
		if err := s.kyc.docs.Delete(ctx, sub.DocumentKey); err != nil {
			s.kyc.log.Error("kyc retention: could not delete a document",
				"submission", sub.ID, "err", err)
			continue
		}
		if err := s.kyc.store.KYC.ClearDocumentKey(ctx, sub.ID); err != nil {
			s.kyc.log.Error("kyc retention: could not clear a document key",
				"submission", sub.ID, "err", err)
			continue
		}
	}
	if len(subs) > 0 {
		s.kyc.log.Info("kyc retention: removed documents past the retention period",
			"count", len(subs), "days", days)
	}
}
