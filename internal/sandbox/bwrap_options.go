package sandbox

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type optionProbeKey struct {
	binary string
	size   int64
	mtime  int64
	option string
}

var optionProbeCache sync.Map

// bwrapSupportsOption reports whether the bubblewrap binary lists option in its
// help text. Older releases (for example the 0.6 series shipped with Ubuntu
// 22.04) reject options added later, such as --size, with "Unknown option".
// The answer is cached per binary identity. If the help text cannot be read the
// option is assumed to be supported so that a genuine failure surfaces from the
// real launch instead of being masked here.
func bwrapSupportsOption(binary, option string) bool {
	info, err := os.Stat(binary)
	if err != nil {
		return true
	}
	key := optionProbeKey{binary: binary, size: info.Size(), mtime: info.ModTime().UnixNano(), option: option}
	if cached, ok := optionProbeCache.Load(key); ok {
		return cached.(bool)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, binary, "--help").CombinedOutput()
	supported := true
	if len(output) > 0 {
		supported = strings.Contains(string(output), option)
	} else if err != nil {
		return true
	}
	optionProbeCache.Store(key, supported)
	return supported
}
