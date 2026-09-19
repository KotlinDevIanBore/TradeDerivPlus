// Package ws is a generic topic-based broadcast hub. It knows nothing about
// prices, signals or accounts -- callers pick the topic strings.
package ws

import (
	"encoding/json"
	"log/slog"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 4096
	sendBuffer     = 256
)

// Envelope is the shape of every message pushed to a client.
type Envelope struct {
	Type    string `json:"type"`
	Payload any    `json:"payload"`
}

// Client is one WebSocket connection with a set of topic subscriptions.
type Client struct {
	hub    *Hub
	conn   *websocket.Conn
	send   chan []byte
	topics map[string]struct{}
	mapper TopicMapper
	mu     sync.RWMutex
	closed bool
}

type Hub struct {
	mu      sync.RWMutex
	clients map[*Client]struct{}
	log     *slog.Logger
}

func NewHub(log *slog.Logger) *Hub {
	return &Hub{clients: make(map[*Client]struct{}), log: log}
}

// Register wires a raw connection into the hub and starts its pumps.
// topics is the initial subscription set.
func (h *Hub) Register(conn *websocket.Conn, topics []string) *Client {
	c := &Client{hub: h, conn: conn, send: make(chan []byte, sendBuffer), topics: map[string]struct{}{}}
	for _, t := range topics {
		c.topics[t] = struct{}{}
	}
	h.mu.Lock()
	h.clients[c] = struct{}{}
	h.mu.Unlock()

	go c.writePump()
	go c.readPump()
	return c
}

// Broadcast sends payload to every client subscribed to topic.
// Slow clients are dropped rather than allowed to block the fan-out.
func (h *Hub) Broadcast(topic, msgType string, payload any) {
	b, err := json.Marshal(Envelope{Type: msgType, Payload: payload})
	if err != nil {
		h.log.Error("ws marshal failed", "type", msgType, "err", err)
		return
	}

	h.mu.RLock()
	targets := make([]*Client, 0, len(h.clients))
	for c := range h.clients {
		if c.subscribed(topic) {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.send <- b:
		default:
			// Buffer full: this client cannot keep up with the tick rate.
			h.log.Warn("ws client too slow, dropping", "topic", topic)
			h.unregister(c)
		}
	}
}

func (h *Hub) unregister(c *Client) {
	h.mu.Lock()
	if _, ok := h.clients[c]; ok {
		delete(h.clients, c)
		c.mu.Lock()
		if !c.closed {
			c.closed = true
			close(c.send)
		}
		c.mu.Unlock()
	}
	h.mu.Unlock()
}

func (h *Hub) ClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

func (c *Client) subscribed(topic string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	_, ok := c.topics[topic]
	return ok
}

// Subscribe/Unsubscribe let a client change topics mid-connection, which the
// price socket uses when the user switches symbol without reconnecting.
func (c *Client) Subscribe(topics ...string) {
	c.mu.Lock()
	for _, t := range topics {
		c.topics[t] = struct{}{}
	}
	c.mu.Unlock()
}

func (c *Client) Unsubscribe(topics ...string) {
	c.mu.Lock()
	for _, t := range topics {
		delete(c.topics, t)
	}
	c.mu.Unlock()
}

func (c *Client) Send(msgType string, payload any) {
	b, err := json.Marshal(Envelope{Type: msgType, Payload: payload})
	if err != nil {
		return
	}
	c.mu.RLock()
	closed := c.closed
	c.mu.RUnlock()
	if closed {
		return
	}
	select {
	case c.send <- b:
	default:
	}
}

// inbound is the only client->server message we accept: subscription changes.
type inbound struct {
	Action  string   `json:"action"` // subscribe | unsubscribe
	Symbols []string `json:"symbols"`
}

// OnMessage is set by the handler to translate symbol names into topic names.
type TopicMapper func(symbol string) string

func (c *Client) SetTopicMapper(m TopicMapper) { c.mapper = m }

func (c *Client) readPump() {
	defer func() {
		c.hub.unregister(c)
		_ = c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		return c.conn.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, raw, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.hub.log.Debug("ws read error", "err", err)
			}
			return
		}
		var in inbound
		if json.Unmarshal(raw, &in) != nil || c.mapper == nil {
			continue
		}
		topics := make([]string, 0, len(in.Symbols))
		for _, s := range in.Symbols {
			topics = append(topics, c.mapper(s))
		}
		switch in.Action {
		case "subscribe":
			c.Subscribe(topics...)
		case "unsubscribe":
			c.Unsubscribe(topics...)
		}
	}
}

func (c *Client) writePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		_ = c.conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.send:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			if err := c.conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
