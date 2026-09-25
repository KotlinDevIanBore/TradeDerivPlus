package service

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// Read paths for the admin console. Split from admin_service.go because that
// file is about changing things and this one is about looking at them.

type Dashboard struct {
	Users               int                         `json:"users"`
	DepositsToday       int                         `json:"deposits_today"`
	DepositsTodayUSD    decimal.Decimal             `json:"deposits_today_usd"`
	WithdrawalsToday    int                         `json:"withdrawals_today"`
	WithdrawalsTodayUSD decimal.Decimal             `json:"withdrawals_today_usd"`
	OpenPositions       int                         `json:"open_positions"`
	LiveConnections     int                         `json:"live_connections"`
	PendingKYC          int                         `json:"pending_kyc"`
	LiveBotRuns         int                         `json:"live_bot_runs"`
	BotEnabled          bool                        `json:"bot_enabled"`
	DriftedAccounts     int                         `json:"drifted_accounts"`
	Drift               []repository.AccountDrift   `json:"drift"`
	DepositsByHour      []repository.HourlyDeposits `json:"deposits_by_hour"`
}

// ConnectionCounter is however the transport reports live sockets. Keeping it
// an interface means this service still does not import the WebSocket layer.
type ConnectionCounter interface{ ClientCount() int }

func (s *AdminService) SetConnectionCounter(c ConnectionCounter) { s.connections = c }

func (s *AdminService) Dashboard(ctx context.Context) (*Dashboard, error) {
	d := &Dashboard{}
	var err error

	if d.Users, err = s.store.Users.Count(ctx); err != nil {
		return nil, err
	}
	if d.DepositsToday, d.DepositsTodayUSD, err = s.store.Transactions.TodayTotals(ctx, models.TxDeposit); err != nil {
		return nil, err
	}
	if d.WithdrawalsToday, d.WithdrawalsTodayUSD, err = s.store.Transactions.TodayTotals(ctx, models.TxWithdrawal); err != nil {
		return nil, err
	}
	if d.OpenPositions, err = s.store.Trades.CountOpen(ctx); err != nil {
		return nil, err
	}
	if d.PendingKYC, err = s.store.KYC.PendingCount(ctx); err != nil {
		return nil, err
	}
	if d.LiveBotRuns, err = s.store.BotRuns.CountLive(ctx); err != nil {
		return nil, err
	}
	// Read through to the row rather than through SettingsService's cache. The
	// console is where the flag is changed, and an operator who has just turned
	// it off should not be shown "on" for another ten seconds.
	if d.BotEnabled, err = s.botEnabled(ctx); err != nil {
		return nil, err
	}
	// The tile that matters most: any account whose ledger and cached balance
	// disagree. In a healthy system this is always zero.
	if d.Drift, d.DriftedAccounts, err = s.store.Transactions.Drift(ctx, 20); err != nil {
		return nil, err
	}
	if d.DepositsByHour, err = s.store.Transactions.DepositsByHour(ctx); err != nil {
		return nil, err
	}
	if s.connections != nil {
		d.LiveConnections = s.connections.ClientCount()
	}
	return d, nil
}

// AdminUserRow is one row of the user list, with everything the table shows.
type AdminUserRow struct {
	models.User
	Accounts     []models.Account `json:"accounts"`
	RealBalance  decimal.Decimal  `json:"real_balance"`
	TotalBalance decimal.Decimal  `json:"total_balance"`
}

type AdminUserPage struct {
	Items  []AdminUserRow `json:"items"`
	Total  int            `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
	Page   int            `json:"page"`
	Pages  int            `json:"pages"`
}

// Users searches and returns rows already carrying their accounts and
// balances, so the table is one round trip rather than one query per row.
func (s *AdminService) Users(ctx context.Context, query string, limit, offset int) (*AdminUserPage, error) {
	users, total, err := s.store.Users.Search(ctx, query, limit, offset)
	if err != nil {
		return nil, err
	}

	ids := make([]uuid.UUID, 0, len(users))
	for _, u := range users {
		ids = append(ids, u.ID)
	}

	byUser := map[uuid.UUID][]models.Account{}
	if len(ids) > 0 {
		if byUser, err = s.store.Accounts.ListByUsers(ctx, ids); err != nil {
			return nil, err
		}
	}

	rows := make([]AdminUserRow, 0, len(users))
	for _, u := range users {
		row := AdminUserRow{User: u, Accounts: byUser[u.ID]}
		for _, a := range row.Accounts {
			w, err := s.store.Wallets.Get(ctx, a.ID)
			if err != nil {
				continue // an account without a wallet row is broken, not fatal to the list
			}
			row.RealBalance = row.RealBalance.Add(w.RealBalance)
			row.TotalBalance = row.TotalBalance.Add(w.Total())
		}
		if row.Accounts == nil {
			row.Accounts = []models.Account{}
		}
		rows = append(rows, row)
	}

	return &AdminUserPage{Items: rows, Total: total, Limit: limit, Offset: offset}, nil
}

// AdminUserDetail is everything the detail screen needs, in one call.
type AdminUserDetail struct {
	User         models.User            `json:"user"`
	Accounts     []AdminAccountDetail   `json:"accounts"`
	Transactions []models.Transaction   `json:"transactions"`
	OpenTrades   []models.Trade         `json:"open_trades"`
	KYC          []models.KYCSubmission `json:"kyc_submissions"`
	Audit        []models.AuditEntry    `json:"audit"`
}

type AdminAccountDetail struct {
	models.Account
	Wallet *models.Wallet `json:"wallet"`
}

func (s *AdminService) UserDetail(ctx context.Context, userID uuid.UUID) (*AdminUserDetail, error) {
	user, err := s.store.Users.GetByID(ctx, userID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, ErrNotFound
		}
		return nil, err
	}

	accounts, err := s.store.Accounts.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	detail := &AdminUserDetail{
		User:         *user,
		Accounts:     make([]AdminAccountDetail, 0, len(accounts)),
		Transactions: []models.Transaction{},
		OpenTrades:   []models.Trade{},
	}

	for _, a := range accounts {
		row := AdminAccountDetail{Account: a}
		if w, err := s.store.Wallets.Get(ctx, a.ID); err == nil {
			row.Wallet = w
		}
		detail.Accounts = append(detail.Accounts, row)

		txs, _, err := s.store.Transactions.ListByAccount(ctx, a.ID, nil, 25, 0)
		if err != nil {
			return nil, err
		}
		detail.Transactions = append(detail.Transactions, txs...)

		trades, _, err := s.store.Trades.ListByAccount(ctx, a.ID, models.TradeOpen, 50, 0)
		if err != nil {
			return nil, err
		}
		detail.OpenTrades = append(detail.OpenTrades, trades...)
	}

	if detail.KYC, err = s.store.KYC.ListForUser(ctx, userID); err != nil {
		return nil, err
	}
	if detail.Audit, err = s.store.Audit.ListForUser(ctx, userID, 50); err != nil {
		return nil, err
	}
	return detail, nil
}

type AdminTransactionPage struct {
	Items  []repository.AdminTransaction `json:"items"`
	Total  int                           `json:"total"`
	Limit  int                           `json:"limit"`
	Offset int                           `json:"offset"`
	Page   int                           `json:"page"`
	Pages  int                           `json:"pages"`
}

func (s *AdminService) Transactions(ctx context.Context, query, txType, status string, limit, offset int) (*AdminTransactionPage, error) {
	items, total, err := s.store.Transactions.SearchAll(ctx, query, txType, status, limit, offset)
	if err != nil {
		return nil, err
	}
	return &AdminTransactionPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

type KYCPage struct {
	Items  []models.KYCSubmission `json:"items"`
	Total  int                    `json:"total"`
	Limit  int                    `json:"limit"`
	Offset int                    `json:"offset"`
	Page   int                    `json:"page"`
	Pages  int                    `json:"pages"`
}

func (s *AdminService) KYCQueue(ctx context.Context, status string, limit, offset int) (*KYCPage, error) {
	items, total, err := s.store.KYC.List(ctx, status, limit, offset)
	if err != nil {
		return nil, err
	}
	return &KYCPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}

type AuditPage struct {
	Items  []models.AuditEntry `json:"items"`
	Total  int                 `json:"total"`
	Limit  int                 `json:"limit"`
	Offset int                 `json:"offset"`
	Page   int                 `json:"page"`
	Pages  int                 `json:"pages"`
}

// PayoutPage is the console's payout list.
type PayoutPage struct {
	Items []repository.AdminPayout `json:"items"`
	Total int                      `json:"total"`
	Page  int                      `json:"page"`
	Pages int                      `json:"pages"`
	Limit int                      `json:"limit"`
}

// Payouts backs the operator's view of withdrawals that have not resolved. The
// default filter is "open", because that is the only reason to open the screen.
func (s *AdminService) Payouts(ctx context.Context, filter string, limit, offset int) (*PayoutPage, error) {
	if filter == "" {
		filter = "open"
	}
	items, total, err := s.store.Payouts.ListForAdmin(ctx, filter, limit, offset)
	if err != nil {
		return nil, err
	}
	return &PayoutPage{Items: items, Total: total, Limit: limit}, nil
}

func (s *AdminService) AuditLog(ctx context.Context, adminID *uuid.UUID, action string, limit, offset int) (*AuditPage, error) {
	items, total, err := s.store.Audit.List(ctx, adminID, action, limit, offset)
	if err != nil {
		return nil, err
	}
	return &AuditPage{Items: items, Total: total, Limit: limit, Offset: offset}, nil
}
