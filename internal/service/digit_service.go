package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// DigitVoidGrace is how long after open we wait for a settlement tick before
// auto-voiding and refunding. Long enough for normal mock/live jitter (~500ms
// ticks), short enough that a stalled feed does not freeze stakes.
const DigitVoidGrace = 5 * time.Second

// DigitSweepEvery is the safety-net interval. Tick settlement is primary;
// this catches missed ticks and process downtime.
const DigitSweepEvery = 5 * time.Second

// DigitService is the parallel binary last-digit product. It does not touch
// CFD trades or pnl.go.
type DigitService struct {
	store *repository.Store
	feed  *FeedService
	hub   Broadcaster
	log   *slog.Logger
}

func NewDigitService(store *repository.Store, feed *FeedService, hub Broadcaster, log *slog.Logger) *DigitService {
	return &DigitService{store: store, feed: feed, hub: hub, log: log}
}

// Start registers the tick settler and runs an immediate then periodic sweep.
func (s *DigitService) Start(ctx context.Context) {
	s.feed.OnTick(s.onTick)
	// Catch anything that expired while the process was down.
	s.sweep(ctx)
	go func() {
		t := time.NewTicker(DigitSweepEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.sweep(ctx)
			}
		}
	}()
}

type OpenDigitInput struct {
	UserID       uuid.UUID
	AccountID    uuid.UUID
	SymbolCode   string
	ContractType string
	Selection    models.DigitSelection
	Stake        decimal.Decimal
}

func (s *DigitService) Open(ctx context.Context, in OpenDigitInput) (*models.DigitContract, error) {
	account, err := s.store.Accounts.GetByID(ctx, in.AccountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if account.UserID != in.UserID {
		return nil, ErrForbidden
	}
	if in.Stake.LessThanOrEqual(decimal.Zero) {
		return nil, invalid("stake", "Enter a stake greater than zero")
	}

	symbol, err := s.store.Symbols.GetByCode(ctx, in.SymbolCode)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !symbol.IsActive {
		return nil, ErrMarketClosed
	}
	if symbol.Category != models.CategoryVolatility {
		return nil, invalid("symbol", "Digit contracts are only available on volatility indices")
	}

	if err := validateDigitSelection(in.ContractType, in.Selection); err != nil {
		return nil, err
	}

	ratio, err := models.PayoutRatioFor(in.ContractType, in.Selection)
	if err != nil {
		return nil, invalid("selection", err.Error())
	}

	tick, ok := s.feed.Latest(symbol.Code)
	if !ok {
		return nil, ErrNoPrice
	}
	entryDigit := models.LastDigit(tick.Price, symbol.PipSize)
	potential := models.WinCredit(in.Stake, ratio)

	contract := &models.DigitContract{
		AccountID:       account.ID,
		SymbolID:        symbol.ID,
		ContractType:    in.ContractType,
		Selection:       in.Selection,
		Stake:           in.Stake,
		PayoutRatio:     ratio,
		PotentialPayout: potential,
		EntryPrice:      tick.Price,
		EntryDigit:      entryDigit,
		EntryTickEpoch:  tick.Epoch,
		VoidAfter:       time.Now().UTC().Add(DigitVoidGrace),
		SymbolCode:      symbol.Code,
		SymbolName:      symbol.DisplayName,
		PipSize:         symbol.PipSize,
	}

	var wallet *models.Wallet
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		w, err := st.Wallets.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}
		locked, err := st.Accounts.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}
		if locked.IsFrozen() {
			return ErrAccountFrozen
		}
		balanceType := pickBalance(w, in.Stake)
		if balanceType == "" {
			return ErrInsufficientFunds
		}
		if err := st.DigitContracts.Create(ctx, contract); err != nil {
			return err
		}
		ref := contract.ID.String()
		if err := st.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxDigitOpen,
			BalanceType: balanceType,
			Amount:      in.Stake.Neg(),
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata: map[string]any{
				"symbol":        symbol.Code,
				"contract_type": in.ContractType,
				"entry":         tick.Price.String(),
				"entry_digit":   entryDigit,
			},
		}); err != nil {
			return err
		}
		w, err = st.Wallets.Adjust(ctx, account.ID, balanceType, in.Stake.Neg())
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrInsufficientFunds
			}
			return err
		}
		wallet = w
		return nil
	})
	if err != nil {
		return nil, err
	}

	opened, err := s.store.DigitContracts.GetByID(ctx, contract.ID)
	if err != nil {
		return nil, err
	}
	s.notify(account.ID, "wallet", wallet)
	s.notify(account.ID, "digit_opened", opened)
	return opened, nil
}

func validateDigitSelection(contractType string, sel models.DigitSelection) error {
	switch contractType {
	case models.DigitEvenOdd, models.DigitMatchesDiffers, models.DigitOverUnder:
	default:
		return invalid("contract_type", "Contract type must be even_odd, matches_differs, or over_under")
	}
	if _, err := models.PayoutRatioFor(contractType, sel); err != nil {
		return invalid("selection", err.Error())
	}
	return nil
}

func (s *DigitService) onTick(tick models.Tick) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	symbol, err := s.store.Symbols.GetByCode(ctx, tick.Symbol)
	if err != nil {
		return
	}
	open, err := s.store.DigitContracts.ListOpenBySymbol(ctx, symbol.ID)
	if err != nil {
		s.log.Error("digit list open by symbol", "err", err)
		return
	}
	for i := range open {
		c := open[i]
		if tick.Epoch <= c.EntryTickEpoch {
			continue
		}
		if _, err := s.settle(ctx, &c, tick, "tick"); err != nil && !errors.Is(err, ErrDigitNotOpen) {
			s.log.Error("digit settle on tick failed", "contract", c.ID, "err", err)
		}
	}
}

func (s *DigitService) sweep(ctx context.Context) {
	open, err := s.store.DigitContracts.ListAllOpen(ctx)
	if err != nil {
		s.log.Error("digit sweep list", "err", err)
		return
	}
	now := time.Now().UTC()
	for i := range open {
		c := &open[i]
		latest, ok := s.feed.Latest(c.SymbolCode)
		if ok && latest.Epoch > c.EntryTickEpoch {
			if _, err := s.settle(ctx, c, latest, "sweep"); err != nil && !errors.Is(err, ErrDigitNotOpen) {
				s.log.Error("digit sweep settle failed", "contract", c.ID, "err", err)
			}
			continue
		}
		// Past expiry with no usable settlement tick past grace → void.
		if now.After(c.VoidAfter) {
			waited := now.Sub(c.OpenedAt)
			if _, err := s.voidContract(ctx, c, waited); err != nil && !errors.Is(err, ErrDigitNotOpen) {
				s.log.Error("digit void failed", "contract", c.ID, "err", err)
			}
		}
	}
}

// EnsureSettled is check-on-read: if an open contract already has a later tick,
// settle before returning it; if past grace with no tick, void.
func (s *DigitService) EnsureSettled(ctx context.Context, c *models.DigitContract) (*models.DigitContract, error) {
	if c.Status != models.DigitOpen {
		return c, nil
	}
	latest, ok := s.feed.Latest(c.SymbolCode)
	if ok && latest.Epoch > c.EntryTickEpoch {
		return s.settle(ctx, c, latest, "check_on_read")
	}
	if time.Now().UTC().After(c.VoidAfter) {
		return s.voidContract(ctx, c, time.Since(c.OpenedAt))
	}
	return c, nil
}

var ErrDigitNotOpen = errors.New("This contract is already settled")

func (s *DigitService) settle(ctx context.Context, c *models.DigitContract, tick models.Tick, reason string) (*models.DigitContract, error) {
	digit := models.LastDigit(tick.Price, c.PipSize)
	won, err := models.DigitWins(c.ContractType, c.Selection, digit)
	if err != nil {
		return nil, err
	}

	status := models.DigitLost
	pnl := c.Stake.Neg()
	credit := decimal.Zero
	if won {
		status = models.DigitWon
		credit = models.WinCredit(c.Stake, c.PayoutRatio)
		pnl = credit.Sub(c.Stake)
	}

	var wallet *models.Wallet
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		locked, err := st.DigitContracts.GetOpenForUpdate(ctx, c.ID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrDigitNotOpen
			}
			return err
		}
		_ = locked

		if err := st.DigitContracts.Settle(ctx, c.ID, status, tick.Price, digit, pnl); err != nil {
			return err
		}

		var balanceType string
		ref := c.ID.String()
		if err := st.Transactions.QueryDigitFundingBalance(ctx, ref, &balanceType); err != nil {
			return err
		}

		if credit.IsPositive() {
			if err := st.Transactions.Insert(ctx, &models.Transaction{
				AccountID:   c.AccountID,
				Type:        models.TxDigitSettle,
				BalanceType: balanceType,
				Amount:      credit,
				Status:      models.StatusCompleted,
				Reference:   &ref,
				Metadata: map[string]any{
					"symbol":           c.SymbolCode,
					"status":           status,
					"settlement_digit": digit,
					"reason":           reason,
				},
			}); err != nil {
				return err
			}
			w, err := st.Wallets.Adjust(ctx, c.AccountID, balanceType, credit)
			if err != nil {
				return err
			}
			wallet = w
		} else {
			// Loss: stake already debited; record a zero-credit settle for audit.
			if err := st.Transactions.Insert(ctx, &models.Transaction{
				AccountID:   c.AccountID,
				Type:        models.TxDigitSettle,
				BalanceType: balanceType,
				Amount:      decimal.Zero,
				Status:      models.StatusCompleted,
				Reference:   &ref,
				Metadata: map[string]any{
					"symbol":           c.SymbolCode,
					"status":           status,
					"settlement_digit": digit,
					"reason":           reason,
				},
			}); err != nil {
				return err
			}
			w, err := st.Wallets.Get(ctx, c.AccountID)
			if err != nil {
				return err
			}
			wallet = w
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	closed, err := s.store.DigitContracts.GetByID(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	s.notify(c.AccountID, "wallet", wallet)
	s.notify(c.AccountID, "digit_settled", map[string]any{"contract": closed, "reason": reason})
	return closed, nil
}

func (s *DigitService) voidContract(ctx context.Context, c *models.DigitContract, waited time.Duration) (*models.DigitContract, error) {
	pnl := decimal.Zero // refund stake → net zero vs open debit
	var wallet *models.Wallet
	err := s.store.WithTx(ctx, func(st *repository.Store) error {
		if _, err := st.DigitContracts.GetOpenForUpdate(ctx, c.ID); err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrDigitNotOpen
			}
			return err
		}
		if err := st.DigitContracts.Void(ctx, c.ID, pnl); err != nil {
			return err
		}
		var balanceType string
		ref := c.ID.String()
		if err := st.Transactions.QueryDigitFundingBalance(ctx, ref, &balanceType); err != nil {
			return err
		}
		// Refund stake.
		if err := st.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   c.AccountID,
			Type:        models.TxDigitSettle,
			BalanceType: balanceType,
			Amount:      c.Stake,
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata: map[string]any{
				"symbol": c.SymbolCode,
				"status": models.DigitVoid,
				"reason": "auto_void",
				"waited": waited.String(),
			},
		}); err != nil {
			return err
		}
		w, err := st.Wallets.Adjust(ctx, c.AccountID, balanceType, c.Stake)
		if err != nil {
			return err
		}
		wallet = w
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.log.Warn("digit contract auto-voided",
		"contract", c.ID, "symbol", c.SymbolCode, "waited", waited.String(),
		"entry_epoch", c.EntryTickEpoch)

	closed, err := s.store.DigitContracts.GetByID(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	s.notify(c.AccountID, "wallet", wallet)
	s.notify(c.AccountID, "digit_settled", map[string]any{"contract": closed, "reason": "auto_void"})
	return closed, nil
}

func (s *DigitService) Get(ctx context.Context, userID, id uuid.UUID) (*models.DigitContract, error) {
	c, err := s.store.DigitContracts.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	account, err := s.store.Accounts.GetByID(ctx, c.AccountID)
	if err != nil {
		return nil, err
	}
	if account.UserID != userID {
		return nil, ErrForbidden
	}
	return s.EnsureSettled(ctx, c)
}

// Quote returns the static-table payout for a type/selection/stake — the same
// numbers Open would snapshot. Read-only; no wallet or feed involved.
func (s *DigitService) Quote(contractType string, sel models.DigitSelection, stake decimal.Decimal) (*models.DigitQuote, error) {
	if err := validateDigitSelection(contractType, sel); err != nil {
		return nil, err
	}
	if !stake.GreaterThan(decimal.Zero) {
		return nil, invalid("stake", "Enter a stake greater than zero")
	}
	ratio, err := models.PayoutRatioFor(contractType, sel)
	if err != nil {
		return nil, invalid("selection", err.Error())
	}
	return &models.DigitQuote{
		ContractType:    contractType,
		Selection:       sel,
		Stake:           stake.Round(2),
		PayoutRatio:     ratio,
		PotentialPayout: models.WinCredit(stake, ratio),
	}, nil
}

func (s *DigitService) List(ctx context.Context, userID, accountID uuid.UUID, status string, limit, offset int) ([]models.DigitContract, int, error) {
	account, err := s.store.Accounts.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, 0, ErrNotFound
		}
		return nil, 0, err
	}
	if account.UserID != userID {
		return nil, 0, ErrForbidden
	}
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	items, total, err := s.store.DigitContracts.ListByAccount(ctx, accountID, status, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	if status == models.DigitOpen || status == "" {
		for i := range items {
			if items[i].Status != models.DigitOpen {
				continue
			}
			settled, err := s.EnsureSettled(ctx, &items[i])
			if err != nil {
				continue
			}
			items[i] = *settled
		}
	}
	return items, total, nil
}

func (s *DigitService) Summary(ctx context.Context, userID, accountID uuid.UUID) (repository.DigitSummary, error) {
	account, err := s.store.Accounts.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return repository.DigitSummary{}, ErrNotFound
		}
		return repository.DigitSummary{}, err
	}
	if account.UserID != userID {
		return repository.DigitSummary{}, ErrForbidden
	}
	// "Session" for Phase 1 = last 24 hours of settlements + current opens.
	return s.store.DigitContracts.Summary(ctx, accountID, time.Now().UTC().Add(-24*time.Hour))
}

func (s *DigitService) notify(accountID uuid.UUID, msgType string, payload any) {
	account, err := s.store.Accounts.GetByID(context.Background(), accountID)
	if err != nil {
		return
	}
	s.hub.Broadcast(UserTopic(account.UserID.String()), msgType, payload)
}

// SettleForTest and SweepForTest expose the private paths to integration tests.
func (s *DigitService) SettleForTest(ctx context.Context, c *models.DigitContract, tick models.Tick, reason string) (*models.DigitContract, error) {
	return s.settle(ctx, c, tick, reason)
}

func (s *DigitService) SweepForTest(ctx context.Context) { s.sweep(ctx) }
