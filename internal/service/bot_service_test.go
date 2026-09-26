package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

// botLimitReached is the stop policy that depends on nothing but the run's row
// and the clock, so it can be tested exhaustively without a database, a feed or a
// goroutine. The integration suite proves the loop honours it; this proves the
// policy itself, including the cases a run would take a long time to reach.

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func ip(n int) *int                { return &n }
func dp(s string) *decimal.Decimal { d := dec(s); return &d }

// runRow is a minimal live run with the given totals. Stop loss is always set,
// because the service refuses a run without one.
func runRow(mutate func(*models.BotRun)) *models.BotRun {
	r := &models.BotRun{
		Status:    models.BotRunning,
		Stake:     dec("10"),
		StartedAt: time.Now(),
		Limits:    models.BotLimitsConfig{StopLoss: dec("100")},
	}
	if mutate != nil {
		mutate(r)
	}
	return r
}

func TestBotLimitReached(t *testing.T) {
	now := time.Now()

	cases := []struct {
		name   string
		run    *models.BotRun
		want   string
		expect bool
	}{
		{
			name: "a fresh run has no reason to stop",
			run:  runRow(nil),
		},
		{
			name: "losses short of the stop loss keep going",
			run:  runRow(func(r *models.BotRun) { r.RealisedPnL = dec("-99.99") }),
		},
		{
			name:   "stop loss fires exactly on the threshold",
			run:    runRow(func(r *models.BotRun) { r.RealisedPnL = dec("-100") }),
			want:   models.BotStopStopLoss,
			expect: true,
		},
		{
			name:   "stop loss fires past the threshold",
			run:    runRow(func(r *models.BotRun) { r.RealisedPnL = dec("-100.01") }),
			want:   models.BotStopStopLoss,
			expect: true,
		},
		{
			name: "no take profit set means profit never stops the run",
			run:  runRow(func(r *models.BotRun) { r.RealisedPnL = dec("100000") }),
		},
		{
			name: "profit below the take profit keeps going",
			run: runRow(func(r *models.BotRun) {
				r.Limits.TakeProfit = dp("50")
				r.RealisedPnL = dec("49.99")
			}),
		},
		{
			name: "take profit fires exactly on the threshold",
			run: runRow(func(r *models.BotRun) {
				r.Limits.TakeProfit = dp("50")
				r.RealisedPnL = dec("50")
			}),
			want:   models.BotStopTakeProfit,
			expect: true,
		},
		{
			name: "a losing streak short of the limit keeps going",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxConsecutiveLosses = ip(3)
				r.ConsecutiveLosses = 2
			}),
		},
		{
			name: "the losing streak limit fires on the threshold",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxConsecutiveLosses = ip(3)
				r.ConsecutiveLosses = 3
			}),
			want:   models.BotStopMaxConsecutiveLosses,
			expect: true,
		},
		{
			name: "voids do not count towards the losing streak",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxConsecutiveLosses = ip(3)
				r.ConsecutiveLosses = 1
				r.Voids = 9
				r.ConsecutiveVoids = 2
			}),
		},
		{
			name: "max contracts counts placements, voids included",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxContracts = ip(5)
				r.ContractsPlaced = 5
				r.Voids = 5 // every one voided, so nothing was won or lost
			}),
			want:   models.BotStopMaxContracts,
			expect: true,
		},
		{
			name: "one short of max contracts keeps going",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxContracts = ip(5)
				r.ContractsPlaced = 4
			}),
		},
		{
			name: "a duration still running keeps going",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxDurationSeconds = ip(600)
				r.StartedAt = now.Add(-5 * time.Minute)
			}),
		},
		{
			name: "a duration past its limit stops",
			run: runRow(func(r *models.BotRun) {
				r.Limits.MaxDurationSeconds = ip(600)
				r.StartedAt = now.Add(-11 * time.Minute)
			}),
			want:   models.BotStopMaxDuration,
			expect: true,
		},
		{
			// The regression this guards: an earlier draft derived the deadline from
			// StartedAt with a zero duration, which is a deadline in the past, so
			// every run with no duration limit stopped on its first check.
			name: "no duration limit never stops on duration",
			run: runRow(func(r *models.BotRun) {
				r.StartedAt = now.Add(-1000 * time.Hour)
			}),
		},
		{
			name: "two voids in a row is not yet a dead feed",
			run:  runRow(func(r *models.BotRun) { r.ConsecutiveVoids = 2 }),
		},
		{
			name:   "three voids in a row means the feed is not delivering",
			run:    runRow(func(r *models.BotRun) { r.ConsecutiveVoids = 3 }),
			want:   models.BotStopFeedDown,
			expect: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason, stop := botLimitReached(tc.run, now)
			if stop != tc.expect {
				t.Fatalf("stop = %v (%q), want %v", stop, reason, tc.expect)
			}
			if stop && reason != tc.want {
				t.Fatalf("reason %q, want %q", reason, tc.want)
			}
			if !stop && reason != "" {
				t.Fatalf("not stopping but gave reason %q", reason)
			}
		})
	}
}

// When more than one condition is met the run should report the one the user is
// most likely to care about, and the order must be stable — a run that hit its
// stop loss should not be reported as having simply run out of contracts.
func TestBotLimitPrecedence(t *testing.T) {
	now := time.Now()
	run := runRow(func(r *models.BotRun) {
		r.RealisedPnL = dec("-500") // stop loss
		r.ConsecutiveLosses = 10    // and a losing streak
		r.ContractsPlaced = 100     // and out of contracts
		r.ConsecutiveVoids = 5      // and a dead feed
		r.Limits.MaxConsecutiveLosses = ip(3)
		r.Limits.MaxContracts = ip(5)
		r.Limits.MaxDurationSeconds = ip(1)
		r.StartedAt = now.Add(-time.Hour)
	})

	reason, stop := botLimitReached(run, now)
	if !stop {
		t.Fatal("want a stop")
	}
	if reason != models.BotStopStopLoss {
		t.Fatalf("reason %q, want stop_loss to win: money lost is what a user asked to be protected from", reason)
	}
}

func TestBotLimitsDefaults(t *testing.T) {
	got := BotLimits{}.withDefaults()
	if got.MinIntervalMS != defaultBotMinIntervalMS {
		t.Errorf("MinIntervalMS %d, want the default %d", got.MinIntervalMS, defaultBotMinIntervalMS)
	}
	if got.MaxRuns != defaultBotMaxRuns {
		t.Errorf("MaxRuns %d, want the default %d", got.MaxRuns, defaultBotMaxRuns)
	}

	// A zero must take the default rather than mean "no limit": the dangerous
	// direction of a mistake here is upwards.
	negative := BotLimits{MinIntervalMS: -1, MaxRuns: -1}.withDefaults()
	if negative.MinIntervalMS <= 0 || negative.MaxRuns <= 0 {
		t.Errorf("a negative limit must fall back to a default, got %+v", negative)
	}

	explicit := BotLimits{MinIntervalMS: 250, MaxRuns: 2}.withDefaults()
	if explicit.MinIntervalMS != 250 || explicit.MaxRuns != 2 {
		t.Errorf("explicit limits must survive, got %+v", explicit)
	}
}

// The stake ceiling is a share of the balance, configurable and clamped. The
// clamping is the point: an unset, negative or absurd fraction has to land on the
// default rather than on "no limit", because the dangerous direction is upwards.
func TestBotMaxStakeFractionDefaults(t *testing.T) {
	cases := []struct {
		name string
		in   decimal.Decimal
		want decimal.Decimal
	}{
		{"unset takes the default", decimal.Decimal{}, defaultBotMaxStakeFraction},
		{"zero is not unlimited", dec("0"), defaultBotMaxStakeFraction},
		{"negative is not unlimited", dec("-0.5"), defaultBotMaxStakeFraction},
		{"a whole balance is allowed", dec("1"), dec("1")},
		{"more than the balance is no cap at all, so it is refused", dec("1.5"), defaultBotMaxStakeFraction},
		{"a configured share survives", dec("0.25"), dec("0.25")},
		{"a small share survives", dec("0.005"), dec("0.005")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BotLimits{MaxStakeFraction: tc.in}.withDefaults().MaxStakeFraction
			if !got.Equal(tc.want) {
				t.Errorf("MaxStakeFraction %s, want %s", got, tc.want)
			}
		})
	}
}

// The percentage in the refusal message is derived, not written out, so a
// configured share cannot be enforced as one number and announced as another.
func TestBotMaxStakePercentLabel(t *testing.T) {
	cases := []struct{ fraction, want string }{
		{"0.1", "10%"},
		{"0.25", "25%"},
		{"0.075", "7.5%"},
		{"0.005", "0.5%"},
		{"1", "100%"},
	}
	for _, tc := range cases {
		s := &BotService{limits: BotLimits{MaxStakeFraction: dec(tc.fraction)}}
		if got := s.MaxStakePercentLabel(); got != tc.want {
			t.Errorf("fraction %s reads %q, want %q", tc.fraction, got, tc.want)
		}
	}

	// And the default reads as the tenth it has always been, so this change is
	// invisible to anyone who has not set the variable.
	s := &BotService{limits: BotLimits{}.withDefaults()}
	if got := s.MaxStakePercentLabel(); got != "10%" {
		t.Errorf("the default reads %q, want 10%%", got)
	}
}

// validateRunConfig is the only thing between a misconfigured run and a few
// hundred contracts, and it needs no database: a service value with limits and a
// per-contract ceiling is enough to test the whole policy. Only the balance
// ceiling is covered here, because that is the rule that just became configurable.
func botValidator(fraction string) *BotService {
	return &BotService{
		limits: BotLimits{MaxStakeFraction: dec(fraction)}.withDefaults(),
		digits: &DigitService{limits: DigitLimits{MaxStake: dec("100")}},
	}
}

func validStartRun(stake string) StartRunInput {
	return StartRunInput{
		Stake:      dec(stake),
		IntervalMS: 1000,
		Limits:     models.BotLimitsConfig{StopLoss: dec("10")},
	}
}

func TestBotValidateRunConfigBalanceCeiling(t *testing.T) {
	// $400 against a $100 per-contract ceiling, so every share under a quarter puts
	// the balance ceiling below the per-contract one and this test measures the rule
	// it means to measure rather than the one checked before it.
	balance := dec("400")

	cases := []struct {
		name     string
		fraction string
		stake    string
		wantMsg  string
	}{
		{name: "a tenth allows the tenth exactly", fraction: "0.1", stake: "40"},
		{
			name:     "a tenth refuses a cent past it",
			fraction: "0.1",
			stake:    "40.01",
			wantMsg:  "An automated run can stake at most 10% of your balance, which is $40.00",
		},
		{name: "a configured fifth allows a stake a tenth refuses", fraction: "0.2", stake: "40.01"},
		{
			// The ceiling, the percentage and the refusal all have to follow the
			// configured share together. A hardcoded 0.1 anywhere breaks one of them.
			name:     "a configured fifth refuses past a fifth",
			fraction: "0.2",
			stake:    "80.01",
			wantMsg:  "An automated run can stake at most 20% of your balance, which is $80.00",
		},
		{
			name:     "a configured fortieth refuses a stake a tenth would allow",
			fraction: "0.025",
			stake:    "20",
			wantMsg:  "An automated run can stake at most 2.5% of your balance, which is $10.00",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := botValidator(tc.fraction).validateRunConfig(validStartRun(tc.stake), balance)
			if tc.wantMsg == "" {
				if err != nil {
					t.Fatalf("stake %s at %s of %s refused: %v", tc.stake, tc.fraction, balance, err)
				}
				return
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error %v, want a ValidationError", err)
			}
			if ve.Field != "stake" {
				t.Errorf("field %q, want stake", ve.Field)
			}
			if ve.Message != tc.wantMsg {
				t.Errorf("message:\n got %q\nwant %q", ve.Message, tc.wantMsg)
			}
		})
	}

	// The per-contract ceiling still wins when both are exceeded: it is the
	// smaller of the two here, and it is the one a user can act on without
	// depositing.
	err := botValidator("0.5").validateRunConfig(validStartRun("150"), balance)
	if err == nil || !strings.Contains(err.Error(), "on one contract") {
		t.Errorf("error %v, want the per-contract ceiling to answer first", err)
	}
}
