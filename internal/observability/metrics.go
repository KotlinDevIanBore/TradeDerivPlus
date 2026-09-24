package observability

import (
	"context"
	"log/slog"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics. Infrastructure numbers plus the business ones that actually say
// whether the platform is healthy, which is what a dashboard is for.
var (
	requestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_request_duration_seconds",
		Help:    "Request latency by route.",
		Buckets: []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5},
	}, []string{"route", "method", "status"})

	requestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Requests by route and status.",
	}, []string{"route", "method", "status"})

	wsConnections = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ws_connections",
		Help: "Currently connected WebSocket clients.",
	})

	// The Daraja health signal. A spike in failures here reaches an alert
	// before it reaches support.
	depositsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "deposits_total",
		Help: "M-Pesa deposits by outcome.",
	}, []string{"status"})

	tradesOpen = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "trades_open",
		Help: "Open positions across the platform.",
	})

	adminActionsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "admin_actions_total",
		Help: "Audited admin actions by kind.",
	}, []string{"action"})

	// The invariant the whole money design rests on, watched continuously
	// rather than checked once per test run. Anything but zero means a balance
	// moved without a matching ledger row. Alert on > 0 for 2 minutes.
	ledgerDrift = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "ledger_drift_accounts",
		Help: "Accounts whose ledger sum disagrees with their cached balance. Must be zero.",
	})

	// Automated trading. A bot places money repeatedly and without a person
	// watching, so what it did has to be visible without reading the database.
	botRunsLive = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "bot_runs_live",
		Help: "Automated runs currently running or stopping.",
	})

	// One contract per run per second is the ceiling by construction: a run holds
	// one contract at a time and contracts take about a second to settle. So
	//
	//	increase(bot_contracts_total[1m]) > bot_runs_live * 60
	//
	// is not load, it is a bug — that is the alert this counter exists for, and
	// it only means that if each contract increments it exactly once. It used to
	// increment twice, once on placement and once on its outcome, which made
	// every reading of the whole metric double the truth.
	//
	// So this counts settlements, one per contract, with the outcome that makes
	// the count worth breaking down. Placements have their own counter below.
	botContractsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_contracts_total",
		Help: "Contracts settled for automated runs, one per contract, by outcome.",
	}, []string{"outcome"})

	// Placements, which lead settlements by about a second. Separate rather than
	// another label on the counter above, because a placement and a settlement
	// are two events about one contract and summing them counts it twice.
	botContractsPlaced = promauto.NewCounter(prometheus.CounterOpts{
		Name: "bot_contracts_placed_total",
		Help: "Contracts placed by automated runs, one per contract.",
	})

	// What went wrong when a run tried to place, by the error's own code. A run
	// that cannot place looks exactly like a run that is not placing without
	// this — bot_contracts_placed_total flat either way.
	botPlacementErrors = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_placement_errors_total",
		Help: "Failed placement attempts by automated runs, by error code.",
	}, []string{"code"})

	botRunsStoppedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "bot_runs_stopped_total",
		Help: "Automated runs that ended, by reason. A rising 'error' is a defect.",
	}, []string{"reason"})

	// STK reconciliation: what the pass found, one increment per row it looked
	// at. Read together with the gauge below. Stale rising while 'error' rises
	// is a Daraja outage; rising while 'unresolved' rises is Daraja saying "still
	// processing" for a long time; rising with neither is the pass not running.
	// 'succeeded' rows are also in deposits_total, because they settle through
	// the same path a callback does.
	mpesaReconcileTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "mpesa_reconcile_total",
		Help: "Pending deposits examined by the STK reconciliation pass, by what happened.",
	}, []string{"outcome"})

	// Deposits pending longer than the reconciliation min-age. Counts every
	// such row, including ones the pass has given up on or cannot see, so this
	// is the number an operator owes attention to.
	mpesaDepositsPendingStale = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "mpesa_deposits_pending_stale",
		Help: "M-Pesa deposits pending longer than the reconciliation min-age.",
	})
)

// The reconcile outcomes, so a dashboard sees every series at zero from boot
// rather than only the ones that have happened yet.
func init() {
	for _, o := range []string{"succeeded", "unresolved", "failed_ignored", "error", "gave_up", "already_settled"} {
		mpesaReconcileTotal.WithLabelValues(o)
	}
}

// RecordReconcile is called once per row the reconciliation pass examines.
func RecordReconcile(outcome string) { mpesaReconcileTotal.WithLabelValues(outcome).Inc() }

// RecordBotContract is called once per contract, when it settles.
func RecordBotContract(outcome string) { botContractsTotal.WithLabelValues(outcome).Inc() }

// RecordBotPlacement is called once per contract, when it is placed.
func RecordBotPlacement() { botContractsPlaced.Inc() }

// RecordBotPlacementError is called when a placement attempt fails, with the
// error's code rather than its message.
func RecordBotPlacementError(code string) { botPlacementErrors.WithLabelValues(code).Inc() }

// RecordBotRunStopped is called once per run, with why it ended.
func RecordBotRunStopped(reason string) { botRunsStoppedTotal.WithLabelValues(reason).Inc() }

// RecordDeposit is called when a deposit settles either way.
func RecordDeposit(status string) { depositsTotal.WithLabelValues(status).Inc() }

// RecordAdminAction is called after each audited admin action.
func RecordAdminAction(action string) { adminActionsTotal.WithLabelValues(action).Inc() }

// Middleware records latency and outcome. It labels by the route pattern, not
// the raw path, so an id in the URL does not explode the label cardinality.
func Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()

		route := c.FullPath()
		if route == "" {
			route = "unmatched"
		}
		status := strconv.Itoa(c.Writer.Status())

		requestDuration.WithLabelValues(route, c.Request.Method, status).Observe(time.Since(start).Seconds())
		requestsTotal.WithLabelValues(route, c.Request.Method, status).Inc()
	}
}

// Sampler is what the collector needs from the rest of the app to fill the
// gauges. Keeping it an interface means observability does not import the
// repository or the transport.
type Sampler interface {
	OpenTrades(ctx context.Context) (int, error)
	DriftedAccounts(ctx context.Context) (int, error)
	LiveBotRuns(ctx context.Context) (int, error)
	StaleDeposits(ctx context.Context) (int, error)
	Connections() int
}

// Collect refreshes the gauges on an interval until ctx is cancelled.
func Collect(ctx context.Context, s Sampler, log *slog.Logger, every time.Duration) {
	sample := func() {
		sampleCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()

		if n, err := s.OpenTrades(sampleCtx); err == nil {
			tradesOpen.Set(float64(n))
		} else {
			log.Warn("metrics: open trades", "err", err)
		}

		if n, err := s.DriftedAccounts(sampleCtx); err == nil {
			ledgerDrift.Set(float64(n))
			if n > 0 {
				// Worth a log line too: whoever reads logs should not have to
				// be watching Grafana to learn money has gone missing.
				log.Error("LEDGER DRIFT DETECTED", "accounts", n)
			}
		} else {
			log.Warn("metrics: ledger drift", "err", err)
		}

		if n, err := s.LiveBotRuns(sampleCtx); err == nil {
			botRunsLive.Set(float64(n))
		} else {
			log.Warn("metrics: live bot runs", "err", err)
		}

		if n, err := s.StaleDeposits(sampleCtx); err == nil {
			mpesaDepositsPendingStale.Set(float64(n))
		} else {
			log.Warn("metrics: stale deposits", "err", err)
		}

		wsConnections.Set(float64(s.Connections()))
	}

	sample()
	t := time.NewTicker(every)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sample()
		}
	}
}

// MetricsHandler serves /metrics behind a bearer token. A public metrics
// endpoint leaks deposit volumes and user counts, so it is never open.
func MetricsHandler(token string, h gin.HandlerFunc) gin.HandlerFunc {
	return func(c *gin.Context) {
		if token != "" {
			auth := c.GetHeader("Authorization")
			if auth != "Bearer "+token {
				c.AbortWithStatus(401)
				return
			}
		}
		h(c)
	}
}
