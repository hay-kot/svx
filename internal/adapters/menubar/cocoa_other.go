//go:build !darwin || !cgo

package menubar

import "context"

// Supported reports whether this build has the Cocoa UI.
const Supported = false

func runUI(context.Context, *Bar) error { return ErrUnsupported }
