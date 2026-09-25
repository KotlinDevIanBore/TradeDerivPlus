package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type AuthHandler struct {
	auth     *service.AuthService
	settings *service.SettingsService
	cfg      *config.Config
}

func NewAuthHandler(auth *service.AuthService, settings *service.SettingsService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{auth: auth, settings: settings, cfg: cfg}
}

// authResponse is what the client receives. The refresh token is deliberately
// absent: it travels as an httpOnly cookie instead, so it is never reachable
// from JavaScript.
type authResponse struct {
	User    any    `json:"user"`
	Account any    `json:"account,omitempty"`
	Access  string `json:"access_token"`
	Expires int    `json:"expires_in"`
}

// issue sets the refresh cookie and returns the body without the refresh token.
func (h *AuthHandler) issue(c *gin.Context, status int, result *service.AuthResult) {
	setRefreshCookie(c, h.cfg, userRefreshCookie, userRefreshPath,
		result.Tokens.RefreshToken, h.cfg.RefreshTokenTTL)

	body := authResponse{
		User:    result.User,
		Access:  result.Tokens.AccessToken,
		Expires: result.Tokens.ExpiresIn,
	}
	if result.Account != nil {
		body.Account = result.Account
	}
	c.JSON(status, body)
}

type registerRequest struct {
	FullName    string `json:"full_name"`
	PhoneNumber string `json:"phone_number" binding:"required"`
	Email       string `json:"email"`
	Password    string `json:"password" binding:"required"`
}

func (h *AuthHandler) Register(c *gin.Context) {
	var req registerRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Phone number and password are required", "")
		return
	}

	result, err := h.auth.Register(c.Request.Context(), service.RegisterInput{
		FullName:    req.FullName,
		PhoneNumber: req.PhoneNumber,
		Email:       req.Email,
		Password:    req.Password,
	})
	if err != nil {
		fail(c, err)
		return
	}
	h.issue(c, http.StatusCreated, result)
}

type loginRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
	Password    string `json:"password" binding:"required"`
}

func (h *AuthHandler) Login(c *gin.Context) {
	var req loginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Phone number and password are required", "")
		return
	}

	result, err := h.auth.Login(c.Request.Context(), req.PhoneNumber, req.Password)
	if err != nil {
		fail(c, err)
		return
	}
	h.issue(c, http.StatusOK, result)
}

// Refresh exchanges the refresh cookie for a new access token and rotates the
// cookie. It reads the cookie only: a token in the request body is ignored, so
// there is no path that accepts a refresh token from JavaScript.
func (h *AuthHandler) Refresh(c *gin.Context) {
	token := readCookie(c, userRefreshCookie)
	if token == "" {
		fail(c, service.ErrInvalidToken)
		return
	}

	tokens, err := h.auth.Refresh(c.Request.Context(), token)
	if err != nil {
		// A rejected refresh token is spent: clear it so the browser stops
		// presenting a credential that will never work again.
		clearRefreshCookie(c, h.cfg, userRefreshCookie, userRefreshPath)
		fail(c, err)
		return
	}

	setRefreshCookie(c, h.cfg, userRefreshCookie, userRefreshPath,
		tokens.RefreshToken, h.cfg.RefreshTokenTTL)
	c.JSON(http.StatusOK, gin.H{
		"access_token": tokens.AccessToken,
		"expires_in":   tokens.ExpiresIn,
	})
}

// Logout clears the refresh cookie. Without it a client can drop its in-memory
// access token but the browser keeps presenting a valid refresh credential.
func (h *AuthHandler) Logout(c *gin.Context) {
	clearRefreshCookie(c, h.cfg, userRefreshCookie, userRefreshPath)
	c.Status(http.StatusNoContent)
}

// Me backs the profile screen, the deposit phone prefill and the withdraw
// KYC gate (spec gap G9). It also carries digit_contracts_enabled so the web
// app can swap the trading surface without a separate config endpoint.
func (h *AuthHandler) Me(c *gin.Context) {
	user, err := h.auth.Me(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		fail(c, err)
		return
	}
	ctx := c.Request.Context()
	withdrawals, err := h.settings.WithdrawalsEnabled(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	digit, err := h.settings.DigitContractsEnabled(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	// Effective for this user, not the raw global: the allowlist is part of the
	// answer, and the client should not have to combine two flags to know whether
	// to show the control.
	botEnabled, err := h.settings.BotEnabledFor(ctx, middleware.UserID(c))
	if err != nil {
		fail(c, err)
		return
	}
	botRealMoney, err := h.settings.BotRealMoneyEnabled(ctx)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, meResponse{
		User:                  *user,
		DigitContractsEnabled: digit,
		WithdrawalsEnabled:    withdrawals,
		BotEnabled:            botEnabled,
		BotRealMoneyEnabled:   botRealMoney,
		DigitMaxStake:         decimal.NewFromFloat(h.cfg.DigitMaxStake).StringFixed(2),
	})
}

// meResponse is the user profile plus the runtime flags the web app branches on.
type meResponse struct {
	models.User
	DigitContractsEnabled bool `json:"digit_contracts_enabled"`
	// WithdrawalsEnabled is false until the M-Pesa payout exists. The wallet
	// screen reads it so it can say so plainly rather than offer a button that
	// takes money and pays nobody.
	WithdrawalsEnabled bool `json:"withdrawals_enabled"`
	// BotEnabled is already narrowed by the allowlist, so it answers "may this
	// user start a run" rather than "is the feature switched on somewhere".
	BotEnabled bool `json:"bot_enabled"`
	// BotRealMoneyEnabled is what keeps v1 demo-only; the ticket reads it to
	// explain itself on a real account rather than failing at submit.
	BotRealMoneyEnabled bool `json:"bot_real_money_enabled"`
	// DigitMaxStake is the server's per-contract ceiling, sent so a ticket can
	// state it and refuse before a round trip rather than after one. A string,
	// like every other amount, so the client parses it the same way.
	DigitMaxStake string `json:"digit_max_stake"`
}
