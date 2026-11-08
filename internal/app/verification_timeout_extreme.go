//go:build !governed

package app

import "time"

// verificationTimeout is zero in Extreme: repository verification has no
// wall-clock limit (owner rule: nothing in Extreme may stop the work). It
// still ends when the command exits or the context is cancelled.
const verificationTimeout time.Duration = 0
