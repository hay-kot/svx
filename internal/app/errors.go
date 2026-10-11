package app

import (
	"errors"
	"fmt"
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
		return "service " + e.Services[0] + " failed, see its output with `svx logs " + e.Services[0] + "`"
	}
	return "services failed: " + strings.Join(e.Services, ", ") + "; see their output with `svx logs <service>`"
}

// CommandExitError carries a service command's exit code out of the _run shim,
// so the pane reports the command's status. The command already printed
// its own output, so the CLI shows nothing for it.
type CommandExitError struct{ Code int }

func (e *CommandExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// ExitCode maps an error to the process exit code.
func ExitCode(err error) int {
	var (
		usageErr  *UsageError
		configErr *domain.ConfigError
		cmdExit   *CommandExitError
	)
	if errors.As(err, &cmdExit) {
		return cmdExit.Code
	}
	if errors.As(err, &usageErr) || errors.As(err, &configErr) {
		return ExitUsage
	}
	return ExitFailure
}
