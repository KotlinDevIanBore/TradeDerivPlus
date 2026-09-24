package service

import (
	"context"
	"errors"
	"strconv"

	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// The automated-trading half of the admin console.
//
// Kept apart from admin_service.go because everything there is about one user's
// row and this is about the platform: the kill switch stops every live run at
// once, which is the most consequential single action the console offers.

// botStopper is the in-process half of the kill switch. An interface so
// AdminService does not depend on BotService's construction order in main.
type botStopper interface {
	StopAll(reason string) int
	SignalStop(ctx context.Context, runID uuid.UUID, reason string)
}

// SetBotStopper wires the running BotService, the way SetConnectionCounter wires
// the hub. Without it the kill switch still works — the rows are updated and
// every loop notices on its next check — but the stop takes up to one iteration
// instead of being immediate.
func (s *AdminService) SetBotStopper(b botStopper) { s.bots = b }

// SetSettingsCache lets the kill switch invalidate the flag cache, so "off" is
// true immediately rather than after the cache's TTL.
func (s *AdminService) SetSettingsCache(c interface{ BustCache() }) { s.settingsCache = c }

// KillBots stops every live run and turns automated trading off.
//
// Both halves commit together: the flag write, the run update and the audit row
// are one transaction, so the console cannot end up having stopped the runs
// without recording who did it or why — and cannot turn the feature off without
// stopping what is already running.
//
// The flag is part of it deliberately. Stopping the runs alone would leave users
// free to start new ones the moment the console's back was turned, which is not
// what anyone reaching for a kill switch means.
func (s *AdminService) KillBots(ctx context.Context, adminID uuid.UUID, reason string) (int, error) {
	trimmed, err := requireReason(reason)
	if err != nil {
		return 0, err
	}

	var stopped int
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		n, err := st.BotRuns.StopAllLive(ctx, models.BotStopAdminKill)
		if err != nil {
			return err
		}
		stopped = n

		if err := st.Settings.Set(ctx, repository.SettingBotEnabled, strconv.FormatBool(false)); err != nil {
			return err
		}

		return s.audit(ctx, st, adminID, models.ActionBotKill, models.TargetPlatform, uuid.Nil, trimmed,
			map[string]any{"runs_stopped": n})
	})
	if err != nil {
		return 0, err
	}

	// After the commit, in the order that matters: drop the cached "enabled" so
	// nothing reads a stale yes, then stop the goroutines. A loop that checks in
	// between sees the stopped row and finishes anyway.
	if s.settingsCache != nil {
		s.settingsCache.BustCache()
	}
	if s.bots != nil {
		s.bots.StopAll(models.BotStopAdminKill)
	}

	s.fired(models.ActionBotKill)
	s.log.Warn("automated trading killed", "admin", adminID, "runs_stopped", stopped, "reason", trimmed)
	return stopped, nil
}

// botEnabled reads the flag from its row rather than from SettingsService's
// cache, so the console always shows what is actually stored. A missing row
// means the feature was never seeded, which is off.
func (s *AdminService) botEnabled(ctx context.Context) (bool, error) {
	raw, err := s.store.Settings.Get(ctx, repository.SettingBotEnabled)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	return raw == "true", nil
}

// EnableBots turns automated trading back on.
//
// The way back from KillBots, and audited like it — with a required reason,
// which is the decision worth stating.
//
// The case against requiring one is that this is the restorative direction, and
// friction on a recovery path is friction at the worst possible moment: an
// operator doing this is ending an outage, probably at speed, probably at night.
// The console already has a precedent for skipping the reason on a safe action —
// approving a KYC submission does not ask for one.
//
// It loses on three counts.
//
// First, the audit log's job across these two actions is the incident, not the
// blame. A kill with "halting pending a payout table review" followed by a
// restart with nothing attached leaves the only question that matters —
// what changed in between — unanswerable forever. The reason on the way back is
// the only place that answer can live.
//
// Second, this is not obviously the safe direction. The switch was thrown
// because something was wrong; turning it back on asserts that the thing is
// fixed. That is a claim about the world, and claims about the world are exactly
// what an audit log is for. The nearest precedent is not KYC approval but
// Unfreeze, which is equally restorative and requires a reason.
//
// Third, the KYC precedent does not transfer, because approving a submission
// leaves its own trace: the user becomes verified and the submission is stamped.
// Re-enabling leaves none. A boolean returns to a value it already held, and
// without a written reason nothing distinguishes "the feed recovered" from
// somebody clicking the wrong control.
//
// Ten characters during an incident is not real friction next to being unable to
// explain the restart the next morning.
func (s *AdminService) EnableBots(ctx context.Context, adminID uuid.UUID, reason string) error {
	trimmed, err := requireReason(reason)
	if err != nil {
		return err
	}

	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		if err := st.Settings.Set(ctx, repository.SettingBotEnabled, strconv.FormatBool(true)); err != nil {
			return err
		}
		return s.audit(ctx, st, adminID, models.ActionBotEnable, models.TargetPlatform, uuid.Nil, trimmed, nil)
	})
	if err != nil {
		return err
	}

	// Same reason KillBots busts it: the cached "off" would otherwise outlive the
	// decision by up to the cache's TTL, and a user told to try again would be
	// refused for no reason they could see.
	if s.settingsCache != nil {
		s.settingsCache.BustCache()
	}

	s.fired(models.ActionBotEnable)
	s.log.Warn("automated trading re-enabled", "admin", adminID, "reason", trimmed)
	return nil
}

// AdminBotRunPage is the console's runs table: rows already carrying whose run
// each one is, so the table is one round trip rather than one query per row.
type AdminBotRunPage struct {
	Items  []repository.AdminBotRun `json:"items"`
	Total  int                      `json:"total"`
	Limit  int                      `json:"limit"`
	Offset int                      `json:"offset"`
	Page   int                      `json:"page"`
	Pages  int                      `json:"pages"`
}

// ListBotRuns is the console's view of automated trading: what is running now,
// and what has run. status is "live", "stopped", or "" for both.
//
// History is paged because it has no ceiling. BOT_MAX_RUNS bounds what can be
// live at any moment; nothing bounds what has ever run, and a console screen
// that asked for all of it would get slower every day it was used.
func (s *AdminService) ListBotRuns(ctx context.Context, status string, limit, offset int) (*AdminBotRunPage, error) {
	items, total, err := s.store.BotRuns.ListAll(ctx, status, limit, offset)
	if err != nil {
		return nil, err
	}
	return &AdminBotRunPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// StopBotRun stops one run from the console.
//
// Not a smaller kill switch: automated trading stays on and every other run
// keeps going, so this is its own audited action rather than a bot_kill over a
// narrower target. The ownership check the user-facing stop makes has no meaning
// here — an operator is by definition not the owner — so what stands in its place
// is the audit row, which is written in the same transaction as the status change
// for the same reason KillBots does it: the console must not be able to stop a
// run without recording who did it and why.
//
// The row is locked for the decision, because a user stopping their own run at
// the same moment would otherwise have both callers read 'running' and both
// decide they were the one that ended it.
func (s *AdminService) StopBotRun(ctx context.Context, adminID, runID uuid.UUID, reason string) (*models.BotRun, error) {
	trimmed, err := requireReason(reason)
	if err != nil {
		return nil, err
	}

	var (
		out     *models.BotRun
		stopped bool
	)
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		// The row is locked before anything is decided, which is what keeps a
		// placement from committing under a run this is about to stop. Shared
		// with the user's own stop, so the two cannot disagree about what a stop
		// means.
		before, err := st.BotRuns.GetForUpdate(ctx, runID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		if before.Status == models.BotStopped {
			// Already over. Nothing to stop and nothing to audit — an audit row
			// for an action that changed nothing would be a false record of an
			// operator having stopped something.
			out = before
			return nil
		}

		if out, _, err = applyStop(ctx, st, runID, models.BotStopAdminStop); err != nil {
			return err
		}
		stopped = true

		return s.audit(ctx, st, adminID, models.ActionBotRunStop, models.TargetBotRun, runID, trimmed,
			map[string]any{"account_id": before.AccountID.String(), "status": out.Status})
	})
	if err != nil {
		return nil, err
	}

	if stopped && s.bots != nil {
		// After the commit: the loop learns it should stop, and the user learns
		// it did. Inside the transaction neither would be true yet.
		s.bots.SignalStop(ctx, runID, models.BotStopAdminStop)
	}
	if stopped {
		s.fired(models.ActionBotRunStop)
		s.log.Warn("bot run stopped from console",
			"admin", adminID, "run", runID, "status", out.Status, "reason", trimmed)
	}
	return out, nil
}
