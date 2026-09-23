package service

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/derivclient"
)

// Broadcaster is the slice of the WebSocket hub that services need. Keeping it
// an interface means the service layer never imports the transport.
type Broadcaster interface {
	Broadcast(topic, msgType string, payload any)
}

// PriceTopic and the other topic helpers are the single naming authority for
// hub topics, shared by the services that publish and the handlers that
// subscribe clients.
func PriceTopic(symbolCode string) string { return "prices:" + symbolCode }
func SignalsTopic() string                { return "signals" }

// UserTopic is the per-user channel. It is keyed by user rather than by
// account so the client holds one socket for the whole session: switching
// between demo and real no longer tears down and rebuilds a connection, and
// an event for the account the user is not currently looking at still
// arrives. Every payload carries the account it refers to, so the client
// routes on that.
func UserTopic(userID string) string { return "user:" + userID }

// tickBufferSize is how much history the chart can draw on reconnect. At the
// mock feed's 2 ticks/sec this is roughly the last 5 minutes (spec gap G5).
const tickBufferSize = 600

// TickObserver is notified of every tick before it is broadcast. The trading
// service registers one to run stop-loss and take-profit checks.
type TickObserver func(tick models.Tick)

// FeedService owns the upstream market connection, keeps the latest price and
// a rolling history per symbol, and fans ticks out to the hub.
type FeedService struct {
	feed derivclient.Feed
	hub  Broadcaster
	log  *slog.Logger

	mu      sync.RWMutex
	latest  map[string]models.Tick
	history map[string][]models.Tick

	// pipSize is the display precision per symbol. Ticks are rounded before
	// they are stored or broadcast so the UI never renders a 16-digit float
	// and a price never jitters in a digit no one trades on.
	pipSize map[string]int32

	obsMu     sync.RWMutex
	observers []TickObserver
}

func NewFeedService(feed derivclient.Feed, hub Broadcaster, log *slog.Logger) *FeedService {
	return &FeedService{
		feed:    feed,
		hub:     hub,
		log:     log,
		latest:  make(map[string]models.Tick),
		history: make(map[string][]models.Tick),
		pipSize: make(map[string]int32),
	}
}

// SetPrecision registers the display precision for each symbol. Call it once
// at boot with the seeded symbol list.
func (s *FeedService) SetPrecision(symbols []models.Symbol) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sym := range symbols {
		s.pipSize[sym.Code] = int32(sym.PipSize)
	}
}

func (s *FeedService) OnTick(o TickObserver) {
	s.obsMu.Lock()
	s.observers = append(s.observers, o)
	s.obsMu.Unlock()
}

// Run starts the upstream feed and the fan-out loop. It returns when ctx ends.
func (s *FeedService) Run(ctx context.Context) {
	go s.feed.Run(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case t, ok := <-s.feed.Ticks():
			if !ok {
				return
			}
			s.handle(models.Tick{
				Symbol: t.Symbol,
				Price:  s.round(t.Symbol, decimal.NewFromFloat(t.Price)),
				Epoch:  t.Epoch,
			})
		}
	}
}

// round applies a symbol's pip size, defaulting to 4 places for an unknown
// symbol rather than leaking raw float noise.
func (s *FeedService) round(symbolCode string, price decimal.Decimal) decimal.Decimal {
	s.mu.RLock()
	places, ok := s.pipSize[symbolCode]
	s.mu.RUnlock()
	if !ok {
		places = 4
	}
	return price.Round(places)
}

func (s *FeedService) handle(tick models.Tick) {
	s.mu.Lock()
	s.latest[tick.Symbol] = tick
	buf := append(s.history[tick.Symbol], tick)
	if len(buf) > tickBufferSize {
		buf = buf[len(buf)-tickBufferSize:]
	}
	s.history[tick.Symbol] = buf
	s.mu.Unlock()

	s.hub.Broadcast(PriceTopic(tick.Symbol), "tick", tick)

	s.obsMu.RLock()
	observers := make([]TickObserver, len(s.observers))
	copy(observers, s.observers)
	s.obsMu.RUnlock()

	for _, o := range observers {
		o(tick)
	}
}

// InjectTick feeds a tick in as if it had arrived from upstream, running the
// full handle path including observers. Tests use it to pin a price so a
// position's outcome is deterministic instead of at the mercy of a random walk.
func (s *FeedService) InjectTick(symbolCode string, price decimal.Decimal) {
	s.InjectTickAt(symbolCode, price, time.Now().Unix())
}

// InjectTickAt is InjectTick with an explicit epoch so digit-contract tests can
// advance "next tick" without waiting a wall-clock second.
func (s *FeedService) InjectTickAt(symbolCode string, price decimal.Decimal, epoch int64) {
	s.handle(models.Tick{
		Symbol: symbolCode,
		Price:  s.round(symbolCode, price),
		Epoch:  epoch,
	})
}

// InjectTickQuiet updates latest/history without running observers — used to
// simulate a missed tick handler while leaving a settlement price for the sweep.
func (s *FeedService) InjectTickQuiet(symbolCode string, price decimal.Decimal, epoch int64) {
	tick := models.Tick{
		Symbol: symbolCode,
		Price:  s.round(symbolCode, price),
		Epoch:  epoch,
	}
	s.mu.Lock()
	s.latest[tick.Symbol] = tick
	buf := append(s.history[tick.Symbol], tick)
	if len(buf) > tickBufferSize {
		buf = buf[len(buf)-tickBufferSize:]
	}
	s.history[tick.Symbol] = buf
	s.mu.Unlock()
}

// Price returns the latest tick for a symbol. ok is false until the first tick
// arrives, which is why the order ticket stays disabled on a cold start.
func (s *FeedService) Price(symbolCode string) (decimal.Decimal, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.latest[symbolCode]
	if !ok {
		return decimal.Zero, false
	}
	return t.Price, true
}

func (s *FeedService) Latest(symbolCode string) (models.Tick, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.latest[symbolCode]
	return t, ok
}

// History returns up to `limit` most recent ticks so a chart can render
// immediately instead of waiting for live ticks to accumulate.
func (s *FeedService) History(symbolCode string, limit int) []models.Tick {
	s.mu.RLock()
	defer s.mu.RUnlock()

	buf := s.history[symbolCode]
	if limit > 0 && len(buf) > limit {
		buf = buf[len(buf)-limit:]
	}
	out := make([]models.Tick, len(buf))
	copy(out, buf)
	return out
}

// Series returns the ticks from the last `window`, used by the signal bot's
// indicator calculations and by the chart's range toggle.
func (s *FeedService) Series(symbolCode string, window time.Duration) []decimal.Decimal {
	cutoff := time.Now().Add(-window).Unix()

	s.mu.RLock()
	defer s.mu.RUnlock()

	buf := s.history[symbolCode]
	out := make([]decimal.Decimal, 0, len(buf))
	for _, t := range buf {
		if t.Epoch >= cutoff {
			out = append(out, t.Price)
		}
	}
	return out
}

// Prices returns the latest tick for every symbol that has one, so the symbol
// picker can show a price next to each row without opening N subscriptions.
func (s *FeedService) Prices() map[string]models.Tick {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]models.Tick, len(s.latest))
	for k, v := range s.latest {
		out[k] = v
	}
	return out
}
