package cli

import (
	"runtime/debug"
	"testing"
)

func TestFillBuildIdentity(t *testing.T) {
	settings := func(kv ...string) []debug.BuildSetting {
		if len(kv)%2 != 0 {
			t.Fatalf("settings must be key/value pairs, got %d values", len(kv))
		}
		var out []debug.BuildSetting
		for i := 0; i < len(kv); i += 2 {
			out = append(out, debug.BuildSetting{Key: kv[i], Value: kv[i+1]})
		}
		return out
	}

	const rev = "a1a16d9e381b3b66e8cc3abde6d2fd7c19c8c65f"
	const stampTime = "2026-10-26T16:50:17Z"

	tests := []struct {
		name        string
		version     string
		commit      string
		buildDate   string
		settings    []debug.BuildSetting
		wantVersion string
		wantCommit  string
		wantDate    string
	}{
		{
			name:        "ldflags win",
			version:     "v1.2.3",
			commit:      "deadbeefcafe",
			buildDate:   "2026-01-02T03:04:05Z",
			settings:    settings("vcs.revision", rev, "vcs.time", stampTime, "vcs.modified", "true"),
			wantVersion: "v1.2.3",
			wantCommit:  "deadbeefcafe",
			wantDate:    "2026-01-02T03:04:05Z",
		},
		{
			name:        "clean source build",
			version:     defaultVersion,
			commit:      defaultCommit,
			buildDate:   defaultBuildDate,
			settings:    settings("vcs.revision", rev, "vcs.time", stampTime, "vcs.modified", "false"),
			wantVersion: "v0.2.0+a1a16d9e",
			wantCommit:  rev,
			wantDate:    stampTime,
		},
		{
			name:        "dirty source build",
			version:     defaultVersion,
			commit:      defaultCommit,
			buildDate:   defaultBuildDate,
			settings:    settings("vcs.revision", rev, "vcs.time", stampTime, "vcs.modified", "true"),
			wantVersion: "v0.2.0+a1a16d9e-dirty",
			wantCommit:  rev,
			wantDate:    stampTime,
		},
		{
			name:        "short revision is not padded",
			version:     defaultVersion,
			commit:      defaultCommit,
			buildDate:   defaultBuildDate,
			settings:    settings("vcs.revision", "abc1234", "vcs.time", stampTime, "vcs.modified", "false"),
			wantVersion: "v0.2.0+abc1234",
			wantCommit:  "abc1234",
			wantDate:    stampTime,
		},
		{
			name:        "no vcs data keeps today's output",
			version:     defaultVersion,
			commit:      defaultCommit,
			buildDate:   defaultBuildDate,
			settings:    nil,
			wantVersion: defaultVersion,
			wantCommit:  defaultCommit,
			wantDate:    defaultBuildDate,
		},
		{
			name:        "partial ldflags still wins",
			version:     defaultVersion,
			commit:      "deadbeefcafe",
			buildDate:   defaultBuildDate,
			settings:    settings("vcs.revision", rev, "vcs.time", stampTime, "vcs.modified", "false"),
			wantVersion: defaultVersion,
			wantCommit:  "deadbeefcafe",
			wantDate:    defaultBuildDate,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotCommit, gotDate := fillBuildIdentity(tt.version, tt.commit, tt.buildDate, vcsStampFrom(tt.settings))
			if gotVersion != tt.wantVersion || gotCommit != tt.wantCommit || gotDate != tt.wantDate {
				t.Fatalf("fillBuildIdentity(%q, %q, %q) = (%q, %q, %q), want (%q, %q, %q)",
					tt.version, tt.commit, tt.buildDate,
					gotVersion, gotCommit, gotDate,
					tt.wantVersion, tt.wantCommit, tt.wantDate)
			}
		})
	}
}
