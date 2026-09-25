package service

import (
	"context"
	"errors"
	"testing"

	"github.com/KotlinDevIanBore/tradederivplus/internal/models"
)

// TestFailureReasonCallsAShutdownAShutdown pins the classification that
// TestBotShutdownFinalisesRun could only assert racily.
//
// Three sites in the run loop consult externalStop and then run a database
// operation. That is a check-then-act pair: a cancel landing in the window
// between them makes the operation fail with "context canceled", and the run was
// recorded as having died of an error. stop_reason is operator-visible, so a
// clean restart read as a fault. Under CI's timing the cancel landed in that
// window often enough to fail the suite on unrelated branches.
func TestFailureReasonCallsAShutdownAShutdown(t *testing.T) {
	reason, lastError := failureReason(true, context.Canceled)
	if reason != models.BotStopShutdown {
		t.Fatalf("reason %q while shutting down, want %q", reason, models.BotStopShutdown)
	}
	// No last_error either: there is no fault to report, and "context canceled"
	// sitting in that column is the noise that reads like a bug.
	if lastError != nil {
		t.Fatalf("last_error %q while shutting down, want none", *lastError)
	}
}

// The converse, which is what stops the fix from being "call everything a
// shutdown": a genuine failure while the process is healthy is still a fault,
// and still carries its message.
func TestFailureReasonKeepsARealFailure(t *testing.T) {
	boom := errors.New("connection refused")
	reason, lastError := failureReason(false, boom)
	if reason != models.BotStopError {
		t.Fatalf("reason %q for a real failure, want %q", reason, models.BotStopError)
	}
	if lastError == nil {
		t.Fatal("a real failure recorded no last_error")
	}
	if *lastError != boom.Error() {
		t.Fatalf("last_error %q, want %q", *lastError, boom.Error())
	}
}

// A cancellation error while the process is healthy is still a fault. The
// decision is "are we shutting down", not "does this error mention
// cancellation" — a per-operation timeout is a real problem and must not be
// disguised as an orderly stop.
func TestFailureReasonDoesNotTrustTheErrorAlone(t *testing.T) {
	reason, lastError := failureReason(false, context.Canceled)
	if reason != models.BotStopError {
		t.Fatalf("reason %q for a cancellation with a live root context, want %q",
			reason, models.BotStopError)
	}
	if lastError == nil || *lastError != context.Canceled.Error() {
		t.Fatal("the cancellation's message was not recorded")
	}
}
