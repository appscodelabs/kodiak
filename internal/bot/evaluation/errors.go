// Sentinel errors that mergeable() may raise to communicate retry intent
// back to the per-PR evaluator loop. Ported from bot/kodiak/errors.py.

package evaluation

import "errors"

// ErrRetryForSkippableChecks asks the caller to wait and try the evaluation
// again because some "skippable" status checks are still pending.
var ErrRetryForSkippableChecks = errors.New("retry: skippable checks pending")

// ErrPollForever asks the caller to keep polling indefinitely (the bot is
// waiting for an external state change that has no natural timeout).
var ErrPollForever = errors.New("poll: waiting on external state")

// ErrAPICall is wrapped around a transient GitHub API failure so the
// evaluator loop can retry with backoff.
type ErrAPICall struct {
	Method string
	Err    error
}

func (e *ErrAPICall) Error() string {
	if e.Method != "" {
		return "api call (" + e.Method + "): " + e.Err.Error()
	}
	return "api call: " + e.Err.Error()
}

func (e *ErrAPICall) Unwrap() error { return e.Err }
