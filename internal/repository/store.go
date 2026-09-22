package repository

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store bundles every repository. Services depend on this one struct rather
// than wiring seven constructors each.
type Store struct {
	pool *pgxpool.Pool
	db   DBTX

	Users        *UserRepo
	Accounts     *AccountRepo
	Wallets      *WalletRepo
	Transactions *TransactionRepo
	Symbols      *SymbolRepo
	Trades       *TradeRepo
	Signals      *SignalRepo
	Mpesa        *MpesaRepo
	Admins       *AdminRepo
	Audit        *AuditRepo
	KYC          *KYCRepo
	Settings     *SettingsRepo
}

func NewStore(pool *pgxpool.Pool) *Store {
	return newStore(pool, pool)
}

func newStore(pool *pgxpool.Pool, db DBTX) *Store {
	return &Store{
		pool:         pool,
		db:           db,
		Users:        &UserRepo{db},
		Accounts:     &AccountRepo{db},
		Wallets:      &WalletRepo{db},
		Transactions: &TransactionRepo{db},
		Symbols:      &SymbolRepo{db},
		Trades:       &TradeRepo{db},
		Signals:      &SignalRepo{db},
		Mpesa:        &MpesaRepo{db},
		Admins:       &AdminRepo{db},
		Audit:        &AuditRepo{db},
		KYC:          &KYCRepo{db},
		Settings:     &SettingsRepo{db},
	}
}

// WithTx runs fn inside a database transaction. Every balance change goes
// through here: the ledger insert and the wallet cache update must commit
// together or not at all.
func (s *Store) WithTx(ctx context.Context, fn func(*Store) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx) // no-op after a successful commit
	}()

	if err := fn(newStore(s.pool, tx)); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) Pool() *pgxpool.Pool { return s.pool }
