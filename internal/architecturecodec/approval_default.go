//go:build governed

package architecturecodec

func ArchitectureApprovalOriginAllowed(a ArchitectureAcceptance) bool {
	return a.Origin == "owner_browser" && !a.ModelSelfApproval && a.OperatorInstructionRef == ""
}

func ModelArchitectureApprovalAllowed() bool { return false }
