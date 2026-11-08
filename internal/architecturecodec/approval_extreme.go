//go:build !governed

package architecturecodec

import "strings"

func ArchitectureApprovalOriginAllowed(a ArchitectureAcceptance) bool {
	if a.Origin == "owner_browser" {
		return !a.ModelSelfApproval && a.OperatorInstructionRef == ""
	}
	return a.Origin == "owner_command" && a.Policy == "private_extreme" &&
		a.ModelSelfApproval && strings.TrimSpace(a.OperatorInstructionRef) != ""
}

func ModelArchitectureApprovalAllowed() bool { return true }
