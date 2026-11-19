package background

import (
	"errors"
	"time"
)

// State is the lifecycle state of a job.
//
//	queued ──▶ running ──▶ succeeded
//	  ▲           │──────▶ failed
//	  └─ retry ───┤──────▶ cancelled    (Queue.Cancel)
//	              └──────▶ interrupted  (process shutdown)
//
// A queued job can also go straight to cancelled or interrupted.
type State string

// Job states.
const (
	// StateQueued: accepted, waiting for a worker or for a retry delay.
	StateQueued State = "queued"
	// StateRunning: a handler is executing the job.
	StateRunning State = "running"
	// StateSucceeded: the handler returned nil.
	StateSucceeded State = "succeeded"
	// StateFailed: the handler returned an error that is not retried (not
	// retryable, attempts exhausted, timeout, panic), or the job could not
	// be resumed after a restart.
	StateFailed State = "failed"
	// StateCancelled: [Queue.Cancel] was called and the job stopped.
	StateCancelled State = "cancelled"
	// StateInterrupted: the process shut down before the job finished. With
	// the in-memory store this is the last anybody hears of the job. With a
	// durable store the job is failed or queued again after the restart,
	// depending on [HandlerOptions.OnRestart].
	StateInterrupted State = "interrupted"
)

// Done reports whether the job has stopped: it is succeeded, failed,
// cancelled or interrupted. A watcher receives no further update after a
// Done state. (An interrupted job in a durable store can be queued again by
// the next process; watch it again to follow that.)
func (s State) Done() bool {
	switch s {
	case StateSucceeded, StateFailed, StateCancelled, StateInterrupted:
		return true
	}
	return false
}

// Progress is what a handler reports through [Run.SetProgress].
type Progress struct {
	// Done and Total are in units chosen by the handler (items, bytes).
	// Total is 0 when unknown.
	Done  int64 `json:"done"`
	Total int64 `json:"total"`
	// Message is a short human-readable status.
	Message string `json:"message,omitempty"`
}

// Job is a snapshot of a job. Payload, Labels and Result are shared with the
// queue: treat them as read-only.
type Job struct {
	// ID is assigned by the queue (a UUID).
	ID string `json:"id"`
	// Kind selects the handler.
	Kind string `json:"kind"`
	// Key is the caller's idempotency key; empty when none was given. At most
	// one job exists per (Kind, Key) while the job is retained.
	Key string `json:"key,omitempty"`
	// Payload is the handler's input, opaque to the queue.
	Payload []byte `json:"payload,omitempty"`
	// Labels are caller metadata, for example the owning user, so that a
	// service can authorize Get/Watch/Cancel.
	Labels map[string]string `json:"labels,omitempty"`

	State State `json:"state"`
	// Attempt counts handler invocations, starting at 1. It starts again
	// when the job is queued again after a restart (see Restarts), so every
	// restart has the handler's full MaxAttempts.
	Attempt int `json:"attempt"`
	// Restarts counts how often the job was queued again after its process
	// stopped (durable stores only).
	Restarts int      `json:"restarts,omitempty"`
	Progress Progress `json:"progress"`
	// Result is what the handler returned on success.
	Result []byte `json:"result,omitempty"`
	// Error is the last error: the reason for failed/cancelled/interrupted,
	// or, while queued for a retry, the error of the previous attempt.
	Error string `json:"error,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	// StartedAt is the start of the latest attempt.
	StartedAt time.Time `json:"started_at,omitzero"`
	// NextAttemptAt is set while the job waits for a retry.
	NextAttemptAt time.Time `json:"next_attempt_at,omitzero"`
	// UpdatedAt changes with every state, progress or attempt change.
	UpdatedAt time.Time `json:"updated_at"`
	// FinishedAt is set when State is Done.
	FinishedAt time.Time `json:"finished_at,omitzero"`

	// Owner identifies the queue instance (process) that runs the job;
	// HeartbeatAt is when it last confirmed that it is alive. Durable stores
	// use them to find jobs whose process died. See [Store].
	Owner       string    `json:"owner,omitempty"`
	HeartbeatAt time.Time `json:"heartbeat_at,omitzero"`
	// Revision increases with every write; stores use it for optimistic
	// concurrency.
	Revision int64 `json:"revision"`
}

// sameView reports whether two snapshots look the same to a watcher
// (heartbeats alone are not worth an update).
func sameView(a, b Job) bool {
	return a.State == b.State && a.Attempt == b.Attempt && a.Progress == b.Progress &&
		a.UpdatedAt.Equal(b.UpdatedAt) && a.Error == b.Error
}

type retryableError struct{ err error }

func (e *retryableError) Error() string { return e.err.Error() }
func (e *retryableError) Unwrap() error { return e.err }

// Retryable marks err as transient: the queue runs the job again, after a
// backoff, while the handler's MaxAttempts allows. Unmarked errors fail the
// job at once. Retryable(nil) is nil.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &retryableError{err: err}
}

// IsRetryable reports whether err was marked with [Retryable].
func IsRetryable(err error) bool {
	_, ok := errors.AsType[*retryableError](err)
	return ok
}
