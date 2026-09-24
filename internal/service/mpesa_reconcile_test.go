//go:build integration

package service_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/service"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// CP3 of the STK reconciliation plan: the pass. Every test here drives
// MpesaService.Reconcile against a scripted Daraja and checks two things the
// plan is built on — that only a plain "the customer paid" moves money, and
// that it moves it exactly once however many ways the answer arrives.

// stubQuerier is the Daraja the pass asks. It answers per checkout id, counts
// how often it was asked, and can run something in the middle of answering —
// which is how a test lands a callback inside the query's window on purpose.
type stubQuerier struct {
	mu       sync.Mutex
	outcomes map[string]mpesaclient.QueryOutcome
	errs     map[string]error
	calls    map[string]int
	during   func(checkoutID string)
}

func newStubQuerier() *stubQuerier {
	return &stubQuerier{
		outcomes: map[string]mpesaclient.QueryOutcome{},
		errs:     map[string]error{},
		calls:    map[string]int{},
	}
}

func (q *stubQuerier) STKQuery(_ context.Context, id string) (mpesaclient.QueryOutcome, error) {
	q.mu.Lock()
	q.calls[id]++
	out, ok := q.outcomes[id]
	err := q.errs[id]
	during := q.during
	q.mu.Unlock()
	if during != nil {
		during(id)
	}
	if err != nil {
		return mpesaclient.QueryOutcome{}, err
	}
	if !ok {
		out = mpesaclient.QueryOutcome{Status: mpesaclient.QueryUnresolved, ResultDesc: "stub: processing"}
	}
	return out, nil
}

func (q *stubQuerier) say(id string, status mpesaclient.QueryStatus, code int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.outcomes[id] = mpesaclient.QueryOutcome{Status: status, ResultCode: code, ResultDesc: "stub"}
}

func (q *stubQuerier) fail(id string, err error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.errs[id] = err
}

func (q *stubQuerier) count(id string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.calls[id]
}

// outcomes captures what the pass reports through OnReconcile.
type outcomes struct {
	mu   sync.Mutex
	seen []string
}

func (o *outcomes) record(s string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seen = append(o.seen, s)
}

func (o *outcomes) countOf(s string) int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, x := range o.seen {
		if x == s {
			n++
		}
	}
	return n
}

// reconciler wires a stub and an outcome capture into the harness's service.
func (h *harness) reconciler() (*stubQuerier, *outcomes) {
	h.t.Helper()
	q, o := newStubQuerier(), &outcomes{}
	h.mpesa.SetQuerierForTest(q)
	h.mpesa.OnReconcile(o.record)
	return q, o
}

// dueDeposit starts a deposit whose first query is due immediately: the
// min-age is zero for this one insert. Load's 60s floor does not apply in the
// harness, which is what makes this possible.
func (h *harness) dueDeposit(userID, accountID uuid.UUID, kes int64) (uuid.UUID, string) {
	h.t.Helper()
	was := h.cfg.MpesaReconcileMinAge
	h.cfg.MpesaReconcileMinAge = 0
	defer func() { h.cfg.MpesaReconcileMinAge = was }()
	return h.pendingDeposit(userID, accountID, kes)
}

func (h *harness) tx(id uuid.UUID) *models.Transaction {
	h.t.Helper()
	tx, err := h.store.Transactions.GetByID(h.ctx, id)
	if err != nil {
		h.t.Fatalf("read transaction: %v", err)
	}
	return tx
}

func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// The happy path: Daraja says paid, the pass credits, once, and the row says
// how it was settled.
func TestQuerySettledDepositCreditsOnce(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.dueDeposit(userID, accountID, 12950)
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0)

	h.mpesa.Reconcile(h.ctx)

	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after a succeeded query, want 100", w.RealBalance)
	}
	tx := h.tx(txID)
	if tx.Status != models.StatusCompleted {
		t.Fatalf("transaction is %q, want completed", tx.Status)
	}
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByQuery {
		t.Fatalf("settled_by %q, want %q", by, service.SettledByQuery)
	}
	if _, has := tx.Metadata["receipt"]; has {
		t.Fatalf("a query-settled deposit carries a receipt: %v — nothing should be synthesised", tx.Metadata["receipt"])
	}
	req := h.mpesaRow(checkoutID)
	if req.Status != models.StatusCompleted || req.ResultCode == nil || *req.ResultCode != 0 {
		t.Fatalf("mpesa row is %q with code %v, want completed / 0", req.Status, req.ResultCode)
	}
	if n := o.countOf(service.ReconcileSucceeded); n != 1 {
		t.Fatalf("%d 'succeeded' outcomes, want 1: %v", n, o.seen)
	}

	// A second pass has nothing to ask: the row is no longer pending.
	h.mpesa.Reconcile(h.ctx)
	if n := q.count(checkoutID); n != 1 {
		t.Fatalf("Daraja was asked %d times about a settled deposit, want 1", n)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after a second pass, want still 100", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// The race the whole design exists for: the pass and a late callback both say
// paid, at once. One credit between them — not zero, not two. The shape of
// TestConcurrentCallbacksCreditExactlyOnce, with half the callers being passes.
func TestQueryAndLateCallbackCreditExactlyOnce(t *testing.T) {
	h := newHarness(t)
	q, _ := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)

	const callers = 8
	for attempt := 0; attempt < 5; attempt++ {
		txID, checkoutID := h.dueDeposit(userID, accountID, 12950)
		q.say(checkoutID, mpesaclient.QuerySucceeded, 0)

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, callers)
		for i := 0; i < callers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if i%2 == 0 {
					h.mpesa.Reconcile(context.Background())
					return
				}
				if err := h.mpesa.HandleCallback(context.Background(),
					darajaCallback(checkoutID, 0, "processed", "QK12345678")); err != nil {
					errs <- err
				}
			}(i)
		}
		close(start)
		wg.Wait()
		close(errs)
		for err := range errs {
			t.Fatalf("callback on deposit %d: %v", attempt, err)
		}

		want := decimal.NewFromInt(int64(100 * (attempt + 1)))
		if w := h.wallet_(accountID); !w.RealBalance.Equal(want) {
			t.Fatalf("after %d concurrent passes and callbacks on deposit %d the balance is %s, want %s",
				callers, attempt+1, w.RealBalance, want)
		}
		if tx := h.tx(txID); tx.Status != models.StatusCompleted {
			t.Fatalf("transaction is %q after the race", tx.Status)
		}
		h.assertReconciled(accountID)
	}
}

// The callback lands while the query is in flight. The pass listed the row as
// pending, Daraja says paid, and settle must lose the guard rather than credit
// a second time. Deterministic: the stub delivers the callback itself, from
// inside the query.
func TestCallbackThenQueryIsANoop(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.dueDeposit(userID, accountID, 12950)
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0)
	q.during = func(id string) {
		if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(id, 0, "processed", "QK00000020")); err != nil {
			t.Errorf("callback during query: %v", err)
		}
	}

	h.mpesa.Reconcile(h.ctx)

	if n := q.count(checkoutID); n != 1 {
		t.Fatalf("queried %d times, want 1", n)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s, want 100 — the callback credited and the query must not have", w.RealBalance)
	}
	tx := h.tx(txID)
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByCallback {
		t.Fatalf("settled_by %q, want callback — the query's write overtook it", by)
	}
	if receipt, _ := tx.Metadata["receipt"].(string); receipt != "QK00000020" {
		t.Fatalf("receipt %q, want the callback's — the query's receipt-less write overtook it", receipt)
	}
	if n := o.countOf(service.ReconcileAlreadySettled); n != 1 {
		t.Fatalf("outcomes %v, want one already_settled", o.seen)
	}
	h.assertReconciled(accountID)
}

// The same window with an unresolved answer: the callback settles during the
// query, and the pass's MarkQueried afterwards must touch nothing. This is
// MarkQueried's guard seen from the pass.
func TestCallbackDuringAnUnresolvedQueryLeavesTheRowAlone(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	_, checkoutID := h.dueDeposit(userID, accountID, 12950)
	before := h.mpesaRow(checkoutID)
	q.during = func(id string) {
		if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(id, 0, "processed", "QK00000021")); err != nil {
			t.Errorf("callback during query: %v", err)
		}
	}

	h.mpesa.Reconcile(h.ctx)

	after := h.mpesaRow(checkoutID)
	if after.Status != models.StatusCompleted {
		t.Fatalf("row is %q, want completed", after.Status)
	}
	if after.ReconcileAttempts != 0 || !after.NextQueryAt.Equal(*before.NextQueryAt) {
		t.Fatalf("the pass wrote to a row the callback had settled: attempts %d next %v (was %v)",
			after.ReconcileAttempts, after.NextQueryAt, before.NextQueryAt)
	}
	if n := o.countOf(service.ReconcileUnresolved); n != 1 {
		t.Fatalf("outcomes %v, want one unresolved — the pass did ask, and Daraja did answer", o.seen)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s, want 100", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// "Still processing" moves the schedule and nothing else, up the ladder; and a
// row inside its backoff is not asked about again.
func TestUnresolvedAdvancesBackoffAndNothingElse(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.dueDeposit(userID, accountID, 12950)

	ladder := []time.Duration{3 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, time.Hour}
	for i, want := range ladder {
		passAt := time.Now()
		h.mpesa.Reconcile(h.ctx)

		row := h.mpesaRow(checkoutID)
		if row.ReconcileAttempts != i+1 {
			t.Fatalf("pass %d: attempts %d, want %d", i+1, row.ReconcileAttempts, i+1)
		}
		if gap := row.NextQueryAt.Sub(passAt); gap < want-2*time.Second || gap > want+2*time.Second {
			t.Fatalf("pass %d: next query %s out, want %s", i+1, gap.Round(time.Second), want)
		}
		if row.Status != models.StatusPending {
			t.Fatalf("pass %d: row is %q", i+1, row.Status)
		}

		// Inside the backoff: a pass right now asks nothing.
		h.mpesa.Reconcile(h.ctx)
		if n := q.count(checkoutID); n != i+1 {
			t.Fatalf("pass %d: Daraja asked %d times, want %d — a pass inside the backoff queried", i+1, n, i+1)
		}

		// Time passes: bring the row due again, keeping its attempt count.
		if err := h.store.Mpesa.MarkQueried(h.ctx, checkoutID, row.ReconcileAttempts, time.Now().Add(-time.Second)); err != nil {
			t.Fatal(err)
		}
	}

	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("an unresolved deposit credited %s", w.Total())
	}
	if tx := h.tx(txID); tx.Status != models.StatusPending {
		t.Fatalf("transaction is %q after five unresolved queries, want pending", tx.Status)
	}
	if n := o.countOf(service.ReconcileUnresolved); n != len(ladder) {
		t.Fatalf("%d unresolved outcomes, want %d: %v", n, len(ladder), o.seen)
	}
	h.assertReconciled(accountID)
}

// Daraja said nothing. That is not an attempt: the schedule is untouched, the
// row is still due, and the next pass asks again — a Daraja outage must not
// walk rows toward the horizon.
func TestQuerierErrorIsNotAnAttempt(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	_, checkoutID := h.dueDeposit(userID, accountID, 12950)
	q.fail(checkoutID, errors.New("stk query: connection refused"))
	before := h.mpesaRow(checkoutID)

	h.mpesa.Reconcile(h.ctx)
	h.mpesa.Reconcile(h.ctx)

	if n := q.count(checkoutID); n != 2 {
		t.Fatalf("Daraja asked %d times across two passes, want 2 — an error must leave the row due", n)
	}
	after := h.mpesaRow(checkoutID)
	if after.ReconcileAttempts != 0 || !after.NextQueryAt.Equal(*before.NextQueryAt) {
		t.Fatalf("an error counted as an attempt: attempts %d next %v (was %v)",
			after.ReconcileAttempts, after.NextQueryAt, before.NextQueryAt)
	}
	if n := o.countOf(service.ReconcileError); n != 2 {
		t.Fatalf("outcomes %v, want two errors", o.seen)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("credited %s on an error", w.Total())
	}

	// Daraja comes back, and the same row settles on the next pass.
	q.fail(checkoutID, nil)
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0)
	h.mpesa.Reconcile(h.ctx)
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s once Daraja answered, want 100", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// Plan D2: a query never writes Failed. Daraja's "cancelled by user" advances
// the backoff like "still processing", the row stays pending, and a later
// callback saying paid still credits.
func TestQueryFailedIsNotWritten(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.dueDeposit(userID, accountID, 12950)
	q.say(checkoutID, mpesaclient.QueryFailed, 1032)

	h.mpesa.Reconcile(h.ctx)

	row := h.mpesaRow(checkoutID)
	if row.Status != models.StatusPending {
		t.Fatalf("mpesa row is %q after a failed query, want pending — a query wrote Failed", row.Status)
	}
	if row.ResultCode != nil {
		t.Fatalf("result_code %d written by a query", *row.ResultCode)
	}
	if row.ReconcileAttempts != 1 {
		t.Fatalf("attempts %d, want 1 — a failed answer is still an answer", row.ReconcileAttempts)
	}
	if tx := h.tx(txID); tx.Status != models.StatusPending {
		t.Fatalf("transaction is %q, want pending", tx.Status)
	}
	if n := o.countOf(service.ReconcileFailedIgnored); n != 1 {
		t.Fatalf("outcomes %v, want one failed_ignored", o.seen)
	}

	// The callback has the last word.
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(checkoutID, 0, "processed", "QK00000022")); err != nil {
		t.Fatal(err)
	}
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s after the callback, want 100 — the query's Failed blocked it", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// Past the horizon a row is not queried; it is taken off the schedule, once,
// and left for an operator. It stays pending, and the stale gauge still sees it.
func TestGiveUpRowIsNotQueried(t *testing.T) {
	h := newHarness(t)
	q, o := h.reconciler()
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.dueDeposit(userID, accountID, 12950)
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0) // would credit, if asked
	if _, err := h.pool.Exec(h.ctx,
		`UPDATE mpesa_requests SET created_at = now() - interval '25 hours' WHERE checkout_request_id = $1`, checkoutID); err != nil {
		t.Fatal(err)
	}

	h.mpesa.Reconcile(h.ctx)
	h.mpesa.Reconcile(h.ctx)

	if n := q.count(checkoutID); n != 0 {
		t.Fatalf("Daraja asked %d times about a row past the horizon, want 0", n)
	}
	row := h.mpesaRow(checkoutID)
	if row.Status != models.StatusPending || row.NextQueryAt != nil {
		t.Fatalf("given-up row: status %q next %v, want pending and no schedule", row.Status, row.NextQueryAt)
	}
	if n := o.countOf(service.ReconcileGaveUp); n != 1 {
		t.Fatalf("outcomes %v, want exactly one gave_up — the second pass must not re-count it", o.seen)
	}
	if tx := h.tx(txID); tx.Status != models.StatusPending {
		t.Fatalf("transaction is %q, want pending — an operator settles it, not the pass", tx.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("credited %s past the horizon", w.Total())
	}
	stale, err := h.store.Mpesa.CountPendingOlderThan(h.ctx, h.cfg.MpesaReconcileMinAge)
	if err != nil {
		t.Fatal(err)
	}
	if stale != 1 {
		t.Fatalf("stale gauge sees %d, want 1 — the given-up row must stay visible", stale)
	}
}

// A deposit the previous process left pending, settled by the boot pass of the
// next one — a fresh service over the same store, wired as main.go wires it.
func TestBootPassSettlesAStrandedDeposit(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	_, checkoutID := h.dueDeposit(userID, accountID, 12950)

	q := newStubQuerier()
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0)
	next := service.NewMpesaService(h.store, h.cfg, h.log)
	next.SetQuerierForTest(q)
	_ = service.NewWalletService(h.store, next, noopBroadcaster{}, h.cfg, h.log) // registers OnResult

	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	next.Start(ctx)

	waitFor(t, "the boot pass to credit", 5*time.Second, func() bool {
		return h.wallet_(accountID).RealBalance.Equal(decimal.NewFromInt(100))
	})
	if n := q.count(checkoutID); n != 1 {
		t.Fatalf("boot pass asked %d times, want 1", n)
	}
	h.assertReconciled(accountID)
}

// The boot-order property: a pass before the wallet service exists refuses to
// settle, and refuses BEFORE the guard is consumed — so the row is still
// there for the pass that can.
func TestPassWithoutAResultHandlerLeavesTheRowForOneThatHasOne(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	_, checkoutID := h.dueDeposit(userID, accountID, 12950)

	q := newStubQuerier()
	q.say(checkoutID, mpesaclient.QuerySucceeded, 0)
	o := &outcomes{}
	lone := service.NewMpesaService(h.store, h.cfg, h.log) // no NewWalletService
	lone.SetQuerierForTest(q)
	lone.OnReconcile(o.record)

	lone.Reconcile(h.ctx)

	if n := o.countOf(service.ReconcileError); n != 1 {
		t.Fatalf("outcomes %v, want one error", o.seen)
	}
	if row := h.mpesaRow(checkoutID); row.Status != models.StatusPending {
		t.Fatalf("row is %q after a handler-less pass, want pending — the guard was consumed with nowhere to send the result", row.Status)
	}
	if w := h.wallet_(accountID); !w.Total().IsZero() {
		t.Fatalf("credited %s with no handler", w.Total())
	}

	// The real service settles the same row.
	h.mpesa.SetQuerierForTest(q)
	h.mpesa.Reconcile(h.ctx)
	if w := h.wallet_(accountID); !w.RealBalance.Equal(decimal.NewFromInt(100)) {
		t.Fatalf("balance %s from the wired service, want 100", w.RealBalance)
	}
	h.assertReconciled(accountID)
}

// Mock mode, production min-age: the pass never reaches a deposit the mock
// callback is about to settle. The row is settled by the callback, and the
// pass has never touched it.
func TestPassNeverTouchesAMockDeposit(t *testing.T) {
	h := newHarness(t) // querier is mock mode's own stub; min-age is the 3m default
	userID, accountID := h.newUser(decimal.Zero)
	txID, checkoutID := h.pendingDeposit(userID, accountID, 12950)
	before := h.mpesaRow(checkoutID)

	h.mpesa.Reconcile(h.ctx)
	row := h.mpesaRow(checkoutID)
	if row.ReconcileAttempts != 0 || !row.NextQueryAt.Equal(*before.NextQueryAt) {
		t.Fatalf("the pass reached a %s-old mock deposit: attempts %d", time.Since(row.CreatedAt).Round(time.Second), row.ReconcileAttempts)
	}

	// simulateCallback lands in 4–9s, either way.
	waitFor(t, "the mock callback", 12*time.Second, func() bool {
		return h.tx(txID).Status != models.StatusPending
	})
	tx := h.tx(txID)
	if by, _ := tx.Metadata["settled_by"].(string); by != service.SettledByCallback {
		t.Fatalf("mock deposit settled_by %q, want callback", by)
	}
	if row := h.mpesaRow(checkoutID); row.ReconcileAttempts != 0 {
		t.Fatalf("attempts %d on a mock deposit, want 0", row.ReconcileAttempts)
	}
	h.assertReconciled(accountID)
}
