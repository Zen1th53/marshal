//go:build governed

package app

import "time"

// Community: the operator decides every provider credential request, and
// every recorded denial, from any decision surface, stays binding.
const automaticCredentialDecision = false

// boundTaskCredentialWindow bounds one task invocation's credential lease.
const boundTaskCredentialWindow = 300 * time.Second

func bindingCredentialDenial(string) bool { return true }
