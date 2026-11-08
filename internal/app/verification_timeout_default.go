//go:build governed

package app

import "time"

// verificationTimeout bounds one repository verification run.
const verificationTimeout = 15 * time.Minute
