// Package version exposes build metadata injected by the release pipeline.
package version

import "fmt"

// These values are replaced with -ldflags for tagged release builds.
var (
	Version   = "dev"
	Commit    = "unknown"
	BuildDate = "unknown"
)

// Info is the stable machine-readable version result.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"build_date"`
}

// Current returns the metadata embedded in the running binary.
func Current() Info {
	return Info{Version: valueOr(Version, "dev"), Commit: valueOr(Commit, "unknown"), BuildDate: valueOr(BuildDate, "unknown")}
}

// String formats version metadata for operators.
func (i Info) String() string {
	return fmt.Sprintf("marshal %s (commit %s, built %s)", valueOr(i.Version, "dev"), valueOr(i.Commit, "unknown"), valueOr(i.BuildDate, "unknown"))
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
