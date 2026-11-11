package architecturecodec

// ReviewApprovalOriginAllowed validates metadata only. Only the owner's
// browser Accept is an acceptance; a model-originated or instruction-bound
// approval is never one, in either edition.
func ReviewApprovalOriginAllowed(approval map[string]any) bool {
	return approval["source"] == "browser_operator" && approval["model_self_approval"] != true && approval["operator_instruction_ref"] == nil
}
