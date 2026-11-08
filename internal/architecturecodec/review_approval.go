package architecturecodec

// ReviewApprovalOriginAllowed validates metadata only. Native model receipts
// additionally require the runtime's SQLite instruction and source check.
func ReviewApprovalOriginAllowed(approval map[string]any) bool {
	if approval["source"] == "browser_operator" {
		return approval["model_self_approval"] != true && approval["operator_instruction_ref"] == nil
	}
	return ModelArchitectureApprovalAllowed() && approval["source"] == "marshal_operator_command" && approval["model_self_approval"] == true && nonempty(approval["operator_instruction_ref"]) && nonempty(approval["receipt_id"])
}
