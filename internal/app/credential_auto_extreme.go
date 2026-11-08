//go:build !governed

package app

import "time"

// Extreme: a registered provider's credential is applied by the runtime
// itself, standing or bounded, with or without a decision surface. Bounded
// leases keep their exact scope, expiry and one-invocation binding; only the
// human/Marshal decision step is removed.
const automaticCredentialDecision = true

// boundTaskCredentialWindow bounds one task invocation's credential lease.
// An approved-plan worker routinely runs longer than five minutes, and the
// egress watcher closes the brokered proxy when the lease expires, so the
// window covers a whole invocation; the invocation's context deadline still
// shortens it, and the lease still closes when its egress ends.
const boundTaskCredentialWindow = 4 * time.Hour

// bindingCredentialDenial reports whether a recorded credential denial keeps
// blocking. Only the explicit operator revoke command does; a popup answer
// (for example a stray keypress) or a timeout is superseded by the next
// automatic grant instead of stopping work permanently.
func bindingCredentialDenial(source string) bool { return source == "operator revoke" }
