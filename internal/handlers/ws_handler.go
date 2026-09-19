package handlers

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/KotlinDevIanBore/tradederivplus/internal/middleware"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/ws"
)

type WSHandler struct {
	hub      *ws.Hub
	feed     *service.FeedService
	wallet   *service.WalletService
	log      *slog.Logger
	upgrader websocket.Upgrader
}

func NewWSHandler(hub *ws.Hub, feed *service.FeedService, wallet *service.WalletService, allowedOrigins []string, log *slog.Logger) *WSHandler {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, o := range allowedOrigins {
		allowed[o] = struct{}{}
	}

	return &WSHandler{
		hub:    hub,
		feed:   feed,
		wallet: wallet,
		log:    log,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				origin := r.Header.Get("Origin")
				if origin == "" {
					return true // non-browser client
				}
				_, ok := allowed[origin]
				return ok
			},
		},
	}
}

// Prices streams ticks for the requested symbols. Clients can change the set
// mid-connection by sending {"action":"subscribe","symbols":["R_75"]}.
func (h *WSHandler) Prices(c *gin.Context) {
	symbols := parseSymbols(c.Query("symbols"))

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Debug("price ws upgrade failed", "err", err)
		return
	}

	topics := make([]string, 0, len(symbols))
	for _, s := range symbols {
		topics = append(topics, service.PriceTopic(s))
	}

	client := h.hub.Register(conn, topics)
	client.SetTopicMapper(service.PriceTopic)

	// Send the last known price immediately so the chart has something to draw
	// before the next tick arrives.
	for _, s := range symbols {
		if tick, ok := h.feed.Latest(s); ok {
			client.Send("tick", tick)
		}
	}
}

// Signals pushes new bot output to every connected client.
func (h *WSHandler) Signals(c *gin.Context) {
	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Debug("signal ws upgrade failed", "err", err)
		return
	}
	h.hub.Register(conn, []string{service.SignalsTopic()})
}

// Account is the per-user event channel (spec G2). It pushes balance changes,
// deposit settlements, trade lifecycle events and KYC decisions for every
// account the user owns. This is what lets the deposit pending screen resolve
// without polling and tells the UI a position was auto-closed at its stop loss
// while the user was on another screen.
//
// There is no account_id parameter: the channel is the authenticated user's,
// so there is nothing for a caller to forge. Each payload names the account it
// concerns.
func (h *WSHandler) Account(c *gin.Context) {
	userID := middleware.UserID(c)
	if userID == uuid.Nil {
		fail(c, service.ErrForbidden)
		return
	}

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.log.Debug("account ws upgrade failed", "err", err)
		return
	}
	h.hub.Register(conn, []string{service.UserTopic(userID.String())})
}

func parseSymbols(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}
