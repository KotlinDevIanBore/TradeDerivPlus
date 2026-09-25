package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// SeededAdminPassword is the development-only default. It is referenced by the
// seed command and by ChangePassword, which refuses to let an admin "change"
// their password back to it.
const SeededAdminPassword = "admin123"

// AdminService is one file because every admin action has the same shape:
// authorise, act, audit -- all inside one transaction, so an action can never
// commit without the record of who did it and why.
type AdminService struct {
	store *repository.Store
	hub   Broadcaster
	cfg   *config.Config
	log   *slog.Logger

	// onAction is called after a successful audited action, for metrics.
	onAction func(action string)

	// connections reports live socket count for the dashboard tile.
	connections ConnectionCounter

	// bots and settingsCache are the in-process halves of the bot kill switch:
	// the database half works without them, these make it immediate.
	bots          botStopper
	settingsCache interface{ BustCache() }

	// deposits is the M-Pesa service, through the one method the console needs:
	// settle the original row. Nil until main wires it; ResolveDeposit refuses
	// rather than settle by any other route.
	deposits depositResolver
}

// depositResolver is what ResolveDeposit needs from MpesaService. The console
// never writes a deposit outcome itself — it asks the service that owns the
// callback path to write it, so the guards and the lock order are shared.
type depositResolver interface {
	ResolveByAdmin(ctx context.Context, checkoutID string, received bool, adminID uuid.UUID) (bool, error)
}

// SetDepositResolver wires the M-Pesa service in. main.go calls it; the harness
// mirrors it.
func (s *AdminService) SetDepositResolver(r depositResolver) { s.deposits = r }

func NewAdminService(store *repository.Store, hub Broadcaster, cfg *config.Config, log *slog.Logger) *AdminService {
	return &AdminService{store: store, hub: hub, cfg: cfg, log: log}
}

// OnAction registers a hook fired after each audited action. main wires the
// Prometheus counter here so the service stays unaware of the metrics library.
func (s *AdminService) OnAction(fn func(action string)) { s.onAction = fn }

// AdminClaims carries scope=admin. The user middleware rejects this scope and
// the admin middleware requires it, so neither token opens the other's routes.
type AdminClaims struct {
	AdminID uuid.UUID `json:"aid"`
	Role    string    `json:"rol"`
	Scope   string    `json:"scp"`
	Kind    string    `json:"knd"`
	jwt.RegisteredClaims
}

type AdminAuthResult struct {
	Admin  *models.AdminUser `json:"admin"`
	Tokens TokenPair         `json:"-"`
}

// --- Auth ------------------------------------------------------------------

func (s *AdminService) Login(ctx context.Context, username, password string) (*AdminAuthResult, error) {
	admin, err := s.store.Admins.GetByUsername(ctx, strings.TrimSpace(username))
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// Same error and the same bcrypt cost as a wrong password, so the
			// response does not reveal which usernames exist.
			_ = bcrypt.CompareHashAndPassword(
				[]byte("$2a$10$invalidinvalidinvalidinvalidinvalidinvalidinvalidinvalidin"), []byte(password))
			return nil, ErrInvalidCredentials
		}
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(password)) != nil {
		return nil, ErrInvalidCredentials
	}

	// Outside development the seeded credential cannot be used at all: the
	// session is refused until the password is changed. The client is told
	// which failure this is so it can route to the change-password screen.
	if admin.MustChangePassword && s.cfg.Env != "development" {
		tokens, err := s.issueTokens(admin, true)
		if err != nil {
			return nil, err
		}
		return &AdminAuthResult{Admin: admin, Tokens: *tokens}, ErrMustChangePassword
	}

	tokens, err := s.issueTokens(admin, false)
	if err != nil {
		return nil, err
	}
	return &AdminAuthResult{Admin: admin, Tokens: *tokens}, nil
}

func (s *AdminService) ChangePassword(ctx context.Context, adminID uuid.UUID, current, next string) error {
	admin, err := s.store.Admins.GetByID(ctx, adminID)
	if err != nil {
		return ErrNotFound
	}
	if bcrypt.CompareHashAndPassword([]byte(admin.PasswordHash), []byte(current)) != nil {
		return invalid("current_password", "That is not your current password")
	}
	if len(next) < 8 {
		return invalid("new_password", "Password must be at least 8 characters")
	}
	if !hasDigit(next) {
		return invalid("new_password", "Password must contain at least one number")
	}
	// Changing the password to the value it already has, or back to the
	// seeded default, would leave the account exactly as exposed as before.
	if next == current || next == SeededAdminPassword {
		return ErrPasswordUnchanged
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(next), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	return s.store.Admins.SetPassword(ctx, adminID, string(hash))
}

func (s *AdminService) Me(ctx context.Context, adminID uuid.UUID) (*models.AdminUser, error) {
	admin, err := s.store.Admins.GetByID(ctx, adminID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	return admin, err
}

func (s *AdminService) ParseToken(raw string) (*AdminClaims, error) {
	token, err := jwt.ParseWithClaims(raw, &AdminClaims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(s.cfg.JWTSecret), nil
	})
	if err != nil || !token.Valid {
		return nil, ErrInvalidToken
	}
	claims, ok := token.Claims.(*AdminClaims)
	if !ok || claims.Scope != ScopeAdmin {
		return nil, ErrInvalidToken
	}
	return claims, nil
}

func (s *AdminService) Refresh(ctx context.Context, refreshToken string) (*TokenPair, *models.AdminUser, error) {
	claims, err := s.ParseToken(refreshToken)
	if err != nil {
		return nil, nil, err
	}
	if claims.Kind != "refresh" {
		return nil, nil, ErrInvalidToken
	}
	admin, err := s.store.Admins.GetByID(ctx, claims.AdminID)
	if err != nil {
		return nil, nil, ErrInvalidToken
	}
	tokens, err := s.issueTokens(admin, admin.MustChangePassword && s.cfg.Env != "development")
	if err != nil {
		return nil, nil, err
	}
	return tokens, admin, nil
}

// issueTokens mints the pair. When restricted is set the access token is
// marked so the middleware allows only the change-password route.
func (s *AdminService) issueTokens(admin *models.AdminUser, restricted bool) (*TokenPair, error) {
	access, err := s.signAdmin(admin, "access", s.cfg.AccessTokenTTL, restricted)
	if err != nil {
		return nil, err
	}
	refresh, err := s.signAdmin(admin, "refresh", s.cfg.RefreshTokenTTL, restricted)
	if err != nil {
		return nil, err
	}
	return &TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(s.cfg.AccessTokenTTL.Seconds()),
	}, nil
}

func (s *AdminService) signAdmin(admin *models.AdminUser, kind string, ttl time.Duration, restricted bool) (string, error) {
	now := time.Now()
	role := admin.Role
	if restricted {
		role = RoleRestricted
	}
	claims := AdminClaims{
		AdminID: admin.ID,
		Role:    role,
		Scope:   ScopeAdmin,
		Kind:    kind,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   admin.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			ID:        uuid.NewString(),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(s.cfg.JWTSecret))
}

// --- Audited actions -------------------------------------------------------
//
// Every action below follows the same shape: take the locks, make the change,
// write the audit row, all inside one transaction. An action that committed
// without its audit row would make the console powerful but not accountable,
// which is the whole reason the table exists.

// audit writes the trail entry. It is only ever called inside a transaction.
func (s *AdminService) audit(ctx context.Context, st *repository.Store, adminID uuid.UUID,
	action, targetType string, targetID uuid.UUID, reason string, meta map[string]any) error {
	return st.Audit.Write(ctx, &models.AuditEntry{
		AdminID:    adminID,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		Reason:     reason,
		Metadata:   meta,
	})
}

func (s *AdminService) fired(action string) {
	if s.onAction != nil {
		s.onAction(action)
	}
}

// Deposit outcomes an operator may state.
const (
	DepositOutcomeCompleted = "completed"
	DepositOutcomeFailed    = "failed"
)

// ResolveDeposit settles a pending deposit on an operator's say-so: the
// customer's statement plus Safaricom's portal, when the callback never came
// and the reconciler could not tell. It settles the ORIGINAL row through the
// callback's own path, which is the whole point — a late callback then loses
// its guard and credits nothing. Adjust cannot offer that: an adjustment is a
// new row, and a late callback still credits the deposit it never knew about.
//
// The audit row is written after the settlement, in its own transaction. They
// cannot share one: the credit belongs to settleDeposit's transaction, whose
// lock order every settlement must keep, and the console's audit must not be
// the first path to take the wallet lock from outside it. The ledger row itself
// records the operator (metadata.admin_id, settled_by = admin), so the money
// is attributed even if the process died between the two writes.
func (s *AdminService) ResolveDeposit(ctx context.Context, adminID, txID uuid.UUID, outcome, reason string) (*models.Transaction, error) {
	trimmed, err := requireReason(reason)
	if err != nil {
		return nil, err
	}
	if outcome != DepositOutcomeCompleted && outcome != DepositOutcomeFailed {
		return nil, invalid("outcome", "Outcome must be completed or failed")
	}
	if s.deposits == nil {
		return nil, errors.New("deposit resolve: no M-Pesa service wired")
	}

	tx, err := s.store.Transactions.GetByID(ctx, txID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if tx.Type != models.TxDeposit {
		return nil, invalid("transaction", "Only a deposit can be resolved")
	}
	if tx.Status != models.StatusPending {
		return nil, ErrDepositSettled
	}
	checkoutID, _ := tx.Metadata["checkout_request_id"].(string)
	if checkoutID == "" {
		return nil, invalid("transaction", "This deposit has no M-Pesa request to resolve")
	}

	won, err := s.deposits.ResolveByAdmin(ctx, checkoutID, outcome == DepositOutcomeCompleted, adminID)
	if err != nil {
		return nil, err
	}
	if !won {
		// Settled between the operator's read and their confirm. Nothing was
		// written by us and there is nothing to audit; the row says who did.
		return nil, ErrDepositSettled
	}

	account, err := s.store.Accounts.GetByID(ctx, tx.AccountID)
	if err != nil {
		return nil, err
	}
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		return s.audit(ctx, st, adminID, models.ActionDepositResolve, models.TargetTransaction, tx.ID, trimmed,
			map[string]any{
				"account_number":      account.AccountNumber,
				"account_id":          tx.AccountID.String(),
				"checkout_request_id": checkoutID,
				"outcome":             outcome,
				"amount":              tx.Amount.String(),
				"balance_type":        tx.BalanceType,
			})
	})
	if err != nil {
		return nil, err
	}
	s.fired(models.ActionDepositResolve)

	return s.store.Transactions.GetByID(ctx, txID)
}

// requireReason enforces the console's rule that nothing which touches money
// or account state happens without a written justification.
func requireReason(reason string) (string, error) {
	trimmed := strings.TrimSpace(reason)
	if len(trimmed) < 10 {
		return "", invalid("reason", "Give a reason of at least 10 characters")
	}
	return trimmed, nil
}

// Freeze stops the account moving on its own. Existing positions stay open and
// can still be closed, which is what the confirmation copy promises the admin.
func (s *AdminService) Freeze(ctx context.Context, adminID, accountID uuid.UUID, reason string) (*models.Account, error) {
	trimmed, err := requireReason(reason)
	if err != nil {
		return nil, err
	}

	var account *models.Account
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		locked, err := st.Accounts.GetForUpdate(ctx, accountID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := st.Accounts.SetStatus(ctx, accountID, models.AccountFrozen, &trimmed); err != nil {
			return err
		}
		locked.Status = models.AccountFrozen
		locked.FrozenReason = &trimmed
		account = locked

		return s.audit(ctx, st, adminID, models.ActionFreeze, models.TargetAccount, accountID, trimmed,
			map[string]any{"account_number": locked.AccountNumber})
	})
	if err != nil {
		return nil, err
	}

	s.fired(models.ActionFreeze)
	s.notifyOwner(ctx, account.UserID, "account", map[string]any{
		"account_id": accountID, "status": models.AccountFrozen, "frozen_reason": trimmed,
	})
	s.log.Info("account frozen", "account", accountID, "admin", adminID)
	return account, nil
}

func (s *AdminService) Unfreeze(ctx context.Context, adminID, accountID uuid.UUID, reason string) (*models.Account, error) {
	trimmed, err := requireReason(reason)
	if err != nil {
		return nil, err
	}

	var account *models.Account
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		locked, err := st.Accounts.GetForUpdate(ctx, accountID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		if err := st.Accounts.SetStatus(ctx, accountID, models.AccountActive, nil); err != nil {
			return err
		}
		locked.Status = models.AccountActive
		locked.FrozenReason = nil
		account = locked

		return s.audit(ctx, st, adminID, models.ActionUnfreeze, models.TargetAccount, accountID, trimmed,
			map[string]any{"account_number": locked.AccountNumber})
	})
	if err != nil {
		return nil, err
	}

	s.fired(models.ActionUnfreeze)
	s.notifyOwner(ctx, account.UserID, "account", map[string]any{
		"account_id": accountID, "status": models.AccountActive,
	})
	s.log.Info("account unfrozen", "account", accountID, "admin", adminID)
	return account, nil
}

type AdjustInput struct {
	AdminID     uuid.UUID
	AccountID   uuid.UUID
	Amount      decimal.Decimal // signed: negative debits
	BalanceType string          // real | bonus
	Reason      string
}

// Adjust is a manual balance correction. It writes an ordinary ledger row, so
// the ledger stays the single source of truth and there is no side channel
// that edits a balance without recording why.
//
// DELIBERATE: an adjustment is NOT blocked by a frozen account. The freeze
// check in open, deposit and withdraw exists to stop the account moving on its
// own. Freezing an account and then being unable to correct the very balance
// that caused the freeze would be a dead end for support. If someone later
// adds the freeze guard here "for consistency", the test
// TestAdjustmentSucceedsOnFrozenAccount will fail and point them back here.
func (s *AdminService) Adjust(ctx context.Context, in AdjustInput) (*models.Transaction, *models.Wallet, error) {
	trimmed, err := requireReason(in.Reason)
	if err != nil {
		return nil, nil, err
	}
	if in.Amount.IsZero() {
		return nil, nil, invalid("amount", "Enter a non-zero amount")
	}
	if in.BalanceType != models.BalanceReal && in.BalanceType != models.BalanceBonus {
		return nil, nil, invalid("balance_type", "Balance must be real or bonus")
	}

	var tx *models.Transaction
	var wallet *models.Wallet
	var userID uuid.UUID

	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		account, err := st.Accounts.GetByID(ctx, in.AccountID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		userID = account.UserID

		if _, err := st.Wallets.GetForUpdate(ctx, in.AccountID); err != nil {
			return err
		}

		ref := "adjustment"
		tx = &models.Transaction{
			AccountID:   in.AccountID,
			Type:        models.TxAdminAdjustment,
			BalanceType: in.BalanceType,
			Amount:      in.Amount,
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata: map[string]any{
				"admin_id": in.AdminID.String(),
				"reason":   trimmed,
			},
		}
		if err := st.Transactions.Insert(ctx, tx); err != nil {
			return err
		}

		// The non-negative guard in Adjust is what refuses an over-debit; it
		// returns no rows rather than writing a negative balance.
		w, err := st.Wallets.Adjust(ctx, in.AccountID, in.BalanceType, in.Amount)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrInsufficientFunds
			}
			return err
		}
		wallet = w

		return s.audit(ctx, st, in.AdminID, models.ActionAdjustment, models.TargetTransaction, tx.ID, trimmed,
			map[string]any{
				"account_id":     in.AccountID.String(),
				"account_number": account.AccountNumber,
				"amount":         in.Amount.String(),
				"balance_type":   in.BalanceType,
				"transaction_id": tx.ID.String(),
			})
	})
	if err != nil {
		return nil, nil, err
	}

	s.fired(models.ActionAdjustment)
	s.notifyOwner(ctx, userID, "wallet", wallet)
	s.notifyOwner(ctx, userID, "transaction", map[string]any{
		"transaction_id": tx.ID, "status": models.StatusCompleted,
		"type": models.TxAdminAdjustment, "amount": tx.Amount,
	})
	s.log.Info("balance adjusted", "account", in.AccountID, "amount", in.Amount.String(), "admin", in.AdminID)
	return tx, wallet, nil
}

// ReviewKYC approves or rejects a submission and moves the user's status in
// the same transaction, so the two can never disagree.
func (s *AdminService) ReviewKYC(ctx context.Context, adminID, submissionID uuid.UUID, approve bool, reason string) (*models.KYCSubmission, error) {
	trimmed := strings.TrimSpace(reason)
	if !approve {
		var err error
		if trimmed, err = requireReason(reason); err != nil {
			return nil, err
		}
	}

	status, userStatus, action := models.KYCSubmissionApproved, models.KYCVerified, models.ActionKYCApprove
	if !approve {
		status, userStatus, action = models.KYCSubmissionRejected, models.KYCRejected, models.ActionKYCReject
	}

	var submission *models.KYCSubmission
	err := s.store.WithTx(ctx, func(st *repository.Store) error {
		existing, err := st.KYC.GetByID(ctx, submissionID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		if existing.Status != models.KYCSubmissionPending {
			return ErrAlreadyReviewed
		}

		if err := st.KYC.Review(ctx, submissionID, adminID, status, trimmed); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrAlreadyReviewed
			}
			return err
		}
		if err := st.Users.SetKYCStatus(ctx, existing.UserID, userStatus); err != nil {
			return err
		}

		existing.Status = status
		existing.ReviewedBy = &adminID
		existing.ReviewReason = &trimmed
		submission = existing

		return s.audit(ctx, st, adminID, action, models.TargetUser, existing.UserID, trimmed,
			map[string]any{"submission_id": submissionID.String(), "kyc_status": userStatus})
	})
	if err != nil {
		return nil, err
	}

	s.fired(action)
	// Push the decision so the user's Withdraw button unlocks immediately
	// rather than on their next profile refetch.
	s.notifyOwner(ctx, submission.UserID, "kyc", map[string]any{
		"user_id": submission.UserID, "kyc_status": userStatus,
	})
	s.log.Info("kyc reviewed", "submission", submissionID, "status", status, "admin", adminID)
	return submission, nil
}

// notifyOwner pushes on the user's channel. A failed notification never fails
// the action that triggered it.
func (s *AdminService) notifyOwner(ctx context.Context, userID uuid.UUID, msgType string, payload any) {
	if s.hub == nil || userID == uuid.Nil {
		return
	}
	s.hub.Broadcast(UserTopic(userID.String()), msgType, payload)
}
