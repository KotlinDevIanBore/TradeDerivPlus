package service

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// withdrawalFlags is the one flag the withdrawal path consults. An interface,
// and wired by a setter rather than through the constructor, because
// NewWalletService is called from main and from three tests and its signature is
// worth keeping — the same reasoning as MpesaService's querier seam.
type withdrawalFlags interface {
	WithdrawalsEnabled(ctx context.Context) (bool, error)
}

// WalletService orchestrates deposits, withdrawals and ledger reads.
// It does not know how trades work and it does not speak Daraja.
type WalletService struct {
	store *repository.Store
	mpesa *MpesaService
	hub   Broadcaster
	cfg   *config.Config
	log   *slog.Logger

	// flags gates withdrawals. Nil means refuse: a wallet service with no flag
	// source must not decide that a feature guarding real payouts is on.
	flags withdrawalFlags

	// payouts sends the money out. Nil means a withdrawal cannot be requested at
	// all, which is the same fail-closed direction as flags.
	payouts payoutDispatcher

	// onDepositSettled reports the outcome for metrics. main wires the
	// Prometheus counter here so this service stays unaware of it.
	onDepositSettled func(status string)
}

// SetWithdrawalFlags wires the settings service in. Until it is called,
// withdrawals are refused.
func (s *WalletService) SetWithdrawalFlags(f withdrawalFlags) { s.flags = f }

// payoutDispatcher is the payout service, through the one method Withdraw needs.
// Set by SetPayouts, which also registers this service's settlement handler — so
// the two halves cannot be wired half way.
type payoutDispatcher interface {
	Send(ctx context.Context, p *models.MpesaPayout)
}

// SetPayouts wires the payout service in both directions: this service asks it
// to send, and it reports outcomes back to settlePayout, which is the only place
// a withdrawal's money moves.
func (s *WalletService) SetPayouts(p *MpesaPayoutService) {
	s.payouts = p
	p.OnResult(s.settlePayout)
}

// settlePayout is what the payout service calls when an outcome is established.
// It is registered by SetPayouts and is the only path from a Daraja answer to a
// balance.
//
// A success moves no money: the debit happened when the withdrawal was requested,
// so all that changes is the ledger row's status and the receipt. A failure
// returns the money, through FailWithdrawal — the guarded flip plus the credit,
// in transactions-row then wallets-row order.
func (s *WalletService) settlePayout(ctx context.Context, st PayoutSettlement) {
	if st.Paid {
		if err := s.completeWithdrawal(ctx, st); err != nil {
			s.log.Error("settle payout: mark completed", "transaction", st.TransactionID, "err", err)
		}
		return
	}
	if _, err := s.failWithdrawalFrom(ctx, st); err != nil {
		s.log.Error("settle payout: refund", "transaction", st.TransactionID, "err", err)
	}
}

// completeWithdrawal closes a paid withdrawal. No balance changes — the money
// left when the withdrawal was requested — so this is a status write and a
// notification, and it is guarded so a repeated result cannot announce twice.
func (s *WalletService) completeWithdrawal(ctx context.Context, st PayoutSettlement) error {
	meta := map[string]any{"settled_by": st.Source}
	if st.Receipt != "" {
		meta["receipt"] = st.Receipt
	}
	if st.AdminID != nil {
		meta["admin_id"] = st.AdminID.String()
	}
	won, err := s.store.Transactions.UpdateStatusIfPending(ctx, st.TransactionID, models.StatusCompleted, meta)
	if err != nil {
		return err
	}
	if !won {
		s.log.Info("duplicate withdrawal completion ignored", "transaction", st.TransactionID)
		return nil
	}
	tx, err := s.store.Transactions.GetByID(ctx, st.TransactionID)
	if err != nil {
		return err
	}
	s.log.Info("withdrawal paid", "transaction", st.TransactionID, "receipt", st.Receipt, "source", st.Source)
	s.notify(tx.AccountID, "transaction", map[string]any{
		"transaction_id": st.TransactionID,
		"status":         models.StatusCompleted,
		"type":           models.TxWithdrawal,
		"receipt":        st.Receipt,
		"settled_by":     st.Source,
	})
	return nil
}

func (s *WalletService) failWithdrawalFrom(ctx context.Context, st PayoutSettlement) (bool, error) {
	return s.failWithdrawal(ctx, st.TransactionID, st.Reason, st.Source, st.AdminID)
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

	// Metadata only. This used to go through UpdateStatus and re-assert
	// 'pending', which reverted any settlement that had landed in the gap since
	// InitiateSTK returned — wallet credited, ledger pending, and unrepairable
	// because the mpesa_requests row was already terminal. The gap is real on a
	// live feed and invisible under the mock, whose callback waits four seconds.
	_ = s.store.Transactions.MergeMetadata(ctx, tx.ID,
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
		meta := map[string]any{"reason": r.Reason, "settled_by": r.Source}
		if r.AdminID != nil {
			meta["admin_id"] = r.AdminID.String()
		}
		won, err := s.store.Transactions.UpdateStatusIfPending(ctx, tx.ID, models.StatusFailed, meta)
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
		// settled_by records where the outcome came from. A callback carries a
		// receipt; a query or an operator does not, and a row with no receipt
		// should say why rather than look like a callback that forgot one.
		meta := map[string]any{"reason": r.Reason, "settled_by": r.Source}
		if r.Receipt != "" {
			meta["receipt"] = r.Receipt
		}
		if r.AdminID != nil {
			// The same key Adjust writes, so the console's admin join names the
			// operator on a resolved deposit exactly as it does on an adjustment.
			meta["admin_id"] = r.AdminID.String()
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
		"settled_by":     r.Source,
	})
	s.notify(tx.AccountID, "wallet", wallet)
}

// Settlement sources for a withdrawal, as written to metadata.settled_by. The
// payout and its reconciler arrive in later checkpoints; "admin" is what an
// operator closing a stuck withdrawal by hand will write.
const (
	WithdrawalFailedByPayout = "payout"
	WithdrawalFailedByAdmin  = "admin"
)

// FailWithdrawal closes a pending withdrawal as failed and returns the money.
//
// It is the only place a withdrawal is refunded, and it is two writes and
// exactly two: the guarded status flip, then the wallet credit, in one
// transaction. Not three — a compensating ledger row alongside these would
// credit the ledger twice, because the flip to 'failed' already takes the row
// out of balanceAffectingRows and hands the amount back to the ledger total.
// The predicate's comment carries the same warning from the other side.
//
// The order is transactions-row then wallets-row, matching settleDeposit and
// every other path that locks an existing ledger row. Withdraw itself takes the
// wallet first, which is safe only because its ledger write is an INSERT of a
// new row; copying that order here would make this the first wallets-then-
// transactions path in the codebase and deadlock it against deposit settlement.
//
// It reports whether this call performed the refund. The guard inside the UPDATE
// decides, not a read before it: a payout result and its retry, or a result and
// an operator, can arrive together, and only one of them may credit.
func (s *WalletService) FailWithdrawal(ctx context.Context, txID uuid.UUID, reason, source string) (bool, error) {
	return s.failWithdrawal(ctx, txID, reason, source, nil)
}

func (s *WalletService) failWithdrawal(ctx context.Context, txID uuid.UUID, reason, source string, adminID *uuid.UUID) (bool, error) {
	var refunded bool
	var wallet *models.Wallet
	var accountID uuid.UUID
	var amount decimal.Decimal

	err := s.store.WithTx(ctx, func(st *repository.Store) error {
		tx, err := st.Transactions.GetByID(ctx, txID)
		if err != nil {
			if errors.Is(err, repository.ErrNotFound) {
				return ErrNotFound
			}
			return err
		}
		if tx.Type != models.TxWithdrawal {
			return invalid("transaction", "Only a withdrawal can be refunded this way")
		}
		accountID, amount = tx.AccountID, tx.Amount.Abs()

		meta := map[string]any{"reason": reason, "settled_by": source}
		if adminID != nil {
			meta["admin_id"] = adminID.String()
		}
		won, err := st.Transactions.UpdateStatusIfPending(ctx, tx.ID, models.StatusFailed, meta)
		if err != nil {
			return err
		}
		if !won {
			// Already settled — by a retry of the same result, or by an operator.
			// Whoever won it credited; this call must not.
			return nil
		}

		w, err := st.Wallets.Adjust(ctx, tx.AccountID, models.BalanceReal, amount)
		if err != nil {
			return err
		}
		wallet, refunded = w, true
		return nil
	})
	if err != nil {
		return false, err
	}
	if !refunded {
		s.log.Info("duplicate withdrawal refund ignored", "transaction", txID, "source", source)
		return false, nil
	}

	s.log.Info("withdrawal refunded", "transaction", txID, "amount_usd", amount.String(), "source", source, "reason", reason)
	s.notify(accountID, "transaction", map[string]any{
		"transaction_id": txID,
		"status":         models.StatusFailed,
		"type":           models.TxWithdrawal,
		"reason":         reason,
		"settled_by":     source,
	})
	s.notify(accountID, "wallet", wallet)
	return true, nil
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
	// The platform gate first: while withdrawals are off, nothing else about the
	// request matters. Off by default, because the payout does not exist yet.
	if s.flags == nil {
		return nil, ErrWithdrawalsDisabled
	}
	enabled, err := s.flags.WithdrawalsEnabled(ctx)
	if err != nil {
		// A broken settings read is not a disabled feature, and it is not a
		// licence to pay out either. Refuse, and say it was us.
		return nil, err
	}
	if !enabled {
		return nil, ErrWithdrawalsDisabled
	}

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
	// Ceilings are expressed in KES, the currency the customer is paid in and the
	// one Daraja's own limits are written in.
	amountKES := s.usdToKES(in.AmountUSD)
	maxKES := decimal.NewFromFloat(s.cfg.MaxWithdrawKES)
	if amountKES.GreaterThan(maxKES) {
		return nil, invalid("amount", "Maximum withdrawal is KES "+maxKES.StringFixed(0)+" per transaction")
	}
	phone := mpesaclient.NormalizePhone(in.PhoneNumber)
	if !mpesaclient.ValidPhone(phone) {
		return nil, invalid("phone_number", "Enter a valid Safaricom number")
	}

	if s.payouts == nil {
		// No way to pay anyone. Refusing here is the difference between a customer
		// waiting and a customer debited with nothing on the way.
		return nil, ErrWithdrawalsDisabled
	}

	var tx *models.Transaction
	var wallet *models.Wallet
	var payout *models.MpesaPayout
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

		// The daily cap, read under the same lock as the balance so two
		// concurrent withdrawals cannot each see the other's headroom.
		alreadyUSD, err := st.Transactions.WithdrawnTodayUSD(ctx, account.ID)
		if err != nil {
			return err
		}
		dayKES := s.usdToKES(alreadyUSD).Add(amountKES)
		if capKES := decimal.NewFromFloat(s.cfg.MaxWithdrawDayKES); dayKES.GreaterThan(capKES) {
			return invalid("amount", "That would take today's withdrawals past the daily limit of KES "+
				capKES.StringFixed(0)+". Already withdrawn today: KES "+s.usdToKES(alreadyUSD).StringFixed(0))
		}

		tx = &models.Transaction{
			AccountID:   account.ID,
			Type:        models.TxWithdrawal,
			BalanceType: models.BalanceReal,
			Amount:      in.AmountUSD.Neg(),
			Status:      models.StatusPending,
			Metadata: map[string]any{
				"amount_kes": amountKES.StringFixed(2),
				"phone":      maskPhone(phone),
				"channel":    "mpesa",
			},
		}
		if err := st.Transactions.Insert(ctx, tx); err != nil {
			// The partial unique index (migration 0008) is what makes a second
			// in-flight withdrawal impossible rather than merely unlikely. A
			// double-submitted request loses here, before any payout exists to
			// send twice.
			if errors.Is(err, repository.ErrConflict) {
				return ErrWithdrawalInFlight
			}
			return err
		}

		// The payout row is reserved in the SAME transaction as the debit and the
		// ledger row. Two reasons, and both are about what a crash leaves behind:
		// a debit whose payout row is missing has lost the destination number —
		// the ledger keeps only a masked form — and the request id that every
		// later callback and query matches on must exist before anything is sent.
		nextQuery := time.Now().Add(firstQueryDelay)
		payout = &models.MpesaPayout{
			TransactionID:            tx.ID,
			OriginatorConversationID: NewOriginatorConversationID(),
			PhoneNumber:              phone,
			Amount:                   amountKES.Ceil(),
			NextQueryAt:              &nextQuery,
		}
		if err := st.Payouts.Insert(ctx, payout); err != nil {
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

	// Reserved and committed. Now send, outside every lock: the request is a
	// network call, and holding a wallet row across it would block every other
	// writer on this account for as long as Daraja takes. Send records its own
	// outcome on the payout row and refunds through settlePayout when Daraja
	// refuses outright, so there is nothing to handle here.
	s.payouts.Send(ctx, payout)

	// Re-read, because Send may already have settled it: a rejection refunds
	// inside that call, and the mock's callback can land before this line.
	if latest, err := s.store.Transactions.GetByID(ctx, tx.ID); err == nil {
		tx = latest
	}
	if w, err := s.store.Wallets.Get(ctx, account.ID); err == nil {
		wallet = w
	}

	s.notify(account.ID, "transaction", map[string]any{
		"transaction_id": tx.ID,
		"status":         tx.Status,
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
