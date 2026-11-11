package architecturecodec

// ArchitectureApprovalOriginAllowed is true only for the owner's browser
// Accept. Acceptance is the same in both editions: the Marshal never accepts
// the architecture, and no recorded instruction stands in for the click.
func ArchitectureApprovalOriginAllowed(a ArchitectureAcceptance) bool {
	return a.Origin == "owner_browser" && !a.ModelSelfApproval && a.OperatorInstructionRef == ""
}
