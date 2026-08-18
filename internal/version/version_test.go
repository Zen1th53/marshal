package version

import "testing"

func TestCurrentReturnsBuildMetadata(t *testing.T) {
	originalVersion, originalCommit, originalDate := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version, Commit, BuildDate = originalVersion, originalCommit, originalDate
	})
	Version = "v1.0.0-rc.1"
	Commit = "0123456789abcdef"
	BuildDate = "2026-08-18T12:00:00Z"

	got := Current()
	if got.Version != Version || got.Commit != Commit || got.BuildDate != BuildDate {
		t.Fatalf("Current() = %#v", got)
	}
	if got.String() != "marshal v1.0.0-rc.1 (commit 0123456789abcdef, built 2026-08-18T12:00:00Z)" {
		t.Fatalf("String() = %q", got.String())
	}
}

func TestDevelopmentMetadataIsExplicit(t *testing.T) {
	if Version == "" || Commit == "" || BuildDate == "" {
		t.Fatalf("empty development metadata: version=%q commit=%q date=%q", Version, Commit, BuildDate)
	}
}
