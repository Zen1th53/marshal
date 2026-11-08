//go:build !governed

package driver

import "time"

// DefaultCheckTimeout is zero in Extreme: an acceptance check has no
// wall-clock limit (owner rule: nothing in Extreme may stop the work). A
// check still ends when it exits or when the run is stopped or cancelled.
const DefaultCheckTimeout time.Duration = 0
