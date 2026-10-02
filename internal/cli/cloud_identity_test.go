package cli

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/Zen1th53/marshal/internal/constitution"
)

// The Cloud's telemetry accepts a bare semantic version; this is its pattern.
var cloudVersionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+(?:[-+][A-Za-z0-9.-]{1,32})?$`)

// The Cloud must record MARSHAL's version, not the constitution's.
func TestCloudClientVersionIsMarshalsOwn(t *testing.T) {
	for in, want := range map[string]string{"v1.5.0": "1.5.0", "1.5.0": "1.5.0", " v0.0.4-rc.1 ": "0.0.4-rc.1"} {
		if got := cloudClientVersion(in); got != want || !cloudVersionPattern.MatchString(got) {
			t.Errorf("cloudClientVersion(%q) = %q, want %q", in, got, want)
		}
	}
	if Version != constitution.Current.String() && cloudClientVersion(Version) == constitution.Current.String() {
		t.Fatalf("the Cloud would be sent the constitution version %q", constitution.Current.String())
	}
}

// Offline, the machine-level identity is never created or touched.
func TestCloudAuthorizeOfflineLeavesUserConfigAlone(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	auth := cloudAuthorize(context.Background(), t.TempDir())
	if auth.Gate != nil || auth.Client != nil {
		t.Fatal("an offline session reached a Cloud")
	}
	if _, err := os.Stat(filepath.Join(config, "marshal")); !os.IsNotExist(err) {
		t.Fatalf("offline run wrote to the user's configuration: %v", err)
	}
}
