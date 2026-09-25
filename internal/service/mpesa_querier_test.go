package service

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/KotlinDevIanBore/tradederivplus/internal/config"
	"github.com/KotlinDevIanBore/tradederivplus/pkg/mpesaclient"
)

// The seam the reconciliation pass will ask through. What matters is which side
// of it each mode gets: mock mode a stub that can never settle anything, real
// mode the client itself with nothing in between.

func TestMockModeQuerierNeverResolves(t *testing.T) {
	s := NewMpesaService(nil, &config.Config{UseMockMpesa: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if s.client != nil {
		t.Fatal("mock mode built a Daraja client")
	}
	for _, id := range []string{"ws_CO_MOCK_1", "ws_CO_MOCK_2", ""} {
		out, err := s.querier.STKQuery(context.Background(), id)
		if err != nil {
			t.Fatalf("the stub errored for %q: %v — an error is 'Daraja said nothing', not what mock mode should say", id, err)
		}
		if out.Status != mpesaclient.QueryUnresolved {
			t.Fatalf("mock querier said %s for %q, want unresolved — the stub must not be able to settle a deposit", out.Status, id)
		}
	}
}

func TestRealModeQuerierIsTheClient(t *testing.T) {
	s := NewMpesaService(nil, &config.Config{
		UseMockMpesa:        false,
		MpesaBaseURL:        "https://sandbox.invalid",
		MpesaConsumerKey:    "k",
		MpesaConsumerSecret: "s",
		MpesaShortcode:      "174379",
		MpesaPasskey:        "p",
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	if s.client == nil {
		t.Fatal("real mode built no Daraja client")
	}
	got, ok := s.querier.(*mpesaclient.Client)
	if !ok || got != s.client {
		t.Fatalf("real mode's querier is %T, want the client itself with no wrapper", s.querier)
	}
}

type scriptedQuerier struct{ out mpesaclient.QueryOutcome }

func (q scriptedQuerier) STKQuery(context.Context, string) (mpesaclient.QueryOutcome, error) {
	return q.out, nil
}

func TestSetQuerierForTestReplacesTheQuerier(t *testing.T) {
	s := NewMpesaService(nil, &config.Config{UseMockMpesa: true}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.SetQuerierForTest(scriptedQuerier{mpesaclient.QueryOutcome{Status: mpesaclient.QuerySucceeded}})

	out, _ := s.querier.STKQuery(context.Background(), "x")
	if out.Status != mpesaclient.QuerySucceeded {
		t.Fatalf("status %s after SetQuerierForTest, want the scripted succeeded", out.Status)
	}
}

// The ladder: widen, then hold. The numbers are provisional; the shape — the
// first wait is the min-age's neighbour, the last repeats — is what the pass
// relies on.
func TestReconcileBackoffLadder(t *testing.T) {
	want := []time.Duration{3 * time.Minute, 10 * time.Minute, 30 * time.Minute, time.Hour, time.Hour, time.Hour}
	for i, w := range want {
		if got := backoffAfter(i + 1); got != w {
			t.Fatalf("after %d attempts backoff is %s, want %s", i+1, got, w)
		}
	}
	if got := backoffAfter(0); got != 3*time.Minute {
		t.Fatalf("after 0 attempts (never valid, but never a panic) backoff is %s, want the first rung", got)
	}
}
