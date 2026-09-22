package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type DigitHandler struct{ digits *service.DigitService }

func NewDigitHandler(digits *service.DigitService) *DigitHandler {
	return &DigitHandler{digits: digits}
}

type openDigitRequest struct {
	AccountID    string          `json:"account_id" binding:"required"`
	Symbol       string          `json:"symbol" binding:"required"`
	ContractType string          `json:"contract_type" binding:"required"`
	Selection    jsonSelection   `json:"selection" binding:"required"`
	Stake        string          `json:"stake" binding:"required"`
}

// jsonSelection mirrors models.DigitSelection for binding.
type jsonSelection struct {
	Mode    string `json:"mode"`
	Digit   *int   `json:"digit"`
	Parity  string `json:"parity"`
	Side    string `json:"side"`
	Barrier *int   `json:"barrier"`
}

func (h *DigitHandler) Open(c *gin.Context) {
	var req openDigitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Account, symbol, contract_type, selection and stake are required", "")
		return
	}
	accountID, err := uuid.Parse(req.AccountID)
	if err != nil {
		badRequest(c, "Invalid account id", "")
		return
	}
	stake, err := decimal.NewFromString(req.Stake)
	if err != nil {
		badRequest(c, "Enter a valid stake", "stake")
		return
	}

	contract, err := h.digits.Open(c.Request.Context(), service.OpenDigitInput{
		UserID:       middleware.UserID(c),
		AccountID:    accountID,
		SymbolCode:   req.Symbol,
		ContractType: req.ContractType,
		Selection: models.DigitSelection{
			Mode: req.Selection.Mode, Digit: req.Selection.Digit,
			Parity: req.Selection.Parity, Side: req.Selection.Side, Barrier: req.Selection.Barrier,
		},
		Stake: stake,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, contract)
}

func (h *DigitHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid contract id", "")
		return
	}
	contract, err := h.digits.Get(c.Request.Context(), middleware.UserID(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, contract)
}

func (h *DigitHandler) List(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}
	status := c.Query("status")
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	items, total, err := h.digits.List(c.Request.Context(), middleware.UserID(c), accountID, status, limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"items": items,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

func (h *DigitHandler) Summary(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}
	summary, err := h.digits.Summary(c.Request.Context(), middleware.UserID(c), accountID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}
