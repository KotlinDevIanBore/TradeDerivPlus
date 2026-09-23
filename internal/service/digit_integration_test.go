//go:build integration

package service_test

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
)

const digitSymbol = "R_75"

func TestDigitWinEven(t *testing.T) {
	h := newHarness(t)
	h.digits.Start(h.ctx)

	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	before := h.wallet_(accountID)
	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.1234"), 101)
	time.Sleep(50 * time.Millisecond)

	got, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.DigitWon {
		t.Fatalf("want won, got %s digit=%v", got.Status, got.SettlementDigit)
	}
	after := h.wallet_(accountID)
	delta := after.BonusBalance.Sub(before.BonusBalance)
	want := decimal.RequireFromString("9.52")
	if !delta.Equal(want) {
		t.Fatalf("bonus delta %s, want %s", delta, want)
	}
	h.assertReconciled(accountID)
}

func TestDigitLossOdd(t *testing.T) {
	h := newHarness(t)
	h.digits.Start(h.ctx)

	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	before := h.wallet_(accountID)
	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityOdd},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.1234"), 101)
	time.Sleep(50 * time.Millisecond)

	got, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.DigitLost {
		t.Fatalf("want lost, got %s", got.Status)
	}
	after := h.wallet_(accountID)
	if !after.BonusBalance.Sub(before.BonusBalance).Equal(decimal.NewFromInt(-10)) {
		t.Fatalf("bonus delta %s, want -10", after.BonusBalance.Sub(before.BonusBalance))
	}
	h.assertReconciled(accountID)
}

func TestDigitDoubleSettleRace(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	settleTick := models.Tick{Symbol: digitSymbol, Price: decimal.RequireFromString("100.1234"), Epoch: 101}
	h.feed.InjectTickQuiet(digitSymbol, settleTick.Price, settleTick.Epoch)

	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
			if err != nil {
				return
			}
			if _, err := h.digits.SettleForTest(h.ctx, c, settleTick, "race"); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()

	if wins.Load() != 1 {
		t.Fatalf("settlements succeeded %d times, want exactly 1", wins.Load())
	}
	got, _ := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if got.Status != models.DigitWon {
		t.Fatalf("status %s", got.Status)
	}
	h.assertReconciled(accountID)
}

func TestDigitSweepAfterMissedTick(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	h.feed.InjectTickQuiet(digitSymbol, decimal.RequireFromString("100.1234"), 101)
	h.digits.SweepForTest(h.ctx)

	got, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.DigitWon {
		t.Fatalf("want swept to won, got %s", got.Status)
	}
	h.assertReconciled(accountID)
}

func TestDigitAutoVoid(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	before := h.wallet_(accountID)
	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if _, err := h.store.Pool().Exec(h.ctx,
		`UPDATE digit_contracts SET void_after = now() - interval '1 second' WHERE id = $1`, contract.ID); err != nil {
		t.Fatalf("backdate void_after: %v", err)
	}

	h.digits.SweepForTest(h.ctx)

	got, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.DigitVoid {
		t.Fatalf("want void, got %s", got.Status)
	}
	after := h.wallet_(accountID)
	if !after.BonusBalance.Equal(before.BonusBalance) {
		t.Fatalf("void should refund: before %s after %s", before.BonusBalance, after.BonusBalance)
	}
	h.assertReconciled(accountID)
}

func TestDigitRestartRecovery(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	contract, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	h.feed.InjectTickQuiet(digitSymbol, decimal.RequireFromString("100.1234"), 101)
	h.digits.Start(h.ctx)
	time.Sleep(50 * time.Millisecond)

	got, err := h.store.DigitContracts.GetByID(h.ctx, contract.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != models.DigitWon {
		t.Fatalf("boot sweep should settle: got %s", got.Status)
	}
	h.assertReconciled(accountID)
}

func TestDigitRejectsNonVolatility(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(1000))
	h.feed.InjectTickAt("EURUSD", decimal.RequireFromString("1.10000"), 100)

	_, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: "EURUSD",
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(10),
	})
	var ve *service.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want validation error, got %v", err)
	}
}
