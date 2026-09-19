package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

type TradeHandler struct{ trading *service.TradingService }

func NewTradeHandler(trading *service.TradingService) *TradeHandler {
	return &TradeHandler{trading: trading}
}

type openTradeRequest struct {
	AccountID  string  `json:"account_id" binding:"required"`
	Symbol     string  `json:"symbol" binding:"required"`
	Direction  string  `json:"direction" binding:"required"`
	Stake      string  `json:"stake" binding:"required"`
	StopLoss   *string `json:"stop_loss"`
	TakeProfit *string `json:"take_profit"`
}

func (h *TradeHandler) Open(c *gin.Context) {
	var req openTradeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "Symbol, direction and stake are required", "")
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

	sl, err := optionalDecimal(req.StopLoss)
	if err != nil {
		badRequest(c, "Enter a valid stop loss", "stop_loss")
		return
	}
	tp, err := optionalDecimal(req.TakeProfit)
	if err != nil {
		badRequest(c, "Enter a valid take profit", "take_profit")
		return
	}

	trade, err := h.trading.Open(c.Request.Context(), service.OpenTradeInput{
		UserID:     middleware.UserID(c),
		AccountID:  accountID,
		SymbolCode: req.Symbol,
		Direction:  req.Direction,
		Stake:      stake,
		StopLoss:   sl,
		TakeProfit: tp,
	})
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusCreated, trade)
}

func (h *TradeHandler) Close(c *gin.Context) {
	tradeID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		badRequest(c, "Invalid trade id", "")
		return
	}

	trade, err := h.trading.Close(c.Request.Context(), middleware.UserID(c), tradeID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, trade)
}

func (h *TradeHandler) List(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}

	status := c.Query("status") // "" means both tabs' data
	limit := clampInt(c.Query("limit"), 25, 1, 100)
	page, offset := resolvePage(c, limit)

	result, err := h.trading.List(c.Request.Context(), middleware.UserID(c), accountID, status, limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	result.Page = page
	result.Pages = pageCount(result.Total, limit)
	c.JSON(http.StatusOK, result)
}

// Summary backs the Portfolio screen's three tiles in one call.
func (h *TradeHandler) Summary(c *gin.Context) {
	accountID, err := uuid.Parse(c.Query("account_id"))
	if err != nil {
		badRequest(c, "account_id is required", "")
		return
	}

	summary, err := h.trading.Summary(c.Request.Context(), middleware.UserID(c), accountID)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, summary)
}

func optionalDecimal(raw *string) (*decimal.Decimal, error) {
	if raw == nil || *raw == "" {
		return nil, nil
	}
	d, err := decimal.NewFromString(*raw)
	if err != nil {
		return nil, err
	}
	return &d, nil
}
