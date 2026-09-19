//go:build integration

package service_test

import (
	"testing"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

func TestOpenValidatesItsInputs(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	sl := decimal.NewFromInt(110) // above entry: wrong side for a buy
	tp := decimal.NewFromInt(90)  // below entry: wrong side for a buy

	cases := []struct {
		name  string
		in    service.OpenTradeInput
		field string
	}{
		{
			"an unknown direction",
			service.OpenTradeInput{Direction: "sideways", Stake: decimal.NewFromInt(10)},
			"direction",
		},
		{
			"a zero stake",
			service.OpenTradeInput{Direction: models.DirectionBuy, Stake: decimal.Zero},
			"stake",
		},
		{
			"a negative stake",
			service.OpenTradeInput{Direction: models.DirectionBuy, Stake: decimal.NewFromInt(-5)},
			"stake",
		},
		{
			"a stop loss above entry on a buy",
			service.OpenTradeInput{Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10), StopLoss: &sl},
			"stop_loss",
		},
		{
			"a take profit below entry on a buy",
			service.OpenTradeInput{Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10), TakeProfit: &tp},
			"take_profit",
		},
		{
			"a stop loss below entry on a sell",
			service.OpenTradeInput{Direction: models.DirectionSell, Stake: decimal.NewFromInt(10), StopLoss: &tp},
			"stop_loss",
		},
		{
			"a take profit above entry on a sell",
			service.OpenTradeInput{Direction: models.DirectionSell, Stake: decimal.NewFromInt(10), TakeProfit: &sl},
			"take_profit",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := c.in
			in.UserID, in.AccountID, in.SymbolCode = userID, accountID, testSymbol

			_, err := h.trading.Open(h.ctx, in)
			var ve *service.ValidationError
			if !asValidation(err, &ve) {
				t.Fatalf("got %v, want a validation error", err)
			}
			if ve.Field != c.field {
				t.Errorf("error named field %q, want %q", ve.Field, c.field)
			}
		})
	}

	// Nothing above should have moved a cent.
	if w := h.wallet_(accountID); !w.Total().Equal(decimal.NewFromInt(1000)) {
		t.Fatalf("balance %s after only invalid attempts, want 1000", w.Total())
	}
	h.assertReconciled(accountID)
}

func TestOpenRefusesUnknownAndInactiveSymbols(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))

	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: "NOT_A_SYMBOL",
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
	}); err != service.ErrNotFound {
		t.Errorf("unknown symbol returned %v, want ErrNotFound", err)
	}

	// A symbol with no tick yet cannot be priced, so it cannot be traded.
	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: "EURUSD",
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
	}); err != service.ErrNoPrice {
		t.Errorf("unpriced symbol returned %v, want ErrNoPrice", err)
	}

	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE symbols SET is_active = false WHERE code = $1`, "BOOM500"); err != nil {
		t.Fatal(err)
	}
	h.setPrice("BOOM500", 9000)

	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: "BOOM500",
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
	}); err != service.ErrMarketClosed {
		t.Errorf("inactive symbol returned %v, want ErrMarketClosed", err)
	}

	h.assertReconciled(accountID)
}

func TestCrossUserTradingIsRefused(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	otherUser, _ := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: otherUser, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
	}); err != service.ErrForbidden {
		t.Errorf("trading on another user's account returned %v, want ErrForbidden", err)
	}

	trade, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.trading.Close(h.ctx, otherUser, trade.ID); err != service.ErrForbidden {
		t.Errorf("closing another user's position returned %v, want ErrForbidden", err)
	}
	if _, err := h.trading.List(h.ctx, otherUser, accountID, models.TradeOpen, 25, 0); err != service.ErrForbidden {
		t.Errorf("listing another user's trades returned %v, want ErrForbidden", err)
	}
	if _, err := h.trading.Summary(h.ctx, otherUser, accountID); err != service.ErrForbidden {
		t.Errorf("reading another user's summary returned %v, want ErrForbidden", err)
	}
	h.assertReconciled(accountID)
}

func TestListAndSummaryReflectPositions(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	empty, err := h.trading.Summary(h.ctx, userID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if empty.OpenPositions != 0 || !empty.OpenPnL.IsZero() {
		t.Fatalf("empty summary reported %d positions and %s pnl", empty.OpenPositions, empty.OpenPnL)
	}
	if !empty.PricesComplete {
		t.Error("an account with no positions has no missing prices")
	}

	for i := 0; i < 3; i++ {
		if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
			UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
			Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
		}); err != nil {
			t.Fatal(err)
		}
	}

	open, err := h.trading.List(h.ctx, userID, accountID, models.TradeOpen, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if open.Total != 3 {
		t.Fatalf("total %d open positions, want 3", open.Total)
	}
	if len(open.Items) != 2 {
		t.Fatalf("page holds %d, want 2", len(open.Items))
	}

	// A 10% move on 300 staked is 30 of unrealised profit.
	h.setPrice(testSymbol, 110)
	summary, err := h.trading.Summary(h.ctx, userID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.OpenPositions != 3 {
		t.Fatalf("summary counted %d positions, want 3", summary.OpenPositions)
	}
	if want := decimal.NewFromInt(30); !summary.OpenPnL.Equal(want) {
		t.Fatalf("open pnl %s, want %s", summary.OpenPnL, want)
	}

	// Close one and it should move to the closed tab and into today's realised.
	if _, err := h.trading.Close(h.ctx, userID, open.Items[0].ID); err != nil {
		t.Fatal(err)
	}

	closed, err := h.trading.List(h.ctx, userID, accountID, models.TradeClosed, 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if closed.Total != 1 {
		t.Fatalf("%d closed positions, want 1", closed.Total)
	}

	after, err := h.trading.Summary(h.ctx, userID, accountID)
	if err != nil {
		t.Fatal(err)
	}
	if after.OpenPositions != 2 {
		t.Fatalf("summary counted %d open after closing one, want 2", after.OpenPositions)
	}
	if want := decimal.NewFromInt(10); !after.RealisedToday.Equal(want) {
		t.Fatalf("realised today %s, want %s", after.RealisedToday, want)
	}

	// An empty status means both tabs.
	both, err := h.trading.List(h.ctx, userID, accountID, "", 25, 0)
	if err != nil {
		t.Fatal(err)
	}
	if both.Total != 3 {
		t.Fatalf("unfiltered list holds %d, want 3", both.Total)
	}

	h.assertReconciled(accountID)
}

// A stake is taken from bonus first so promotional money is consumed before a
// user's own deposits, and the close must credit back the same balance.
func TestStakeDrawsBonusFirstAndReturnsThere(t *testing.T) {
	h := newHarness(t)
	adminID := h.newAdmin()
	userID, accountID := h.newUser(decimal.NewFromInt(50)) // 50 bonus
	h.setPrice(testSymbol, 100)

	if _, _, err := h.admin.Adjust(h.ctx, service.AdjustInput{
		AdminID: adminID, AccountID: accountID, Amount: decimal.NewFromInt(500),
		BalanceType: models.BalanceReal, Reason: "real money that must not be touched first",
	}); err != nil {
		t.Fatal(err)
	}

	// 40 fits inside the bonus balance, so bonus pays.
	small, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(40),
	})
	if err != nil {
		t.Fatal(err)
	}
	w := h.wallet_(accountID)
	if !w.BonusBalance.Equal(decimal.NewFromInt(10)) {
		t.Fatalf("bonus %s after a 40 stake, want 10", w.BonusBalance)
	}
	if !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("real %s was touched when bonus could cover the stake", w.RealBalance)
	}

	// Closing at a profit must credit the bonus balance, not turn into real
	// money the user could withdraw.
	h.setPrice(testSymbol, 110)
	if _, err := h.trading.Close(h.ctx, userID, small.ID); err != nil {
		t.Fatal(err)
	}
	w = h.wallet_(accountID)
	if !w.RealBalance.Equal(decimal.NewFromInt(500)) {
		t.Fatalf("real balance became %s: bonus winnings leaked into withdrawable money", w.RealBalance)
	}
	if want := decimal.NewFromInt(54); !w.BonusBalance.Equal(want) {
		t.Fatalf("bonus %s after closing at a profit, want %s", w.BonusBalance, want)
	}

	// 100 exceeds the remaining bonus, so real pays.
	h.setPrice(testSymbol, 100)
	if _, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(100),
	}); err != nil {
		t.Fatal(err)
	}
	w = h.wallet_(accountID)
	if !w.RealBalance.Equal(decimal.NewFromInt(400)) {
		t.Fatalf("real %s after a 100 stake bonus could not cover, want 400", w.RealBalance)
	}

	h.assertReconciled(accountID)
}

func TestCloseRequiresALivePrice(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.setPrice(testSymbol, 100)

	trade, err := h.trading.Open(h.ctx, service.OpenTradeInput{
		UserID: userID, AccountID: accountID, SymbolCode: testSymbol,
		Direction: models.DirectionBuy, Stake: decimal.NewFromInt(50),
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := h.trading.Close(h.ctx, userID, trade.ID); err != nil {
		t.Fatalf("close with a live price: %v", err)
	}
	if _, err := h.trading.Close(h.ctx, userID, trade.ID); err != service.ErrTradeNotOpen {
		t.Errorf("second close returned %v, want ErrTradeNotOpen", err)
	}

	h.assertReconciled(accountID)
}
