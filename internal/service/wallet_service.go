package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// WalletService orchestrates deposits, withdrawals and ledger reads.
// It does not know how trades work and it does not speak Daraja.
type WalletService struct {
	store *repository.Store
	mpesa *MpesaService
	hub   Broadcaster
	cfg   *config.Config
	log   *slog.Logger

	// onDepositSettled reports the outcome for metrics. main wires the
	// Prometheus counter here so this service stays unaware of it.
	onDepositSettled func(status string)
}

// OnDepositSettled registers the outcome hook.
func (s *WalletService) OnDepositSettled(fn func(status string)) { s.onDepositSettled = fn }

func NewWalletService(store *repository.Store, mpesa *MpesaService, hub Broadcaster, cfg *config.Config, log *slog.Logger) *WalletService {
	s := &WalletService{store: store, mpesa: mpesa, hub: hub, cfg: cfg, log: log}
	// The M-Pesa service reports outcomes here; this is the only place a
	// deposit ever moves money.
	mpesa.OnResult(s.settleDeposit)
	return s
}

// --- Accounts and balances -------------------------------------------------

func (s *WalletService) ListAccounts(ctx context.Context, userID uuid.UUID) ([]models.Account, error) {
	return s.store.Accounts.ListByUser(ctx, userID)
}

// CreateAccount adds a demo or real account. One of each per user.
func (s *WalletService) CreateAccount(ctx context.Context, userID uuid.UUID, accountType string) (*models.Account, error) {
	if accountType != models.AccountDemo && accountType != models.AccountReal {
		return nil, invalid("account_type", "Account type must be demo or real")
	}

	n, err := s.store.Accounts.CountByType(ctx, userID, accountType)
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, ErrAccountExists
	}

	account := &models.Account{UserID: userID, AccountType: accountType, Currency: "USD"}
	err = s.store.WithTx(ctx, func(tx *repository.Store) error {
		if err := tx.Accounts.Create(ctx, account); err != nil {
			return err
		}

		starting := decimal.Zero
		if accountType == models.AccountDemo {
			starting = decimal.NewFromFloat(s.cfg.DemoStartingBalance)
		}
		if err := tx.Wallets.Create(ctx, account.ID, decimal.Zero, starting); err != nil {
			return err
		}
		if starting.IsZero() {
			return nil
		}

		ref := "demo-starting-balance"
		return tx.Transactions.Insert(ctx, &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxBonusCredit,
			BalanceType: models.BalanceBonus,
			Amount:      starting,
			Status:      models.StatusCompleted,
			Reference:   &ref,
			Metadata:    map[string]any{"note": "Demo account starting balance"},
		})
	})
	if err != nil {
		return nil, err
	}
	return account, nil
}

// OwnedAccount loads an account and checks it belongs to the caller. Every
// account-scoped endpoint goes through this.
func (s *WalletService) OwnedAccount(ctx context.Context, userID, accountID uuid.UUID) (*models.Account, error) {
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
	return account, nil
}

func (s *WalletService) GetWallet(ctx context.Context, userID, accountID uuid.UUID) (*models.Wallet, error) {
	if _, err := s.OwnedAccount(ctx, userID, accountID); err != nil {
		return nil, err
	}
	w, err := s.store.Wallets.Get(ctx, accountID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, ErrNotFound
	}
	return w, err
}

// --- Deposits --------------------------------------------------------------

type DepositInput struct {
	UserID      uuid.UUID
	AccountID   uuid.UUID
	AmountKES   decimal.Decimal
	PhoneNumber string
}

type DepositResponse struct {
	TransactionID     uuid.UUID       `json:"transaction_id"`
	CheckoutRequestID string          `json:"checkout_request_id"`
	AmountKES         decimal.Decimal `json:"amount_kes"`
	AmountUSD         decimal.Decimal `json:"amount_usd"`
	Status            string          `json:"status"`
}

// Deposit writes a pending ledger row and fires the STK push. No money moves
// until the Daraja callback arrives at settleDeposit.
func (s *WalletService) Deposit(ctx context.Context, in DepositInput) (*DepositResponse, error) {
	account, err := s.OwnedAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return nil, err
	}
	if account.AccountType != models.AccountReal {
		return nil, ErrRealOnly
	}

	if in.AmountKES.LessThan(decimal.NewFromFloat(s.cfg.MinDepositKES)) {
		return nil, invalid("amount", "Minimum deposit is KES "+decimal.NewFromFloat(s.cfg.MinDepositKES).StringFixed(0))
	}
	if in.AmountKES.GreaterThan(decimal.NewFromFloat(s.cfg.MaxDepositKES)) {
		return nil, invalid("amount", "Maximum deposit is KES "+decimal.NewFromFloat(s.cfg.MaxDepositKES).StringFixed(0))
	}

	phone := mpesaclient.NormalizePhone(in.PhoneNumber)
	if !mpesaclient.ValidPhone(phone) {
		return nil, invalid("phone_number", "Enter a valid Safaricom number")
	}

	// A frozen account may not take new money in. Read under a lock so a
	// freeze committing concurrently is seen.
	if err := s.store.WithTx(ctx, func(st *repository.Store) error {
		locked, err := st.Accounts.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}
		if locked.IsFrozen() {
			return ErrAccountFrozen
		}
		return nil
	}); err != nil {
		return nil, err
	}

	amountUSD := s.kesToUSD(in.AmountKES)

	tx := &models.Transaction{
		AccountID:   account.ID,
		Type:        models.TxDeposit,
		BalanceType: models.BalanceReal,
		Amount:      amountUSD,
		Status:      models.StatusPending,
		Metadata: map[string]any{
			"amount_kes": in.AmountKES.StringFixed(2),
			"rate":       s.cfg.KESPerUSD,
			"phone":      maskPhone(phone),
			"channel":    "mpesa",
		},
	}
	if err := s.store.Transactions.Insert(ctx, tx); err != nil {
		return nil, err
	}

	checkoutID, err := s.mpesa.InitiateSTK(ctx, tx.ID, phone, in.AmountKES, account.AccountNumber)
	if err != nil {
		// Mark the row failed so it never sits pending forever in the ledger.
		_ = s.store.Transactions.UpdateStatus(ctx, tx.ID, models.StatusFailed,
			map[string]any{"error": err.Error()})
		return nil, err
	}

	_ = s.store.Transactions.UpdateStatus(ctx, tx.ID, models.StatusPending,
		map[string]any{"checkout_request_id": checkoutID})

	s.notify(account.ID, "transaction", map[string]any{
		"transaction_id": tx.ID,
		"status":         models.StatusPending,
		"type":           models.TxDeposit,
	})

	return &DepositResponse{
		TransactionID:     tx.ID,
		CheckoutRequestID: checkoutID,
		AmountKES:         in.AmountKES,
		AmountUSD:         amountUSD,
		Status:            models.StatusPending,
	}, nil
}

// settleDeposit is the callback target. It credits the wallet and the ledger in
// one transaction, then pushes the new balance over the account socket so the
// pending deposit screen can flip to success without polling (spec gap G2).
func (s *WalletService) settleDeposit(ctx context.Context, r DepositResult) {
	tx, err := s.store.Transactions.GetByID(ctx, r.TransactionID)
	if err != nil {
		s.log.Error("settle deposit: transaction not found", "id", r.TransactionID, "err", err)
		return
	}
	if tx.Status != models.StatusPending {
		return // already settled
	}

	if !r.Success {
		won, err := s.store.Transactions.UpdateStatusIfPending(ctx, tx.ID, models.StatusFailed,
			map[string]any{"reason": r.Reason})
		if err != nil {
			s.log.Error("settle deposit: mark failed", "err", err)
			return
		}
		if !won {
			return // another delivery of the same callback got here first
		}
		if s.onDepositSettled != nil {
			s.onDepositSettled(models.StatusFailed)
		}
		s.notify(tx.AccountID, "transaction", map[string]any{
			"transaction_id": tx.ID,
			"status":         models.StatusFailed,
			"type":           models.TxDeposit,
			"reason":         r.Reason,
		})
		return
	}

	// The status check above is a fast path, not the guarantee. This is the
	// guarantee: the ledger row moves off 'pending' and the wallet is credited in
	// one transaction, and the move is conditional on it still being pending — so
	// a second delivery that got past the read finds nothing to move and credits
	// nothing. Money is the one place where two guards are not one too many.
	var wallet *models.Wallet
	credited := false
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		meta := map[string]any{"reason": r.Reason}
		if r.Receipt != "" {
			meta["receipt"] = r.Receipt
		}
		won, err := st.Transactions.UpdateStatusIfPending(ctx, tx.ID, models.StatusCompleted, meta)
		if err != nil {
			return err
		}
		if !won {
			return nil
		}
		w, err := st.Wallets.Adjust(ctx, tx.AccountID, models.BalanceReal, tx.Amount)
		if err != nil {
			return err
		}
		wallet, credited = w, true
		return nil
	})
	if err != nil {
		s.log.Error("settle deposit: credit failed", "id", tx.ID, "err", err)
		return
	}
	if !credited {
		s.log.Info("duplicate deposit settlement ignored", "transaction", tx.ID)
		return
	}

	s.log.Info("deposit settled", "transaction", tx.ID, "amount_usd", tx.Amount.String())
	if s.onDepositSettled != nil {
		s.onDepositSettled(models.StatusCompleted)
	}
	s.notify(tx.AccountID, "transaction", map[string]any{
		"transaction_id": tx.ID,
		"status":         models.StatusCompleted,
		"type":           models.TxDeposit,
		"amount":         tx.Amount,
		"receipt":        r.Receipt,
	})
	s.notify(tx.AccountID, "wallet", wallet)
}

// --- Withdrawals -----------------------------------------------------------

type WithdrawInput struct {
	UserID      uuid.UUID
	AccountID   uuid.UUID
	AmountUSD   decimal.Decimal
	PhoneNumber string
}

// Withdraw debits the real balance immediately and records a pending payout.
// Debiting up front stops a user spending the same balance twice while a
// payout is in flight.
func (s *WalletService) Withdraw(ctx context.Context, in WithdrawInput) (*models.Transaction, error) {
	account, err := s.OwnedAccount(ctx, in.UserID, in.AccountID)
	if err != nil {
		return nil, err
	}
	if account.AccountType != models.AccountReal {
		return nil, ErrDemoNotAllowed
	}

	user, err := s.store.Users.GetByID(ctx, in.UserID)
	if err != nil {
		return nil, err
	}
	if user.KYCStatus != models.KYCVerified {
		return nil, ErrKYCRequired
	}

	if in.AmountUSD.LessThanOrEqual(decimal.Zero) {
		return nil, invalid("amount", "Enter an amount greater than zero")
	}
	phone := mpesaclient.NormalizePhone(in.PhoneNumber)
	if !mpesaclient.ValidPhone(phone) {
		return nil, invalid("phone_number", "Enter a valid Safaricom number")
	}

	var tx *models.Transaction
	var wallet *models.Wallet
	err = s.store.WithTx(ctx, func(st *repository.Store) error {
		w, err := st.Wallets.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}

		// Same reasoning as the trading path: check the freeze under the lock.
		locked, err := st.Accounts.GetForUpdate(ctx, account.ID)
		if err != nil {
			return err
		}
		if locked.IsFrozen() {
			return ErrAccountFrozen
		}

		// Only real balance is withdrawable; bonus never is.
		if w.RealBalance.LessThan(in.AmountUSD) {
			return ErrInsufficientFunds
		}

		tx = &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxWithdrawal,
			BalanceType: models.BalanceReal,
			Amount:      in.AmountUSD.Neg(),
			Status:      models.StatusPending,
			Metadata: map[string]any{
				"amount_kes": s.usdToKES(in.AmountUSD).StringFixed(2),
				"phone":      maskPhone(phone),
				"channel":    "mpesa",
			},
		}
		if err := st.Transactions.Insert(ctx, tx); err != nil {
			return err
		}

		w, err = st.Wallets.Adjust(ctx, account.ID, models.BalanceReal, in.AmountUSD.Neg())
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

	s.notify(account.ID, "transaction", map[string]any{
		"transaction_id": tx.ID,
		"status":         models.StatusPending,
		"type":           models.TxWithdrawal,
	})
	s.notify(account.ID, "wallet", wallet)
	return tx, nil
}

// --- Ledger ----------------------------------------------------------------

type LedgerPage struct {
	Items  []models.Transaction `json:"items"`
	Total  int                  `json:"total"`
	Limit  int                  `json:"limit"`
	Offset int                  `json:"offset"`
	Page   int                  `json:"page"`
	Pages  int                  `json:"pages"`
}

func (s *WalletService) Transactions(ctx context.Context, userID, accountID uuid.UUID, types []string, limit, offset int) (*LedgerPage, error) {
	if _, err := s.OwnedAccount(ctx, userID, accountID); err != nil {
		return nil, err
	}
	items, total, err := s.store.Transactions.ListByAccount(ctx, accountID, types, limit, offset)
	if err != nil {
		return nil, err
	}
	return &LedgerPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

// Transaction backs the deposit screen's polling fallback (spec gap G10).
func (s *WalletService) Transaction(ctx context.Context, userID, txID uuid.UUID) (*models.Transaction, error) {
	tx, err := s.store.Transactions.GetByID(ctx, txID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if _, err := s.OwnedAccount(ctx, userID, tx.AccountID); err != nil {
		return nil, err
	}
	return tx, nil
}

// --- Helpers ---------------------------------------------------------------

func (s *WalletService) kesToUSD(kes decimal.Decimal) decimal.Decimal {
	return kes.Div(decimal.NewFromFloat(s.cfg.KESPerUSD)).Round(2)
}

func (s *WalletService) usdToKES(usd decimal.Decimal) decimal.Decimal {
	return usd.Mul(decimal.NewFromFloat(s.cfg.KESPerUSD)).Round(2)
}

func (s *WalletService) Rate() float64 { return s.cfg.KESPerUSD }

// notify pushes on the owning user's channel. The account id is resolved here
// rather than threaded through every call site, and a lookup failure is logged
// rather than propagated: a missed notification must never fail the money
// operation that triggered it.
func (s *WalletService) notify(accountID uuid.UUID, msgType string, payload any) {
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

// NotifyUser pushes directly to a user's channel, for events that are about
// the person rather than one of their accounts.
func (s *WalletService) NotifyUser(userID uuid.UUID, msgType string, payload any) {
	if s.hub != nil {
		s.hub.Broadcast(UserTopic(userID.String()), msgType, payload)
	}
}

// maskPhone keeps the last three digits so a user can recognise their number
// in the ledger without the full MSISDN sitting in metadata.
func maskPhone(p string) string {
	if len(p) < 4 {
		return "***"
	}
	return "***" + p[len(p)-3:]
}
