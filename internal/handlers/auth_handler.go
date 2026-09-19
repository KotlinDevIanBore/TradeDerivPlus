package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type AuthHandler struct {
	auth *service.AuthService
	cfg  *config.Config
}

func NewAuthHandler(auth *service.AuthService, cfg *config.Config) *AuthHandler {
	return &AuthHandler{auth: auth, cfg: cfg}
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
// KYC gate (spec gap G9).
func (h *AuthHandler) Me(c *gin.Context) {
	user, err := h.auth.Me(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, user)
}
