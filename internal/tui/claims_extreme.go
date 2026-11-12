//go:build !governed

package tui

// Text that says what this build does. The Marshal build runs workers on
// the host with no sandbox and no network or credential broker, and the
// Marshal approves plans and delivery itself, so nothing here claims
// otherwise. claims_default.go holds the governed edition's wording.
const (
	securityKeyWhat    = "security: network calls and audit"
	egressStatusHeader = "Workers run on this computer and use your network connection directly. Marshal lists the network calls it records; it does not block them.\n"
	sandboxStatusText  = "No sandbox: workers run on this computer as you, each task in its own Git worktree."
	providerEgressLine = "    Network: the worker uses your normal network connection\n"

	// egressReportsEnforcement: /egress probes and reports bubblewrap
	// network isolation only where the build uses it.
	egressReportsEnforcement = false
	// egressNoRunsText is /egress with nothing recorded.
	egressNoRunsText = "No network calls recorded yet.\n"
	// showQualificationLabel: the Marshal build passes a provider's own
	// commands through without the governed build's qualification label.
	showQualificationLabel = false
	// providerStatusHeading heads /provider status.
	providerStatusHeading = "Providers:"
)

// credentialRequestQueuedText answers /permission credential request. This
// build has no credential broker: the Marshal decides whether tasks may use
// the provider's own sign-in.
func credentialRequestQueuedText(provider string) string {
	return "Asked to let tasks use " + provider + "'s sign-in in this project. The Marshal decides."
}

// credentialRevocationMessage answers /permission credential revoke and
// status from the revocation's state, in this build's words.
func credentialRevocationMessage(provider, state string) string {
	switch state {
	case "pending":
		return "Tasks may no longer use " + provider + "'s sign-in; running ones stop once they confirm. Check /permission credential status " + provider + "."
	case "closed":
		return "Tasks may no longer use " + provider + "'s sign-in; every running task has stopped using it."
	default:
		return "Nothing about " + provider + "'s sign-in was revoked."
	}
}
