package app

import (
	"errors"
	"strings"

	"github.com/hay-kot/svx/internal/domain"
)

// Exit codes from the spec: 1 for a failed or timed-out service, 2 for a
// config or usage error.
const (
	ExitFailure = 1
	ExitUsage   = 2
)

// ErrNoProject means no project config was found.
var ErrNoProject = errors.New("no svx.yaml")

// UsageError is a mistake in how svx was called.
type UsageError struct{ Err error }

func (e *UsageError) Error() string { return e.Err.Error() }
func (e *UsageError) Unwrap() error { return e.Err }

func usage(err error) error { return &UsageError{Err: err} }

// ServiceError reports services that failed to start or become ready.
type ServiceError struct{ Services []string }

func (e *ServiceError) Error() string {
	if len(e.Services) == 1 {
		return "service " + e.Services[0] + " failed, its window is left open for inspection: svx attach " + e.Services[0]
	}
	return "services failed: " + strings.Join(e.Services, ", ") + "; their windows are left open for inspection"
}

// ExitCode maps an error to the process exit code.
func ExitCode(err error) int {
	var (
		usageErr  *UsageError
		configErr *domain.ConfigError
	)
	if errors.As(err, &usageErr) || errors.As(err, &configErr) {
		return ExitUsage
	}
	return ExitFailure
}
