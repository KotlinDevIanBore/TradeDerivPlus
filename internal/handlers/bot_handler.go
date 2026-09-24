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

type BotHandler struct{ bots *service.BotService }

func NewBotHandler(bots *service.BotService) *BotHandler { return &BotHandler{bots: bots} }

// startRunRequest mirrors the digit open request: money as strings, never JSON
// numbers, so a stake cannot lose precision on the way in.
type startRunRequest struct {
	AccountID    string        `json:"account_id" binding:"required"`
	Symbol       string        `json:"symbol" binding:"required"`
	ContractType string        `json:"contract_type" binding:"required"`
	Selection    jsonSelection `json:"selection" binding:"required"`
	Stake        string        `json:"stake" binding:"required"`
	IntervalMS   int           `json:"interval_ms" binding:"required"`
	Limits       limitsRequest `json:"limits"`
}

// limitsRequest is the run's stop conditions. Every field but stop_loss is
// optional; stop_loss is required because a run with no floor is not something
// this product offers, and the service refuses one.
type limitsRequest struct {
	StopLoss             string `json:"stop_loss"`
	TakeProfit           string `json:"take_profit"`
	MaxConsecutiveLosses *int   `json:"max_consecutive_losses"`
	MaxContracts         *int   `json:"max_contracts"`
	MaxDurationSeconds   *int   `json:"max_duration_s"`
}

// POST /bot/runs
func (h *BotHandler) Start(c *gin.Context) {
	var req startRunRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Account, symbol, contract_type, selection, stake and interval_ms are required", "")
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

	limits, err := parseLimits(req.Limits)
	if err != nil {
		fail(c, err)
		return
	}

	run, err := h.bots.StartRun(c.Request.Context(), service.StartRunInput{
		UserID:       middleware.UserID(c),
		AccountID:    accountID,
		SymbolCode:   req.Symbol,
		ContractType: req.ContractType,
		Selection: models.DigitSelection{
			Mode: req.Selection.Mode, Digit: req.Selection.Digit,
			Parity: req.Selection.Parity, Side: req.Selection.Side, Barrier: req.Selection.Barrier,
		},
		Stake:      stake,
		IntervalMS: req.IntervalMS,
		Limits:     limits,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, run)
}

func parseLimits(in limitsRequest) (models.BotLimitsConfig, error) {
	out := models.BotLimitsConfig{
		MaxConsecutiveLosses: in.MaxConsecutiveLosses,
		MaxContracts:         in.MaxContracts,
		MaxDurationSeconds:   in.MaxDurationSeconds,
	}
	if in.StopLoss == "" {
		return out, service.ValidationFor("stop_loss", "Set a stop loss so the run knows when to stop")
	}
	sl, err := decimal.NewFromString(in.StopLoss)
	if err != nil {
		return out, service.ValidationFor("stop_loss", "Enter a valid stop loss")
	}
	out.StopLoss = sl

	// Absent and "0" mean different things: absent is "no take profit", which is
	// allowed, while a zero the user typed is a mistake the service will name.
	if in.TakeProfit != "" {
		tp, err := decimal.NewFromString(in.TakeProfit)
		if err != nil {
			return out, service.ValidationFor("take_profit", "Enter a valid take profit")
		}
		out.TakeProfit = &tp
	}
	return out, nil
}

// POST /bot/runs/:id/stop
//
// Idempotent: stopping an already-stopped run returns it with 200 rather than an
// error, because the caller's intent is already satisfied and a red error on a
// second tap would only confuse.
func (h *BotHandler) Stop(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid run id", "")
		return
	}
	run, err := h.bots.StopRun(c.Request.Context(), middleware.UserID(c), id, models.BotStopUser)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// GET /bot/runs/:id
func (h *BotHandler) Get(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid run id", "")
		return
	}
	run, err := h.bots.Get(c.Request.Context(), middleware.UserID(c), id)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, run)
}

// GET /bot/runs?account_id=&status=live|stopped
func (h *BotHandler) List(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	items, total, err := h.bots.List(c.Request.Context(), middleware.UserID(c), accountID, c.Query("status"), limit, offset)
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
