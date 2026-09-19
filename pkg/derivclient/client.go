// Package derivclient speaks the raw Deriv WebSocket protocol and emits ticks
// on a channel. It knows nothing about our database or HTTP layer.
package derivclient

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// Tick is the neutral shape both the live client and the mock emit.
type Tick struct {
	Symbol string
	Price  float64
	Epoch  int64
}

type Client struct {
	url     string
	appID   string
	symbols []string
	log     *slog.Logger

	out chan Tick
	mu  sync.Mutex
}

func New(wsURL, appID string, symbols []string, log *slog.Logger) *Client {
	return &Client{
		url:     wsURL,
		appID:   appID,
		symbols: symbols,
		log:     log,
		out:     make(chan Tick, 1024),
	}
}

func (c *Client) Ticks() <-chan Tick { return c.out }

// Run connects and reconnects with exponential backoff until ctx is cancelled.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	const maxBackoff = 30 * time.Second

	for {
		if ctx.Err() != nil {
			return
		}
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		c.log.Warn("deriv feed disconnected, retrying", "err", err, "in", backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, maxBackoff)
	}
}

func (c *Client) session(ctx context.Context) error {
	url := fmt.Sprintf("%s?app_id=%s", c.url, c.appID)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, url, nil)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close()

	for _, s := range c.symbols {
		req := map[string]any{"ticks": s, "subscribe": 1}
		if err := conn.WriteJSON(req); err != nil {
			return fmt.Errorf("subscribe %s: %w", s, err)
		}
	}
	c.log.Info("deriv feed connected", "symbols", len(c.symbols))

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var msg struct {
			MsgType string `json:"msg_type"`
			Tick    struct {
				Symbol string  `json:"symbol"`
				Quote  float64 `json:"quote"`
				Epoch  int64   `json:"epoch"`
			} `json:"tick"`
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &msg) != nil {
			continue
		}
		if msg.Error != nil {
			c.log.Warn("deriv feed error frame", "msg", msg.Error.Message)
			continue
		}
		if msg.MsgType != "tick" || msg.Tick.Symbol == "" {
			continue
		}
		select {
		case c.out <- Tick{Symbol: msg.Tick.Symbol, Price: msg.Tick.Quote, Epoch: msg.Tick.Epoch}:
		default:
			// Consumer is behind; dropping a tick is correct, queuing is not.
		}
	}
}

// MockClient generates a plausible random walk per symbol so the whole stack
// (chart, PnL, SL/TP, signal bot) can run without network access.
type MockClient struct {
	symbols []string
	out     chan Tick
	log     *slog.Logger
}

func NewMock(symbols []string, log *slog.Logger) *MockClient {
	return &MockClient{symbols: symbols, out: make(chan Tick, 1024), log: log}
}

func (m *MockClient) Ticks() <-chan Tick { return m.out }

func (m *MockClient) Run(ctx context.Context) {
	m.log.Info("using MOCK market feed", "symbols", len(m.symbols))

	type state struct {
		price float64
		vol   float64
		// boom/crash spike behaviour
		spikeEvery int
		spikeSize  float64
		tickN      int
	}

	states := make(map[string]*state, len(m.symbols))
	for _, s := range m.symbols {
		st := &state{price: 1000, vol: 0.0008}
		switch {
		case s == "EURUSD":
			st.price, st.vol = 1.08540, 0.00012
		case s == "GBPUSD":
			st.price, st.vol = 1.27310, 0.00014
		case s == "USDJPY":
			st.price, st.vol = 157.220, 0.00012
		case len(s) > 2 && s[:2] == "R_":
			// Volatility index: number in the code is the annualised vol.
			st.price = 4200 + rand.Float64()*800
			st.vol = 0.0004
		case s == "BOOM500", s == "BOOM1000":
			st.price, st.vol = 9500+rand.Float64()*500, 0.00025
			st.spikeEvery, st.spikeSize = 500, 0.012
			if s == "BOOM1000" {
				st.spikeEvery = 1000
			}
		case s == "CRASH500", s == "CRASH1000":
			st.price, st.vol = 6200+rand.Float64()*400, 0.00025
			st.spikeEvery, st.spikeSize = 500, -0.012
			if s == "CRASH1000" {
				st.spikeEvery = 1000
			}
		}
		states[s] = st
	}

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now().Unix()
			for sym, st := range states {
				st.tickN++
				drift := (rand.Float64()*2 - 1) * st.vol
				// Boom pays out in rare upward spikes, crash in downward ones.
				if st.spikeEvery > 0 && rand.Intn(st.spikeEvery) == 0 {
					drift += st.spikeSize
				}
				st.price *= 1 + drift
				if st.price <= 0 || math.IsNaN(st.price) {
					st.price = 1000
				}
				select {
				case m.out <- Tick{Symbol: sym, Price: st.price, Epoch: now}:
				default:
				}
			}
		}
	}
}

// Feed is what the service layer depends on, so live and mock are swappable.
type Feed interface {
	Run(ctx context.Context)
	Ticks() <-chan Tick
}

func min(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}
