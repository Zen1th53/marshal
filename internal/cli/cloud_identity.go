package cli

import (
	"context"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/projectid"
)

// cloudAuthorize asks the Community Cloud about this session, identifying the
// installation by its one machine-level identity and MARSHAL by its own
// version.
//
// The machine-level directory is resolved only when a Cloud is configured, so
// running offline never touches the user's configuration. If it cannot be
// resolved, the project directory is used as before rather than losing ULTRA.
func cloudAuthorize(ctx context.Context, root string) cloud.Authorization {
	cfg := cloud.LoadConfig()
	cfg.TelemetryManaged = true
	stateDir := filepath.Join(root, projectid.StateDirName)
	if cfg.Enabled() {
		if dir, err := cloud.MachineStateDir(stateDir); err == nil {
			stateDir = dir
		}
	}
	a := cloud.Authorize(ctx, cfg, stateDir, cloudClientVersion(Version))
	observeCloudAuthorization(ctx, a)
	return a
}

// cloudClientVersion is the MARSHAL version as the Cloud records it: a bare
// semantic version, without the release tag's leading "v". Development builds
// use a fixed semantic version rather than arbitrary VCS/build text.
var cloudSemanticVersion = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]{1,32})?$`)

func cloudClientVersion(version string) string {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	if !cloudSemanticVersion.MatchString(version) {
		return "0.0.0-dev"
	}
	return version
}
