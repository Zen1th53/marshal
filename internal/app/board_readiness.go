package app

import (
	"path/filepath"
	"runtime"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/sandbox"
)

// What the Board's first-open checklist asks of the runtime: whether workers
// can run on this computer, whether a provider failure is a missing sign-in,
// and the telemetry choice. Each answer reuses the classifier or file the
// runtime itself decides by, so the checklist never says more than a run
// would find.

// supervisedArgv is sandbox.SupervisedArgv; tests replace it.
var supervisedArgv = sandbox.SupervisedArgv

// platformName is the computer as the checklist names it: "Linux amd64",
// "macOS arm64".
func platformName(goos, goarch string) string {
	name := map[string]string{"linux": "Linux", "darwin": "macOS", "windows": "Windows", "freebsd": "FreeBSD"}[goos]
	if name == "" {
		name = goos
	}
	return name + " " + goarch
}

// WorkerPlatform says whether workers can run on this computer, and the
// computer's name. Where workers run on the host (the Marshal build) every
// platform can; where they need confinement, the confinement's own refusal
// is classified as a worker failure would be, so a computer that cannot host
// a worker reads as the platform limit a run would stop on.
func WorkerPlatform() (ok bool, host, reason string) {
	host = platformName(runtime.GOOS, runtime.GOARCH)
	if sandbox.HostExecutionPreferred() {
		return true, host, ""
	}
	if _, _, err := supervisedArgv([]string{"true"}); err != nil {
		if kind, _ := classifyInfraFailure(err.Error()); kind == platformUnsupported {
			return false, host, err.Error()
		}
	}
	return true, host, ""
}

// IsSignInFailure reports whether a provider's failure text names a missing
// or expired sign-in, by the same markers a worker failure is classified by.
func IsSignInFailure(text string) bool { return isProviderAuthFailure(text) }

// TelemetryChoice is whether anonymous telemetry is on for this installation
// and whether the owner has been told about it yet (the one-time notice). It
// reads the saved choice and never creates an identity.
func TelemetryChoice(root string) (on, told bool) {
	if !cloud.LoadConfig().Enabled() {
		return false, true
	}
	dir, err := cloud.MachineStateDir(filepath.Join(root, projectid.StateDirName))
	if err != nil {
		return true, false
	}
	st, err := cloud.NewStore(dir).Load()
	if err != nil {
		return true, false
	}
	return !st.TelemetryDisabled, st.TelemetryNoticeShown || st.TelemetryDisabled
}
