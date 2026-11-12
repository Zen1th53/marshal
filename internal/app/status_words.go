package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
)

// Telemetry lines, as /status and the help show them.
const (
	TelemetryOnLine  = "anonymous telemetry: on (marshal telemetry disable turns it off)"
	TelemetryOffLine = "anonymous telemetry: off"
)

// TelemetryLine says, plainly, whether anonymous telemetry is on for this
// installation. It reads the saved choice and never creates an identity:
// telemetry is on unless the endpoint is switched off or the user disabled
// it with `marshal telemetry disable`.
func TelemetryLine(root string) string {
	if !cloud.LoadConfig().Enabled() {
		return TelemetryOffLine
	}
	dir, err := cloud.MachineStateDir(filepath.Join(root, projectid.StateDirName))
	if err != nil {
		return TelemetryOnLine
	}
	if st, err := cloud.NewStore(dir).Load(); err == nil && st.TelemetryDisabled {
		return TelemetryOffLine
	}
	return TelemetryOnLine
}

// MarshalProviderName is the display name of the project's saved Marshal
// provider, or "" when none is saved yet.
func MarshalProviderName(root string) string {
	if root == "" {
		return ""
	}
	path := statepath.Path(root, "default-provider")
	if statepath.CheckPath(root, path) != nil {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(string(data))
	if name == "" {
		return ""
	}
	return ProviderDisplayName(name)
}
