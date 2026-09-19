package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

// MarketHandler serves the instrument list, tick history and signals.
type MarketHandler struct {
	store   *repository.Store
	feed    *service.FeedService
	signals *service.SignalService
}

func NewMarketHandler(store *repository.Store, feed *service.FeedService, signals *service.SignalService) *MarketHandler {
	return &MarketHandler{store: store, feed: feed, signals: signals}
}

// Symbols returns every tradable instrument with its latest price attached, so
// the symbol picker can render prices without opening N subscriptions.
func (h *MarketHandler) Symbols(c *gin.Context) {
	symbols, err := h.store.Symbols.List(c.Request.Context(), false)
	if err != nil {
		fail(c, err)
		return
	}

	prices := h.feed.Prices()
	items := make([]gin.H, 0, len(symbols))
	for _, s := range symbols {
		item := gin.H{
			"id":           s.ID,
			"code":         s.Code,
			"display_name": s.DisplayName,
			"category":     s.Category,
			"pip_size":     s.PipSize,
			"is_active":    s.IsActive,
		}
		if t, ok := prices[s.Code]; ok {
			item["price"] = t.Price
			item["epoch"] = t.Epoch
		}
		items = append(items, item)
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}

// Ticks returns buffered price history so a chart draws immediately instead of
// waiting for live ticks to accumulate (spec gap G5).
func (h *MarketHandler) Ticks(c *gin.Context) {
	code := c.Param("code")
	minutes := clampInt(c.Query("minutes"), 5, 1, 30)

	series := h.feed.History(code, 0)
	cutoff := time.Now().Add(-time.Duration(minutes) * time.Minute).Unix()

	out := make([]gin.H, 0, len(series))
	for _, t := range series {
		if t.Epoch >= cutoff {
			out = append(out, gin.H{"price": t.Price, "epoch": t.Epoch})
		}
	}
	c.JSON(http.StatusOK, gin.H{"symbol": code, "items": out})
}

// Signals returns recent bot output. An absent `symbol` means newest across all
// symbols, which is what the Home card asks for (spec gap G3).
func (h *MarketHandler) Signals(c *gin.Context) {
	symbol := c.Query("symbol")
	limit := clampInt(c.Query("limit"), 20, 1, 100)

	items, err := h.signals.List(c.Request.Context(), symbol, limit)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items})
}
