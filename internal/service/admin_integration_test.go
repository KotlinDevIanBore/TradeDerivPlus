//go:build integration

package service_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"golang.org/x/crypto/bcrypt"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// seedAdminWithPassword creates an admin whose password is known, so the auth
// paths can be exercised rather than only the actions behind them.
func (h *harness) seedAdminWithPassword(username, password string, mustChange bool) uuid.UUID {
	h.t.Helper()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		h.t.Fatal(err)
	}
	admin := &models.AdminUser{
		Username:           username,
		PasswordHash:       string(hash),
		Role:               models.RoleAdmin,
		MustChangePassword: mustChange,
	}
	if err := h.store.Admins.Create(h.ctx, admin); err != nil {
		h.t.Fatal(err)
	}
	return admin.ID
}

func TestAdminLoginAndTokenScope(t *testing.T) {
	h := newHarness(t)
	h.seedAdminWithPassword("ops", "correct-horse-1", false)

	if _, err := h.admin.Login(h.ctx, "ops", "wrong"); err != service.ErrInvalidCredentials {
		t.Errorf("wrong password returned %v, want ErrInvalidCredentials", err)
	}
	// An unknown username must be indistinguishable from a wrong password.
	if _, err := h.admin.Login(h.ctx, "nobody", "wrong"); err != service.ErrInvalidCredentials {
		t.Errorf("unknown username returned %v, want ErrInvalidCredentials", err)
	}

	result, err := h.admin.Login(h.ctx, "ops", "correct-horse-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	claims, err := h.admin.ParseToken(result.Tokens.AccessToken)
	if err != nil {
		t.Fatalf("parse own token: %v", err)
	}
	if claims.Scope != service.ScopeAdmin {
		t.Fatalf("token scope %q, want admin", claims.Scope)
	}

	// The two token families must not open each other's doors, even though
	// they are signed with the same secret.
	userTokens, err := h.newUserToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.admin.ParseToken(userTokens); err != service.ErrInvalidToken {
		t.Errorf("a user token parsed as admin: %v", err)
	}

	auth := service.NewAuthService(h.store, h.cfg)
	if _, err := auth.ParseToken(result.Tokens.AccessToken); err != service.ErrInvalidToken {
		t.Errorf("an admin token parsed as a user: %v", err)
	}

	// Refresh rotates the pair.
	refreshed, admin, err := h.admin.Refresh(h.ctx, result.Tokens.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if refreshed.AccessToken == "" || admin.Username != "ops" {
		t.Fatal("refresh did not return a usable session")
	}
	// An access token is not a refresh token.
	if _, _, err := h.admin.Refresh(h.ctx, result.Tokens.AccessToken); err != service.ErrInvalidToken {
		t.Errorf("an access token was accepted as a refresh token: %v", err)
	}

	if _, err := h.admin.Me(h.ctx, claims.AdminID); err != nil {
		t.Fatalf("me: %v", err)
	}
	if _, err := h.admin.Me(h.ctx, uuid.New()); err != service.ErrNotFound {
		t.Errorf("unknown admin returned %v, want ErrNotFound", err)
	}
}

// Outside development the seeded credential cannot open a session at all.
func TestSeededAdminIsRefusedOutsideDevelopment(t *testing.T) {
	h := newHarness(t)
	h.seedAdminWithPassword("seeded", service.SeededAdminPassword, true)

	// The harness config uses Env "test", which is not development.
	result, err := h.admin.Login(h.ctx, "seeded", service.SeededAdminPassword)
	if err != service.ErrMustChangePassword {
		t.Fatalf("login returned %v, want ErrMustChangePassword", err)
	}
	// A restricted session is still issued so the password can be changed.
	if result == nil || result.Tokens.AccessToken == "" {
		t.Fatal("no session issued, so there is no way to change the password")
	}
	claims, err := h.admin.ParseToken(result.Tokens.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Role != service.RoleRestricted {
		t.Fatalf("role %q, want restricted", claims.Role)
	}
}

func TestChangePasswordRules(t *testing.T) {
	h := newHarness(t)
	adminID := h.seedAdminWithPassword("changer", service.SeededAdminPassword, true)

	cases := []struct {
		name    string
		current string
		next    string
		wantErr error
		field   string
	}{
		{"wrong current password", "nope", "brandNewPass1", nil, "current_password"},
		{"too short", service.SeededAdminPassword, "short1", nil, "new_password"},
		{"no digit", service.SeededAdminPassword, "nodigitshere", nil, "new_password"},
		{"same as current", service.SeededAdminPassword, service.SeededAdminPassword, service.ErrPasswordUnchanged, ""},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := h.admin.ChangePassword(h.ctx, adminID, c.current, c.next)
			if c.wantErr != nil {
				if err != c.wantErr {
					t.Fatalf("got %v, want %v", err, c.wantErr)
				}
				return
			}
			var ve *service.ValidationError
			if !asValidation(err, &ve) {
				t.Fatalf("got %v, want a validation error", err)
			}
			if ve.Field != c.field {
				t.Errorf("named field %q, want %q", ve.Field, c.field)
			}
		})
	}

	if err := h.admin.ChangePassword(h.ctx, adminID, service.SeededAdminPassword, "properPassword1"); err != nil {
		t.Fatalf("valid change refused: %v", err)
	}

	// The flag clears with the password, so the session stops being restricted.
	result, err := h.admin.Login(h.ctx, "changer", "properPassword1")
	if err != nil {
		t.Fatalf("login with the new password: %v", err)
	}
	if result.Admin.MustChangePassword {
		t.Error("must_change_password survived the change")
	}
	if _, err := h.admin.Login(h.ctx, "changer", service.SeededAdminPassword); err != service.ErrInvalidCredentials {
		t.Error("the old password still works")
	}
}

func TestAdminActionsDemandAReason(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	_, accountID := h.newUser(decimal.NewFromInt(100))

	short := "too short"

	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, short); !isReasonError(err) {
		t.Errorf("freeze accepted a thin reason: %v", err)
	}
	if _, err := h.admin.Unfreeze(h.ctx, adminID, accountID, ""); !isReasonError(err) {
		t.Errorf("unfreeze accepted no reason: %v", err)
	}
	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(10),
		BalanceType: models.BalanceReal, Reason: short,
	}); !isReasonError(err) {
		t.Errorf("adjustment accepted a thin reason: %v", err)
	}

	// Nothing was recorded, because nothing happened.
	var audits int
	if err := h.store.Pool().QueryRow(h.ctx, `SELECT count(*) FROM admin_audit_log`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if audits != 0 {
		t.Fatalf("%d audit rows written for actions that were refused", audits)
	}
	h.assertReconciled(accountID)
}

func TestAdjustValidatesAmountAndBalance(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	_, accountID := h.newUser(decimal.NewFromInt(100))
	reason := "a perfectly adequate reason for the change"

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.Zero,
		BalanceType: models.BalanceReal, Reason: reason,
	}); !isFieldError(err, "amount") {
		t.Errorf("a zero adjustment was accepted: %v", err)
	}

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(10),
		BalanceType: "savings", Reason: reason,
	}); !isFieldError(err, "balance_type") {
		t.Errorf("an unknown balance type was accepted: %v", err)
	}

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: uuid.New(), Amount: decimal.NewFromInt(10),
		BalanceType: models.BalanceReal, Reason: reason,
	}); err != service.ErrNotFound {
		t.Errorf("adjusting a missing account returned %v, want ErrNotFound", err)
	}

	// An over-debit must be refused whole: no ledger row, no audit row.
	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(-1000),
		BalanceType: models.BalanceBonus, Reason: reason,
	}); err != service.ErrInsufficientFunds {
		t.Errorf("over-debit returned %v, want ErrInsufficientFunds", err)
	}

	var rows int
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM transactions WHERE type = 'admin_adjustment'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("%d adjustment rows survived a rolled-back transaction", rows)
	}
	h.assertReconciled(accountID)
}

func TestKYCReviewMovesUserAndSubmissionTogether(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.Zero)

	submission := &models.KYCSubmission{
		// Create no longer generates the id: it is minted at presign in the real
		// flow and embedded in the object key.
		ID:          uuid.New(),
		UserID:      userID,
		IDNumber:    "21234567",
		DocumentKey: "kyc/" + userID.String() + "/fixture.jpg",
		ContentType: "image/jpeg",
		SizeBytes:   2048,
	}
	if err := h.store.KYC.Create(h.ctx, submission); err != nil {
		t.Fatal(err)
	}

	// A rejection must say why; an approval need not.
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, submission.ID, false, "no"); !isReasonError(err) {
		t.Errorf("rejection accepted a thin reason: %v", err)
	}

	reviewed, err := h.admin.ReviewKYC(h.ctx, adminID, submission.ID, true, "")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if reviewed.Status != models.KYCSubmissionApproved {
		t.Fatalf("submission is %q after approval", reviewed.Status)
	}

	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.KYCStatus != models.KYCVerified {
		t.Fatalf("user is %q after approval, want verified", user.KYCStatus)
	}

	// Reviewing again changes nothing.
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, submission.ID, false, "changed my mind about this"); err != service.ErrAlreadyReviewed {
		t.Errorf("second review returned %v, want ErrAlreadyReviewed", err)
	}
	if _, err := h.admin.ReviewKYC(h.ctx, adminID, uuid.New(), true, ""); err != service.ErrNotFound {
		t.Errorf("reviewing a missing submission returned %v, want ErrNotFound", err)
	}

	// And verification is what unlocks withdrawals.
	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(50),
		BalanceType: models.BalanceReal, Reason: "funding a withdrawal after verification",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.wallet.Withdraw(h.ctx, service.WithdrawInput{
		UserID: userID, AccountID: accountID,
		AmountUSD: decimal.NewFromInt(50), PhoneNumber: "0712345678",
	}); err != nil {
		t.Fatalf("withdrawal after verification: %v", err)
	}
	h.assertReconciled(accountID)
}

func TestKYCRejectionMarksTheUserRejected(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, _ := h.newUser(decimal.Zero)

	submission := &models.KYCSubmission{UserID: userID, IDNumber: "29999999"}
	if err := h.store.KYC.Create(h.ctx, submission); err != nil {
		t.Fatal(err)
	}

	if _, err := h.admin.ReviewKYC(h.ctx, adminID, submission.ID, false,
		"The document is unreadable, ask for a resubmission"); err != nil {
		t.Fatalf("reject: %v", err)
	}

	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if user.KYCStatus != models.KYCRejected {
		t.Fatalf("user is %q after rejection, want rejected", user.KYCStatus)
	}
}

func TestDashboardCountsAndDetectsDrift(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
	}); err != nil {
		t.Fatal(err)
	}

	d, err := h.admin.Dashboard(h.ctx)
	if err != nil {
		t.Fatalf("dashboard: %v", err)
	}
	if d.Users < 1 {
		t.Error("dashboard reported no users")
	}
	if d.OpenPositions != 1 {
		t.Fatalf("dashboard counted %d open positions, want 1", d.OpenPositions)
	}
	if len(d.DepositsByHour) != 24 {
		t.Fatalf("deposit series has %d buckets, want 24", len(d.DepositsByHour))
	}
	if d.DriftedAccounts != 0 {
		t.Fatalf("a healthy system reported %d drifted accounts", d.DriftedAccounts)
	}

	// Move a balance behind the ledger's back; the gauge must notice.
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE wallets SET real_balance = real_balance + 250 WHERE account_id = $1`, accountID); err != nil {
		t.Fatal(err)
	}

	drifted, err := h.admin.Dashboard(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if drifted.DriftedAccounts != 1 {
		t.Fatalf("drift went undetected: %d accounts reported", drifted.DriftedAccounts)
	}
	if len(drifted.Drift) != 1 {
		t.Fatalf("drift detail listed %d accounts", len(drifted.Drift))
	}
	if want := decimal.NewFromInt(-250); !drifted.Drift[0].Difference.Equal(want) {
		t.Fatalf("difference %s, want %s", drifted.Drift[0].Difference, want)
	}

	// Put it back so the harness's own reconciliation check passes.
	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE wallets SET real_balance = real_balance - 250 WHERE account_id = $1`, accountID); err != nil {
		t.Fatal(err)
	}
	h.assertReconciled(accountID)
}

func TestAdminReadsAreFilterableAndPaged(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(500))
	reason := "building an audit trail worth paging through"

	for i := 0; i < 3; i++ {
		if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
			AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(10),
			BalanceType: models.BalanceReal, Reason: reason,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := h.admin.Freeze(h.ctx, adminID, accountID, reason); err != nil {
		t.Fatal(err)
	}

	users, err := h.admin.Users(h.ctx, "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if users.Total < 1 {
		t.Fatal("user search found nobody")
	}

	user, err := h.store.Users.GetByID(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	found, err := h.admin.Users(h.ctx, user.PhoneNumber, 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if found.Total != 1 {
		t.Fatalf("searching by phone found %d users, want 1", found.Total)
	}
	if len(found.Items[0].Accounts) == 0 {
		t.Error("the user row carries no accounts, so the table would render empty")
	}

	detail, err := h.admin.UserDetail(h.ctx, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Accounts) != 1 || detail.Accounts[0].Wallet == nil {
		t.Fatal("user detail is missing the account or its wallet")
	}
	if !detail.Accounts[0].IsFrozen() {
		t.Error("user detail does not show the account as frozen")
	}
	if len(detail.Audit) < 4 {
		t.Fatalf("user detail carries %d audit rows, want at least 4", len(detail.Audit))
	}
	if _, err := h.admin.UserDetail(h.ctx, uuid.New()); err != service.ErrNotFound {
		t.Errorf("detail for a missing user returned %v, want ErrNotFound", err)
	}

	adjustments, err := h.admin.AuditLog(h.ctx, nil, models.ActionAdjustment, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if adjustments.Total != 3 {
		t.Fatalf("filtered to %d adjustments, want 3", adjustments.Total)
	}

	byAdmin, err := h.admin.AuditLog(h.ctx, &adminID, "", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if byAdmin.Total != 4 {
		t.Fatalf("this admin has %d actions, want 4", byAdmin.Total)
	}

	txs, err := h.admin.Transactions(h.ctx, "", models.TxAdminAdjustment, "", 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if txs.Total != 3 || len(txs.Items) != 2 {
		t.Fatalf("transaction search returned total %d with %d on the page", txs.Total, len(txs.Items))
	}
	if txs.Items[0].AdminUsername == nil {
		t.Error("an adjustment row does not name the admin who made it")
	}

	queue, err := h.admin.KYCQueue(h.ctx, models.KYCSubmissionPending, 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if queue.Total != 0 {
		t.Fatalf("pending queue holds %d, want 0", queue.Total)
	}
}

func isReasonError(err error) bool { return isFieldError(err, "reason") }

func isFieldError(err error, field string) bool {
	var ve *service.ValidationError
	return asValidation(err, &ve) && ve.Field == field
}
