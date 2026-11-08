//go:build linux && !race

package tui

import (
	"os"
	"time"
)

// ptyPatience is the uninstrumented multiplier: the waits are already sized
// for a machine running at full speed. Shared CI runners are slower, so CI
// doubles them. See the race-tagged file for why the instrumented run needs
// more.
var ptyPatience = func() time.Duration {
	if os.Getenv("CI") != "" {
		return 2
	}
	return 1
}()
