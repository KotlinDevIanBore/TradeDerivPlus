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

func TestDigitMaxStakeRejected(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(100000))
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	// Well within the balance, so only the ceiling can refuse it. This is the
	// case the client cannot be trusted with: the ticket checks the balance, not
	// the limit.
	_, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(101),
	})
	var ve *service.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want validation error, got %v", err)
	}
	if ve.Field != "stake" {
		t.Fatalf("field %q, want stake", ve.Field)
	}

	// The boundary itself is allowed, so the message and the rule agree.
	if _, err := h.digits.Open(h.ctx, service.OpenDigitInput{
		UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
		ContractType: models.DigitEvenOdd,
		Selection:    models.DigitSelection{Parity: models.DigitParityEven},
		Stake:        decimal.NewFromInt(100),
	}); err != nil {
		t.Fatalf("stake at the ceiling should open: %v", err)
	}
	h.assertReconciled(accountID)
}

func TestDigitOpenCapIsRaceFree(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.NewFromInt(100000))
	// No settlement tick follows, so every contract opened here stays open and
	// counts against the cap.
	h.feed.InjectTickAt(digitSymbol, decimal.RequireFromString("100.10"), 100)

	const cap_ = 5 // the harness's MaxOpenPerAccount
	var opened, refused atomic.Int32
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < cap_*2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := h.digits.Open(h.ctx, service.OpenDigitInput{
				UserID: userID, AccountID: accountID, SymbolCode: digitSymbol,
				ContractType: models.DigitEvenOdd,
				Selection:    models.DigitSelection{Parity: models.DigitParityEven},
				Stake:        decimal.NewFromInt(10),
			})
			switch {
			case err == nil:
				opened.Add(1)
			case errors.Is(err, service.ErrTooManyOpen):
				refused.Add(1)
			default:
				t.Errorf("unexpected error: %v", err)
			}
		}()
	}
	close(start)
	wg.Wait()

	if opened.Load() != cap_ {
		t.Fatalf("opened %d, want exactly %d", opened.Load(), cap_)
	}
	if refused.Load() != cap_ {
		t.Fatalf("refused %d, want %d", refused.Load(), cap_)
	}

	// The cap is about how many contracts exist, not how many the caller thinks
	// it made, so it is checked against the table.
	var n int
	if err := h.store.Pool().QueryRow(h.ctx,
		`SELECT count(*) FROM digit_contracts WHERE account_id = $1 AND status = 'open'`,
		accountID).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != cap_ {
		t.Fatalf("%d open contracts in the table, want %d", n, cap_)
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
