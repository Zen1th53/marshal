//go:build governed

package plan

func sealPrivateProducerAssignments(ExecutionPlan, *ApprovalScope) error { return nil }
func pinPrivateFallbackProfile(HarnessCandidate, *Fallback)              {}
