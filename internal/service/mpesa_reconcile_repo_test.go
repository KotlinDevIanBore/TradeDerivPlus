//go:build integration

package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
	"github.com/KotlinDevIanBore/tradederivplus/internal/repository"
)

// CP2 of the STK reconciliation plan: the schema and the repo the pass will
// work from. No pass exists yet; these pin what it will find.

func (h *harness) mpesaRow(checkoutID string) *models.MpesaRequest {
	h.t.Helper()
	m, err := h.store.Mpesa.GetByCheckoutID(h.ctx, checkoutID)
	if err != nil {
		h.t.Fatalf("read mpesa row %s: %v", checkoutID, err)
	}
	return m
}

// A deposit's first query is scheduled at insert, min-age out. Nil here would
// mean the pass never looks at the row.
func TestInsertSchedulesTheFirstQueryAfterMinAge(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	_, checkoutID := h.pendingDeposit(userID, accountID, 1000)

	m := h.mpesaRow(checkoutID)
	if m.ReconcileAttempts != 0 {
		t.Fatalf("a fresh row has %d attempts, want 0", m.ReconcileAttempts)
	}
	if m.NextQueryAt == nil {
		t.Fatal("a fresh row has no next_query_at — the pass would never look at it")
	}
	gap := m.NextQueryAt.Sub(m.CreatedAt)
	want := h.cfg.MpesaReconcileMinAge
	if gap < want-2*time.Second || gap > want+2*time.Second {
		t.Fatalf("first query scheduled %s after creation, want the %s min-age", gap, want)
	}
}

// ListPending is the pass's whole view of the world: pending, due, oldest due
// first, bounded. Everything else is invisible to it.
func TestListPendingReturnsOnlyDueRowsOldestFirst(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)
	now := time.Now()

	// Inserted in the wrong order on purpose: B before A, so the ordering the
	// test sees is next_query_at's and not created_at's.
	_, dueLater := h.pendingDeposit(userID, accountID, 1000) // B: due 5m ago
	_, dueFirst := h.pendingDeposit(userID, accountID, 1000) // A: due 10m ago
	_, notYet := h.pendingDeposit(userID, accountID, 1000)   // C: due in 3m (insert default)
	_, settled := h.pendingDeposit(userID, accountID, 1000)  // D: due, but settled since
	_, unscheduled := h.pendingDeposit(userID, accountID, 1000)

	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(h.store.Mpesa.MarkQueried(h.ctx, dueLater, 1, now.Add(-5*time.Minute)))
	must(h.store.Mpesa.MarkQueried(h.ctx, dueFirst, 2, now.Add(-10*time.Minute)))
	must(h.store.Mpesa.MarkQueried(h.ctx, settled, 1, now.Add(-7*time.Minute)))
	must(h.mpesa.HandleCallback(h.ctx, darajaCallback(settled, 0, "processed", "QK00000010")))
	// A pending row that was never scheduled — the state a row is in if the
	// service ever inserts one without a next_query_at.
	_, err := h.pool.Exec(h.ctx, `UPDATE mpesa_requests SET next_query_at = NULL WHERE checkout_request_id = $1`, unscheduled)
	must(err)

	got, err := h.store.Mpesa.ListPending(h.ctx, now, 100)
	must(err)
	ids := make([]string, 0, len(got))
	for _, m := range got {
		ids = append(ids, m.CheckoutRequestID)
	}
	if len(ids) != 2 || ids[0] != dueFirst || ids[1] != dueLater {
		t.Fatalf("ListPending returned %v, want [%s %s]: due rows only, oldest due first — not %s (not due), %s (settled) or %s (never scheduled)",
			ids, dueFirst, dueLater, notYet, settled, unscheduled)
	}
	if got[0].ReconcileAttempts != 2 {
		t.Fatalf("listed row carries %d attempts, want the 2 MarkQueried wrote — the pass needs it to pick the next backoff", got[0].ReconcileAttempts)
	}

	// Bounded: one pass cannot pick up the whole table.
	one, err := h.store.Mpesa.ListPending(h.ctx, now, 1)
	must(err)
	if len(one) != 1 || one[0].CheckoutRequestID != dueFirst {
		t.Fatalf("limit 1 returned %d rows, want just %s", len(one), dueFirst)
	}

	// Time moves on and C comes due behind the others.
	later, err := h.store.Mpesa.ListPending(h.ctx, now.Add(10*time.Minute), 100)
	must(err)
	if len(later) != 3 || later[2].CheckoutRequestID != notYet {
		t.Fatalf("10m later ListPending returned %d rows with %v last, want 3 with %s last", len(later), last(later), notYet)
	}
}

func last(ms []models.MpesaRequest) string {
	if len(ms) == 0 {
		return ""
	}
	return ms[len(ms)-1].CheckoutRequestID
}

// MarkQueried moves a pending row's schedule and nothing else's. A callback can
// settle the row between the pass reading it and writing to it; the write must
// then touch nothing, because the row is no longer the reconciler's.
func TestMarkQueriedIsANoopOnASettledRow(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	// The positive case first: on a pending row both fields move.
	_, pending := h.pendingDeposit(userID, accountID, 1000)
	next := time.Now().Add(10 * time.Minute).Truncate(time.Microsecond)
	if err := h.store.Mpesa.MarkQueried(h.ctx, pending, 3, next); err != nil {
		t.Fatal(err)
	}
	m := h.mpesaRow(pending)
	if m.ReconcileAttempts != 3 || m.NextQueryAt == nil || !m.NextQueryAt.Equal(next) {
		t.Fatalf("pending row after MarkQueried: attempts %d next %v, want 3 and %v", m.ReconcileAttempts, m.NextQueryAt, next)
	}

	// The guard: settled, then marked. Nothing moves, no error.
	_, done := h.pendingDeposit(userID, accountID, 1000)
	before := h.mpesaRow(done)
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(done, 0, "processed", "QK00000011")); err != nil {
		t.Fatal(err)
	}
	if err := h.store.Mpesa.MarkQueried(h.ctx, done, 7, time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("MarkQueried on a settled row errored: %v — it should be a silent no-op, the row got what it was waiting for", err)
	}
	after := h.mpesaRow(done)
	if after.Status != models.StatusCompleted {
		t.Fatalf("settled row is %q after MarkQueried, want completed", after.Status)
	}
	if after.ReconcileAttempts != before.ReconcileAttempts || !after.NextQueryAt.Equal(*before.NextQueryAt) {
		t.Fatalf("MarkQueried moved a settled row: attempts %d→%d next %v→%v",
			before.ReconcileAttempts, after.ReconcileAttempts, before.NextQueryAt, after.NextQueryAt)
	}
	h.assertReconciled(accountID)
}

// The stale gauge keys on created_at, so a row the pass cannot see — pending
// with no schedule — still shows up in it. That is the net under the pass.
func TestCountPendingOlderThanSeesEveryOldPendingRow(t *testing.T) {
	h := newHarness(t)
	userID, accountID := h.newUser(decimal.Zero)

	_, fresh := h.pendingDeposit(userID, accountID, 1000)
	_, old := h.pendingDeposit(userID, accountID, 1000)
	_, oldUnscheduled := h.pendingDeposit(userID, accountID, 1000)
	_, oldSettled := h.pendingDeposit(userID, accountID, 1000)

	backdate := func(id string) {
		if _, err := h.pool.Exec(h.ctx,
			`UPDATE mpesa_requests SET created_at = now() - interval '10 minutes' WHERE checkout_request_id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	backdate(old)
	backdate(oldUnscheduled)
	backdate(oldSettled)
	if _, err := h.pool.Exec(h.ctx, `UPDATE mpesa_requests SET next_query_at = NULL WHERE checkout_request_id = $1`, oldUnscheduled); err != nil {
		t.Fatal(err)
	}
	if err := h.mpesa.HandleCallback(h.ctx, darajaCallback(oldSettled, 1032, "cancelled", "")); err != nil {
		t.Fatal(err)
	}

	n, err := h.store.Mpesa.CountPendingOlderThan(h.ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d pending deposits older than 5m, want 2 (%s and the unscheduled %s; not the fresh %s, not the settled %s)",
			n, old, oldUnscheduled, fresh, oldSettled)
	}
	n, err = h.store.Mpesa.CountPendingOlderThan(h.ctx, 30*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d pending deposits older than 30m, want 0", n)
	}
}

// Migration 0007 against a database that already has rows: pending rows become
// eligible on the first pass, terminal rows are left alone, and the pass's
// index exists. Applies 0001–0006, seeds, then applies the real 0007 file.
func TestMigration0007BackfillsPendingRows(t *testing.T) {
	pool := freshPool(t)
	ctx := context.Background()
	root := repoRoot(t)

	// Everything before 0007, from the real files.
	before := t.TempDir()
	entries, err := os.ReadDir(filepath.Join(root, "migrations"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() >= "0007" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, "migrations", e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(before, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.Migrate(ctx, pool, before); err != nil {
		t.Fatalf("migrate to 0006: %v", err)
	}
	var hasColumn bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
		WHERE table_name = 'mpesa_requests' AND column_name = 'next_query_at')`).Scan(&hasColumn); err != nil {
		t.Fatal(err)
	}
	if hasColumn {
		t.Fatal("next_query_at exists before 0007 — the test is not testing the backfill")
	}

	// Rows as a pre-0007 deployment would have them. transaction_id is a
	// nullable FK, so no ledger is needed.
	seed := func(status string) string {
		id := "ws_CO_" + uuid.NewString()[:8]
		if _, err := pool.Exec(ctx, `INSERT INTO mpesa_requests (checkout_request_id, merchant_request_id, phone_number, amount, status)
			VALUES ($1, $2, '254712345678', 100, $3)`, id, "M-"+id, status); err != nil {
			t.Fatal(err)
		}
		return id
	}
	pendingA, pendingB := seed(models.StatusPending), seed(models.StatusPending)
	completed, failed := seed(models.StatusCompleted), seed(models.StatusFailed)

	// The real directory: 0001–0006 are recorded as applied, so only 0007 runs.
	if err := repository.Migrate(ctx, pool, filepath.Join(root, "migrations")); err != nil {
		t.Fatalf("apply 0007: %v", err)
	}

	store := repository.NewStore(pool)
	for _, id := range []string{pendingA, pendingB} {
		m, err := store.Mpesa.GetByCheckoutID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if m.NextQueryAt == nil || !m.NextQueryAt.Equal(m.CreatedAt) {
			t.Fatalf("pending row %s has next_query_at %v after backfill, want created_at %v", id, m.NextQueryAt, m.CreatedAt)
		}
		if m.ReconcileAttempts != 0 {
			t.Fatalf("pending row %s has %d attempts after backfill, want 0", id, m.ReconcileAttempts)
		}
	}
	for _, id := range []string{completed, failed} {
		m, err := store.Mpesa.GetByCheckoutID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if m.NextQueryAt != nil {
			t.Fatalf("terminal row %s (%s) was scheduled at %v — the backfill touched a row nothing should look at", id, m.Status, m.NextQueryAt)
		}
	}

	// And the pass would pick up exactly the two pending rows, first pass.
	due, err := store.Mpesa.ListPending(ctx, time.Now(), 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 2 {
		t.Fatalf("first pass after backfill sees %d rows, want the 2 pending", len(due))
	}

	var hasIndex bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_indexes
		WHERE tablename = 'mpesa_requests' AND indexname = 'idx_mpesa_requests_pending_next')`).Scan(&hasIndex); err != nil {
		t.Fatal(err)
	}
	if !hasIndex {
		t.Fatal("idx_mpesa_requests_pending_next was not created")
	}

	// Re-running is a no-op: Migrate records 0007 as applied.
	if err := repository.Migrate(ctx, pool, filepath.Join(root, "migrations")); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
}
