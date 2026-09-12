package tui

// The frozen IA travels inside the binary.
//
// Reading MANIFEST.json from disk at startup would make the navigation depend
// on a path that exists in this repository and in no installation. Embedding it
// means a shipped MARSHAL carries the exact IA its code was built against, and a
// spec pack that moves or is deleted cannot leave the TUI with no menu.

import (
	_ "embed"
	"sync"
)

//go:embed manifest.json
var frozenManifest []byte

var (
	frozenOnce sync.Once
	frozenIA   *IA
	frozenErr  error
)

// FrozenIA returns the embedded Community TUI information architecture.
//
// It is parsed once. A failure is returned rather than panicking, so a build
// with a damaged manifest reports the problem through the normal error path
// instead of taking down a session that was otherwise working.
func FrozenIA() (*IA, error) {
	frozenOnce.Do(func() {
		frozenIA, frozenErr = LoadIA(frozenManifest)
	})
	return frozenIA, frozenErr
}
