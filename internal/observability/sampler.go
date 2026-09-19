package observability

import (
	"context"

	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// StoreSampler adapts the repository to the Sampler interface, so the metrics
// package never imports anything but this one adapter.
type StoreSampler struct {
	store       *repository.Store
	connections func() int
}

func NewStoreSampler(store *repository.Store, connections func() int) *StoreSampler {
	return &StoreSampler{store: store, connections: connections}
}

func (s *StoreSampler) OpenTrades(ctx context.Context) (int, error) {
	return s.store.Trades.CountOpen(ctx)
}

func (s *StoreSampler) DriftedAccounts(ctx context.Context) (int, error) {
	_, total, err := s.store.Transactions.Drift(ctx, 1)
	return total, err
}

func (s *StoreSampler) Connections() int {
	if s.connections == nil {
		return 0
	}
	return s.connections()
}
