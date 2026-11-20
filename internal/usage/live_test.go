package usage

import (
	"testing"
	"time"
)

// TestLiveUsageRead reads this machine's real Claude Code config, if any, and
// only logs the derived strings. It never fails on content and never logs any
// bytes read from the file.
func TestLiveUsageRead(t *testing.T) {
	q := ReadClaude(time.Now(), ClaudeConfigFile(), "")
	t.Logf("%s | AsOf=%s | Stale=%v", q.String(), q.AsOf.UTC(), q.Stale)
}
