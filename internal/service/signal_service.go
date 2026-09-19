package service

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

const (
	emaFastPeriod = 9
	emaSlowPeriod = 21
	rsiPeriod     = 14

	// A symbol will not produce a second signal inside this window, so the
	// Signals screen does not fill up with near-duplicates.
	signalCooldown = 8 * time.Minute

	// Below this the setup is not worth showing to a user.
	minConfidence = 45.0

	strategyName = "ema_rsi"
)

// SignalService runs the strategy on a schedule and publishes what it finds.
type SignalService struct {
	store *repository.Store
	feed  *FeedService
	hub   Broadcaster
	cfg   *config.Config
	log   *slog.Logger
}

func NewSignalService(store *repository.Store, feed *FeedService, hub Broadcaster, cfg *config.Config, log *slog.Logger) *SignalService {
	return &SignalService{store: store, feed: feed, hub: hub, cfg: cfg, log: log}
}

// Run evaluates every configured symbol on a ticker until ctx is cancelled.
func (s *SignalService) Run(ctx context.Context) {
	// Give the feed time to fill enough history for the slow EMA.
	warmup := time.NewTimer(45 * time.Second)
	defer warmup.Stop()

	select {
	case <-ctx.Done():
		return
	case <-warmup.C:
	}

	t := time.NewTicker(s.cfg.SignalInterval)
	defer t.Stop()

	s.evaluateAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.evaluateAll(ctx)
		}
	}
}

func (s *SignalService) evaluateAll(ctx context.Context) {
	for _, code := range s.cfg.SignalSymbols {
		if err := s.evaluate(ctx, code); err != nil && !errors.Is(err, errNoSetup) {
			s.log.Debug("signal evaluation skipped", "symbol", code, "reason", err)
		}
	}
}

var errNoSetup = errors.New("no setup")

// evaluate runs EMA crossover confirmed by RSI on the recent tick series.
//
//	buy  when fast EMA is above slow EMA and RSI has room to run (< 70)
//	sell when fast EMA is below slow EMA and RSI is not already oversold (> 30)
//
// Confidence blends how far apart the EMAs are with how far RSI sits from the
// exhausted end of its range.
func (s *SignalService) evaluate(ctx context.Context, code string) error {
	series := s.feed.Series(code, 10*time.Minute)
	if len(series) < emaSlowPeriod+rsiPeriod {
		return errNoSetup
	}

	values := make([]float64, len(series))
	for i, d := range series {
		values[i], _ = d.Float64()
	}

	fast := EMA(values, emaFastPeriod)
	slow := EMA(values, emaSlowPeriod)
	if len(fast) < 2 || len(slow) < 2 {
		return errNoSetup
	}

	f := fast[len(fast)-1]
	sl := slow[len(slow)-1]
	rsi := RSI(values, rsiPeriod)
	last := values[len(values)-1]
	if last == 0 {
		return errNoSetup
	}

	var direction string
	switch {
	case f > sl && rsi < 70:
		direction = models.DirectionBuy
	case f < sl && rsi > 30:
		direction = models.DirectionSell
	default:
		return errNoSetup
	}

	// Separation as a fraction of price, scaled so a 0.15% gap reads as full
	// strength on these instruments.
	separation := math.Abs(f-sl) / last
	trendScore := math.Min(separation/0.0015, 1.0)

	// RSI score: strongest when RSI still has room in the trade's direction.
	var rsiScore float64
	if direction == models.DirectionBuy {
		rsiScore = math.Min(math.Max((70-rsi)/40, 0), 1)
	} else {
		rsiScore = math.Min(math.Max((rsi-30)/40, 0), 1)
	}

	confidence := 40 + 45*trendScore + 15*rsiScore
	if confidence < minConfidence {
		return errNoSetup
	}
	confidence = math.Min(confidence, 97)

	symbol, err := s.store.Symbols.GetByCode(ctx, code)
	if err != nil {
		return err
	}

	// Skip if the last signal for this symbol is recent and points the same way.
	latest, err := s.store.Signals.LatestForSymbol(ctx, code)
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		return err
	}
	if latest != nil && latest.Direction == direction && time.Since(latest.CreatedAt) < signalCooldown {
		return errNoSetup
	}

	// Size the levels off recent volatility: 2 sigma stop, 3 sigma target,
	// which keeps the reward-to-risk at 1.5 regardless of instrument.
	sigma := StdDev(values[maxInt(0, len(values)-60):])
	if sigma <= 0 {
		sigma = last * 0.0005
	}
	entry := decimal.NewFromFloat(last)
	stopDist := decimal.NewFromFloat(sigma * 2)
	profitDist := decimal.NewFromFloat(sigma * 3)

	var stopLoss, takeProfit decimal.Decimal
	if direction == models.DirectionBuy {
		stopLoss = entry.Sub(stopDist)
		takeProfit = entry.Add(profitDist)
	} else {
		stopLoss = entry.Add(stopDist)
		takeProfit = entry.Sub(profitDist)
	}

	signal := &models.Signal{
		SymbolID:   symbol.ID,
		Direction:  direction,
		Confidence: decimal.NewFromFloat(confidence).Round(2),
		EntryPrice: entry.Round(int32(symbol.PipSize)),
		StopLoss:   ptr(stopLoss.Round(int32(symbol.PipSize))),
		TakeProfit: ptr(takeProfit.Round(int32(symbol.PipSize))),
		Strategy:   strategyName,
		SymbolCode: symbol.Code,
		SymbolName: symbol.DisplayName,
		PipSize:    symbol.PipSize,
	}
	if err := s.store.Signals.Insert(ctx, signal); err != nil {
		return err
	}

	s.log.Info("signal published", "symbol", code, "direction", direction,
		"confidence", signal.Confidence.String())
	s.hub.Broadcast(SignalsTopic(), "signal", signal)
	return nil
}

// List backs GET /signals. An empty symbolCode returns the newest signals
// across all symbols, which is what the Home card asks for (spec gap G3).
func (s *SignalService) List(ctx context.Context, symbolCode string, limit int) ([]models.Signal, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return s.store.Signals.List(ctx, symbolCode, limit)
}

func ptr[T any](v T) *T { return &v }

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
