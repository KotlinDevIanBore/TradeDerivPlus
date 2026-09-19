package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type AccountHandler struct{ wallet *service.WalletService }

func NewAccountHandler(wallet *service.WalletService) *AccountHandler {
	return &AccountHandler{wallet: wallet}
}

func (h *AccountHandler) List(c *gin.Context) {
	accounts, err := h.wallet.ListAccounts(c.Request.Context(), middleware.UserID(c))
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": accounts})
}

type createAccountRequest struct {
	AccountType string `json:"account_type" binding:"required"`
}

func (h *AccountHandler) Create(c *gin.Context) {
	var req createAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Account type is required", "account_type")
		return
	}

	account, err := h.wallet.CreateAccount(c.Request.Context(), middleware.UserID(c), req.AccountType)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, account)
}

func (h *AccountHandler) Wallet(c *gin.Context) {
	accountID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid account id", "")
		return
	}

	w, err := h.wallet.GetWallet(c.Request.Context(), middleware.UserID(c), accountID)
	if err != nil {
		fail(c, err)
		return
	}
	// Wallet serialises its own derived total.
	c.JSON(http.StatusOK, w)
}
