// Package kerrors contains error types shared across the kodiak bot,
// ported from bot/kodiak/errors.py.
package kerrors

import (
	"errors"
	"fmt"
)

var (
	ErrRetryForSkippableChecks      = errors.New("retry for skippable checks")
	ErrPollForever                  = errors.New("poll forever")
	ErrGitHubAPIInternalServerError = errors.New("github api internal server error")
)

// APICallError corresponds to ApiCallException in the Python implementation.
type APICallError struct {
	Method     string
	StatusCode int
	Response   []byte
}

func (e *APICallError) Error() string {
	return fmt.Sprintf("api call %q failed with HTTP status %d", e.Method, e.StatusCode)
}
