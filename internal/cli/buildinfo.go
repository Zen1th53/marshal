package cli

import "runtime/debug"

// defaultVersion, defaultCommit, and defaultBuildDate are the identity a
// source build carries before -ldflags or VCS stamping supplies anything
// real. tools/build_release.sh overrides all three via -ldflags, so a value
// that still matches its default means the linker left it untouched.
const (
	defaultVersion   = "v0.2.0"
	defaultCommit    = "head"
	defaultBuildDate = "unknown"
)

// vcsStamp is the git identity Go embeds when a binary is built inside a
// checkout with build VCS stamping enabled (the default for go build).
type vcsStamp struct {
	revision string
	time     string
	modified bool
}

// vcsStampFrom selects the vcs.* settings Go recorded at build time.
func vcsStampFrom(settings []debug.BuildSetting) vcsStamp {
	var v vcsStamp
	for _, s := range settings {
		switch s.Key {
		case "vcs.revision":
			v.revision = s.Value
		case "vcs.time":
			v.time = s.Value
		case "vcs.modified":
			v.modified = s.Value == "true"
		}
	}
	return v
}

// currentVCSStamp reads the VCS settings embedded in this binary, returning a
// zero stamp when none are present (for example -buildvcs=false, the release
// path, or a test binary).
func currentVCSStamp() vcsStamp {
	if info, ok := debug.ReadBuildInfo(); ok {
		return vcsStampFrom(info.Settings)
	}
	return vcsStamp{}
}

// fillBuildIdentity resolves the three identity strings against the build's
// VCS stamp. ldflags values always win: once any of version, commit, or
// buildDate no longer carries its source default, a release identity was
// injected and all three are returned untouched. A source build inside a git
// checkout is otherwise identified from its revision, and the defaults are
// kept only when no VCS stamp is available.
func fillBuildIdentity(version, commit, buildDate string, v vcsStamp) (string, string, string) {
	if version != defaultVersion || commit != defaultCommit || buildDate != defaultBuildDate {
		return version, commit, buildDate
	}
	if v.revision == "" {
		return version, commit, buildDate
	}
	short := v.revision
	if len(short) > 8 {
		short = short[:8]
	}
	version = defaultVersion + "+" + short
	if v.modified {
		version += "-dirty"
	}
	return version, v.revision, v.time
}
