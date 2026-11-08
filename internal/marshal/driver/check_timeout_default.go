//go:build governed

package driver

import "time"

// DefaultCheckTimeout bounds one acceptance check when a driver sets none.
const DefaultCheckTimeout = 10 * time.Minute
