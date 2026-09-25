package handlers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type AdminHandler struct {
	admin *service.AdminService
	cfg   *config.Config
}

func NewAdminHandler(admin *service.AdminService, cfg *config.Config) *AdminHandler {
	return &AdminHandler{admin: admin, cfg: cfg}
}

// --- Auth ------------------------------------------------------------------

type adminLoginRequest struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func (h *AdminHandler) Login(c *gin.Context) {
	var req adminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Username and password are required", "")
		return
	}

	result, err := h.admin.Login(c.Request.Context(), req.Username, req.Password)

	// A session that must change its password is still issued, restricted to
	// the change-password route, so the client can complete the change. The
	// status tells it to route there instead of to the dashboard.
	if errors.Is(err, service.ErrMustChangePassword) && result != nil {
		h.issue(c, http.StatusOK, result, true)
		return
	}
	if err != nil {
		fail(c, err)
		return
	}
	h.issue(c, http.StatusOK, result, false)
}

func (h *AdminHandler) issue(c *gin.Context, status int, result *service.AdminAuthResult, mustChange bool) {
	setRefreshCookie(c, h.cfg, adminRefreshCookie, adminRefreshPath,
		result.Tokens.RefreshToken, h.cfg.RefreshTokenTTL)

	c.JSON(status, gin.H{
		"admin":                    result.Admin,
		"access_token":             result.Tokens.AccessToken,
		"expires_in":               result.Tokens.ExpiresIn,
		"password_change_required": mustChange,
	})
}

func (h *AdminHandler) Refresh(c *gin.Context) {
	token := readCookie(c, adminRefreshCookie)
	if token == "" {
		fail(c, service.ErrInvalidToken)
		return
	}

	tokens, admin, err := h.admin.Refresh(c.Request.Context(), token)
	if err != nil {
		clearRefreshCookie(c, h.cfg, adminRefreshCookie, adminRefreshPath)
		fail(c, err)
		return
	}

	setRefreshCookie(c, h.cfg, adminRefreshCookie, adminRefreshPath,
		tokens.RefreshToken, h.cfg.RefreshTokenTTL)
	c.JSON(http.StatusOK, gin.H{
		"admin":                    admin,
		"access_token":             tokens.AccessToken,
		"expires_in":               tokens.ExpiresIn,
		"password_change_required": admin.MustChangePassword && h.cfg.Env != "development",
	})
}

func (h *AdminHandler) Logout(c *gin.Context) {
	clearRefreshCookie(c, h.cfg, adminRefreshCookie, adminRefreshPath)
	c.Status(http.StatusNoContent)
}

func (h *AdminHandler) Me(c *gin.Context) {
	admin, err := h.admin.Me(c.Request.Context(), middleware.AdminID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, admin)
}

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" binding:"required"`
	NewPassword     string `json:"new_password" binding:"required"`
}

func (h *AdminHandler) ChangePassword(c *gin.Context) {
	var req changePasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Current and new password are required", "")
		return
	}

	if err := h.admin.ChangePassword(c.Request.Context(), middleware.AdminID(c),
		req.CurrentPassword, req.NewPassword); err != nil {
		fail(c, err)
		return
	}
	// The old token still carries the restricted role, so re-authentication
	// is required. Clearing the cookie makes that explicit rather than
	// leaving a half-privileged session alive.
	clearRefreshCookie(c, h.cfg, adminRefreshCookie, adminRefreshPath)
	c.JSON(http.StatusOK, gin.H{"message": "Password changed. Sign in again."})
}

// --- Reads -----------------------------------------------------------------

func (h *AdminHandler) Dashboard(c *gin.Context) {
	d, err := h.admin.Dashboard(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

func (h *AdminHandler) Users(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	result, err := h.admin.Users(c.Request.Context(), c.Query("search"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) UserDetail(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid user id", "")
		return
	}

	detail, err := h.admin.UserDetail(c.Request.Context(), userID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, detail)
}

func (h *AdminHandler) Transactions(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	result, err := h.admin.Transactions(c.Request.Context(),
		c.Query("search"), c.Query("type"), c.Query("status"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) KYCQueue(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	status := c.Query("status")
	if status == "" {
		status = "pending"
	}
	if status == "all" {
		status = ""
	}

	result, err := h.admin.KYCQueue(c.Request.Context(), status, limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

func (h *AdminHandler) AuditLog(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 50, 1, 200)
	page, offset := resolvePage(c, limit)

	var adminID *uuid.UUID
	if raw := c.Query("admin_id"); raw != "" {
		if parsed, err := uuid.Parse(raw); err == nil {
			adminID = &parsed
		}
	}

	result, err := h.admin.AuditLog(c.Request.Context(), adminID, c.Query("action"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

// --- Actions ---------------------------------------------------------------

type reasonRequest struct {
	Reason string `json:"reason"`
}

func (h *AdminHandler) Freeze(c *gin.Context) {
	accountID, ok := h.accountParam(c)
	if !ok {
		return
	}
	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	account, err := h.admin.Freeze(c.Request.Context(), middleware.AdminID(c), accountID, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, account)
}

func (h *AdminHandler) Unfreeze(c *gin.Context) {
	accountID, ok := h.accountParam(c)
	if !ok {
		return
	}
	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	account, err := h.admin.Unfreeze(c.Request.Context(), middleware.AdminID(c), accountID, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, account)
}

type adjustRequest struct {
	Amount      string `json:"amount" binding:"required"`
	BalanceType string `json:"balance_type" binding:"required"`
	Reason      string `json:"reason"`
}

func (h *AdminHandler) Adjust(c *gin.Context) {
	accountID, ok := h.accountParam(c)
	if !ok {
		return
	}

	var req adjustRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Amount and balance type are required", "")
		return
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil {
		badRequest(c, "Enter a valid amount", "amount")
		return
	}

	tx, wallet, err := h.admin.Adjust(c.Request.Context(), service.AdjustInput{
		AdminID:     middleware.AdminID(c),
		AccountID:   accountID,
		Amount:      amount,
		BalanceType: req.BalanceType,
		Reason:      req.Reason,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"transaction": tx, "wallet": wallet})
}

func (h *AdminHandler) ApproveKYC(c *gin.Context) { h.reviewKYC(c, true) }
func (h *AdminHandler) RejectKYC(c *gin.Context)  { h.reviewKYC(c, false) }

func (h *AdminHandler) reviewKYC(c *gin.Context, approve bool) {
	submissionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid submission id", "")
		return
	}
	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	submission, err := h.admin.ReviewKYC(c.Request.Context(), middleware.AdminID(c),
		submissionID, approve, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, submission)
}

func (h *AdminHandler) accountParam(c *gin.Context) (uuid.UUID, bool) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid account id", "")
		return uuid.Nil, false
	}
	return accountID, true
}

// POST /admin/deposits/:id/resolve  {outcome: completed|failed, reason}
//
// Settles a stuck deposit's original row. A reason is required, like every
// other action that touches money; the outcome is the operator's statement of
// what Safaricom's records show.
func (h *AdminHandler) ResolveDeposit(c *gin.Context) {
	txID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "A valid transaction id is required", "")
		return
	}

	var req struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	tx, err := h.admin.ResolveDeposit(c.Request.Context(), middleware.AdminID(c), txID, req.Outcome, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, tx)
}

// GET /admin/payouts?filter=open|unknown|given_up|settled
//
// The operator's view of withdrawals whose payout has not resolved. 'unknown' is
// the one worth watching: each row is a customer whose balance was debited and
// about whom M-Pesa has said nothing either way.
func (h *AdminHandler) Payouts(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	result, err := h.admin.Payouts(c.Request.Context(), c.Query("filter"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

// POST /admin/payouts/:id/resolve  {outcome: paid|failed|never_received, reason}
//
// :id is the WITHDRAWAL's transaction id, matching the deposit resolve route, so
// an operator working from the transactions list uses the same identifier for
// both directions.
func (h *AdminHandler) ResolvePayout(c *gin.Context) {
	txID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "A valid transaction id is required", "")
		return
	}

	var req struct {
		Outcome string `json:"outcome"`
		Reason  string `json:"reason"`
	}
	_ = c.ShouldBindJSON(&req)

	tx, err := h.admin.ResolvePayout(c.Request.Context(), middleware.AdminID(c), txID, req.Outcome, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, tx)
}

// --- Automated trading -----------------------------------------------------

// POST /admin/bot/enable
//
// The way back from the kill switch. A reason is required, the same as it is for
// unfreezing an account: this is the restorative direction, and the restorative
// direction is where the explanation lives.
func (h *AdminHandler) EnableBots(c *gin.Context) {
	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.admin.EnableBots(c.Request.Context(), middleware.AdminID(c), req.Reason); err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"bot_enabled": true})
}

// GET /admin/bot/runs?status=live|stopped
//
// Paged, unlike the user's own list of runs: this one is every run on the
// platform and history does not stop growing.
func (h *AdminHandler) BotRuns(c *gin.Context) {
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	result, err := h.admin.ListBotRuns(c.Request.Context(), c.Query("status"), limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page, result.Pages = page, pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

// POST /admin/bot/runs/:id/stop
//
// Stops one run. A reason is required and recorded, like every other console
// action that reaches into a user's account.
func (h *AdminHandler) StopBotRun(c *gin.Context) {
	runID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "A valid run id is required", "")
		return
	}

	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	run, err := h.admin.StopBotRun(c.Request.Context(), middleware.AdminID(c), runID, req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// POST /admin/bot/kill
//
// Stops every live run and turns automated trading off, in one audited
// transaction. A reason is required, like every other action that touches money.
func (h *AdminHandler) KillBots(c *gin.Context) {
	var req reasonRequest
	_ = c.ShouldBindJSON(&req)

	stopped, err := h.admin.KillBots(c.Request.Context(), middleware.AdminID(c), req.Reason)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"runs_stopped": stopped, "bot_enabled": false})
}
