package claude

import (
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

// Claude prints nothing until it ends, so its worker keeps the completion
// event but must never be stopped for silence.
func TestClaudeWorkerIsNotStoppedForSilence(t *testing.T) {
	live := quietWorkerLiveness(adapter.Request{StallAfter: 3 * time.Minute, IdleTimeout: 6 * time.Minute, Stalled: func(time.Duration) {}})
	if live == nil || live.Classify == nil || live.DoneGrace <= 0 {
		t.Fatalf("completion detection lost: %+v", live)
	}
	if live.IdleTimeout != 0 || live.StallAfter != 0 || live.Stalled != nil {
		t.Fatalf("silent Claude worker can still be idle-stopped: %+v", live)
	}
}
