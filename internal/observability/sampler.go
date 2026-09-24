package observability

import (
	"context"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// StoreSampler adapts the repository to the Sampler interface, so the metrics
// package never imports anything but this one adapter.
type StoreSampler struct {
	store       *repository.Store
	connections func() int
	// staleAfter is the reconciliation min-age: a deposit pending longer than
	// this is one the callback should have settled by now.
	staleAfter time.Duration
}

func NewStoreSampler(store *repository.Store, connections func() int, staleAfter time.Duration) *StoreSampler {
	return &StoreSampler{store: store, connections: connections, staleAfter: staleAfter}
}

// StaleDeposits counts from the table, not from the pass, so a row the pass has
// given up on — or never scheduled — is still in the number.
func (s *StoreSampler) StaleDeposits(ctx context.Context) (int, error) {
	return s.store.Mpesa.CountPendingOlderThan(ctx, s.staleAfter)
}

func (s *StoreSampler) OpenTrades(ctx context.Context) (int, error) {
	return s.store.Trades.CountOpen(ctx)
}

func (s *StoreSampler) DriftedAccounts(ctx context.Context) (int, error) {
	_, total, err := s.store.Transactions.Drift(ctx, 1)
	return total, err
}

// LiveBotRuns counts runs the database believes are live. Sampled from the table
// rather than from BotService, so a run this process has orphaned still shows up
// — which is the case worth alerting on.
func (s *StoreSampler) LiveBotRuns(ctx context.Context) (int, error) {
	return s.store.BotRuns.CountLive(ctx)
}

func (s *StoreSampler) Connections() int {
	if s.connections == nil {
		return 0
	}
	return s.connections()
}
