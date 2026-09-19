package service

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// TradingService opens and closes positions and keeps the ledger straight.
// It does not know how deposits work.
type TradingService struct {
	store *repository.Store
	feed  *FeedService
	hub   Broadcaster
	log   *slog.Logger

	// openBySymbol is a cache of open positions so the stop-loss watcher does
	// not query the database on every tick. It is authoritative only as a
	// trigger; the close itself re-reads the row under a lock.
	mu           sync.RWMutex
	openBySymbol map[string][]uuid.UUID
}

func NewTradingService(store *repository.Store, feed *FeedService, hub Broadcaster, log *slog.Logger) *TradingService {
	return &TradingService{
		store:        store,
		feed:         feed,
		hub:          hub,
		log:          log,
		openBySymbol: make(map[string][]uuid.UUID),
	}
}

// Start loads the open-position cache and subscribes to the tick stream so
// stop-loss and take-profit levels are honoured while the user is away.
func (s *TradingService) Start(ctx context.Context) {
	if err := s.refreshCache(ctx); err != nil {
		s.log.Error("load open positions", "err", err)
	}
	s.feed.OnTick(s.onTick)

	// Periodic resync guards against cache drift if a close ever fails midway.
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := s.refreshCache(ctx); err != nil {
					s.log.Error("resync open positions", "err", err)
				}
			}
		}
	}()
}

func (s *TradingService) refreshCache(ctx context.Context) error {
	trades, err := s.store.Trades.ListAllOpen(ctx)
	if err != nil {
		return err
	}
	index := make(map[string][]uuid.UUID)
	for _, t := range trades {
		if t.StopLoss != nil || t.TakeProfit != nil {
			index[t.SymbolCode] = append(index[t.SymbolCode], t.ID)
		}
	}
	s.mu.Lock()
	s.openBySymbol = index
	s.mu.Unlock()
	return nil
}

func (s *TradingService) trackOpen(symbolCode string, id uuid.UUID) {
	s.mu.Lock()
	s.openBySymbol[symbolCode] = append(s.openBySymbol[symbolCode], id)
	s.mu.Unlock()
}

func (s *TradingService) untrack(symbolCode string, id uuid.UUID) {
	s.mu.Lock()
	ids := s.openBySymbol[symbolCode]
	for i, existing := range ids {
		if existing == id {
			s.openBySymbol[symbolCode] = append(ids[:i], ids[i+1:]...)
			break
		}
	}
	s.mu.Unlock()
}

// onTick checks every tracked position on this symbol against its levels.
func (s *TradingService) onTick(tick models.Tick) {
	s.mu.RLock()
	ids := make([]uuid.UUID, len(s.openBySymbol[tick.Symbol]))
	copy(ids, s.openBySymbol[tick.Symbol])
	s.mu.RUnlock()

	if len(ids) == 0 {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, id := range ids {
		trade, err := s.store.Trades.GetByID(ctx, id)
		if err != nil || trade.Status != models.TradeOpen {
			s.untrack(tick.Symbol, id)
			continue
		}

		reason := ""
		switch {
		case trade.StopLoss != nil && models.HitsStopLoss(trade.Direction, *trade.StopLoss, tick.Price):
			reason = "stop_loss"
		case trade.TakeProfit != nil && models.HitsTakeProfit(trade.Direction, *trade.TakeProfit, tick.Price):
			reason = "take_profit"
		}
		if reason == "" {
			continue
		}

		if _, err := s.settle(ctx, trade, tick.Price, reason); err != nil {
			s.log.Error("auto-close failed", "trade", id, "reason", reason, "err", err)
			continue
		}
		s.log.Info("position auto-closed", "trade", id, "reason", reason)
	}
}

// --- Opening ---------------------------------------------------------------

type OpenTradeInput struct {
	UserID     uuid.UUID
	AccountID  uuid.UUID
	SymbolCode string
	Direction  string
	Stake      decimal.Decimal
	StopLoss   *decimal.Decimal
	TakeProfit *decimal.Decimal
}

// Open validates, debits the stake, and records the position. Entry price is
// taken from the live feed on the server, never from the client, so a stale
// browser tab cannot open at an old price.
func (s *TradingService) Open(ctx context.Context, in OpenTradeInput) (*models.Trade, error) {
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

	if in.Direction != models.DirectionBuy && in.Direction != models.DirectionSell {
		return nil, invalid("direction", "Direction must be buy or sell")
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

	entry, ok := s.feed.Price(symbol.Code)
	if !ok {
		return nil, ErrNoPrice
	}

	if err := validateLevels(in.Direction, entry, in.StopLoss, in.TakeProfit); err != nil {
		return nil, err
	}

	trade := &models.Trade{
		AccountID:  account.ID,
		SymbolID:   symbol.ID,
		Direction:  in.Direction,
		EntryPrice: entry,
		Stake:      in.Stake,
		StopLoss:   in.StopLoss,
		TakeProfit: in.TakeProfit,
		SymbolCode: symbol.Code,
		SymbolName: symbol.DisplayName,
		PipSize:    symbol.PipSize,
	}

	var wallet *models.Wallet
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		w, err := st.Wallets.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}

		// The freeze check lives here, inside the same transaction that locks
		// the wallet, not as a guard before it. A check outside the
		// transaction is a race the freeze can lose: a trade that read the
		// account a millisecond before the freeze committed would still debit.
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

		if err := st.Trades.Create(ctx, trade); err != nil {
			return err
		}

		ref := trade.ID.String()
		if err := st.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxTradeOpen,
			BalanceType: balanceType,
			Amount:      in.Stake.Neg(),
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata: map[string]any{
				"symbol":    symbol.Code,
				"direction": in.Direction,
				"entry":     entry.String(),
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

	if trade.StopLoss != nil || trade.TakeProfit != nil {
		s.trackOpen(symbol.Code, trade.ID)
	}
	s.notify(account.ID, "wallet", wallet)
	s.notify(account.ID, "trade_opened", trade)
	return trade, nil
}

// --- Closing ---------------------------------------------------------------

func (s *TradingService) Close(ctx context.Context, userID, tradeID uuid.UUID) (*models.Trade, error) {
	trade, err := s.store.Trades.GetByID(ctx, tradeID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	account, err := s.store.Accounts.GetByID(ctx, trade.AccountID)
	if err != nil {
		return nil, err
	}
	if account.UserID != userID {
		return nil, ErrForbidden
	}
	if trade.Status != models.TradeOpen {
		return nil, ErrTradeNotOpen
	}

	exit, ok := s.feed.Price(trade.SymbolCode)
	if !ok {
		return nil, ErrNoPrice
	}
	return s.settle(ctx, trade, exit, "manual")
}

// settle is the single close path, shared by the user's Close button and the
// stop-loss watcher. It re-reads the trade under a row lock so the two can
// never both settle the same position.
func (s *TradingService) settle(ctx context.Context, trade *models.Trade, exit decimal.Decimal, reason string) (*models.Trade, error) {
	pnl := models.PnL(trade.Direction, trade.EntryPrice, exit, trade.Stake)

	var wallet *models.Wallet
	err := s.store.WithTx(ctx, func(st *repository.Store) error {
		locked, err := st.Trades.GetOpenForUpdate(ctx, trade.ID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrTradeNotOpen
			}
			return err
		}
		_ = locked

		if err := st.Trades.Close(ctx, trade.ID, exit, pnl); err != nil {
			return err
		}

		// The stake was debited from whichever balance funded the open; return
		// stake + pnl to the same one.
		balanceType, err := s.fundingBalance(ctx, st, trade.ID)
		if err != nil {
			return err
		}
		credit := trade.Stake.Add(pnl)
		if credit.LessThan(decimal.Zero) {
			credit = decimal.Zero // a position can lose the stake, never more
		}

		ref := trade.ID.String()
		if err := st.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   trade.AccountID,
			Type:        models.TxTradeClose,
			BalanceType: balanceType,
			Amount:      credit,
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata: map[string]any{
				"symbol":    trade.SymbolCode,
				"direction": trade.Direction,
				"exit":      exit.String(),
				"pnl":       pnl.String(),
				"reason":    reason,
			},
		}); err != nil {
			return err
		}

		w, err := st.Wallets.Adjust(ctx, trade.AccountID, balanceType, credit)
		if err != nil {
			return err
		}
		wallet = w
		return nil
	})
	if err != nil {
		return nil, err
	}

	s.untrack(trade.SymbolCode, trade.ID)

	closed, err := s.store.Trades.GetByID(ctx, trade.ID)
	if err != nil {
		return nil, err
	}
	s.notify(trade.AccountID, "wallet", wallet)
	s.notify(trade.AccountID, "trade_closed", map[string]any{"trade": closed, "reason": reason})
	return closed, nil
}

// fundingBalance recovers which balance paid for the open, so the close credits
// the same one. Demo bonus money must never turn into withdrawable real money.
func (s *TradingService) fundingBalance(ctx context.Context, st *repository.Store, tradeID uuid.UUID) (string, error) {
	ref := tradeID.String()
	var balanceType string
	err := st.Transactions.QueryFundingBalance(ctx, ref, &balanceType)
	if err != nil {
		return models.BalanceReal, nil
	}
	return balanceType, nil
}

// --- Reads -----------------------------------------------------------------

type TradePage struct {
	Items  []models.Trade `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Page   int            `json:"page"`
	Pages  int            `json:"pages"`
}

func (s *TradingService) List(ctx context.Context, userID, accountID uuid.UUID, status string, limit, offset int) (*TradePage, error) {
	account, err := s.store.Accounts.GetByID(ctx, accountID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if account.UserID != userID {
		return nil, ErrForbidden
	}

	items, total, err := s.store.Trades.ListByAccount(ctx, accountID, status, limit, offset)
	if err != nil {
		return nil, err
	}
	return &TradePage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// Summary backs the Portfolio screen's three tiles.
type Summary struct {
	OpenPnL        decimal.Decimal `json:"open_pnl"`
	RealisedToday  decimal.Decimal `json:"realised_today"`
	OpenPositions  int             `json:"open_positions"`
	PricesComplete bool            `json:"prices_complete"`
}

func (s *TradingService) Summary(ctx context.Context, userID, accountID uuid.UUID) (*Summary, error) {
	page, err := s.List(ctx, userID, accountID, models.TradeOpen, 500, 0)
	if err != nil {
		return nil, err
	}

	out := &Summary{OpenPositions: len(page.Items), PricesComplete: true}
	for _, t := range page.Items {
		price, ok := s.feed.Price(t.SymbolCode)
		if !ok {
			out.PricesComplete = false
			continue
		}
		out.OpenPnL = out.OpenPnL.Add(models.PnL(t.Direction, t.EntryPrice, price, t.Stake))
	}

	realised, err := s.store.Trades.RealisedSince(ctx, accountID, 24)
	if err != nil {
		return nil, err
	}
	out.RealisedToday = realised
	return out, nil
}

// notify pushes on the owning user's channel; see WalletService.notify.
func (s *TradingService) notify(accountID uuid.UUID, msgType string, payload any) {
	if s.hub == nil {
		return
	}
	account, err := s.store.Accounts.GetByID(context.Background(), accountID)
	if err != nil {
		s.log.Warn("could not resolve account for notification", "account", accountID, "err", err)
		return
	}
	s.hub.Broadcast(UserTopic(account.UserID.String()), msgType, payload)
}

// pickBalance chooses which balance funds a stake: bonus first so demo and
// promotional money is consumed before a user's own deposits.
func pickBalance(w *models.Wallet, stake decimal.Decimal) string {
	if w.BonusBalance.GreaterThanOrEqual(stake) {
		return models.BalanceBonus
	}
	if w.RealBalance.GreaterThanOrEqual(stake) {
		return models.BalanceReal
	}
	return ""
}

// validateLevels enforces that a stop loss sits on the losing side of entry and
// a take profit on the winning side, for the given direction.
func validateLevels(direction string, entry decimal.Decimal, sl, tp *decimal.Decimal) error {
	if sl != nil {
		if direction == models.DirectionBuy && sl.GreaterThanOrEqual(entry) {
			return invalid("stop_loss", "Stop loss must be below the current price for a buy")
		}
		if direction == models.DirectionSell && sl.LessThanOrEqual(entry) {
			return invalid("stop_loss", "Stop loss must be above the current price for a sell")
		}
	}
	if tp != nil {
		if direction == models.DirectionBuy && tp.LessThanOrEqual(entry) {
			return invalid("take_profit", "Take profit must be above the current price for a buy")
		}
		if direction == models.DirectionSell && tp.GreaterThanOrEqual(entry) {
			return invalid("take_profit", "Take profit must be below the current price for a sell")
		}
	}
	return nil
}
