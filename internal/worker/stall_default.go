//go:build governed

package worker

import (
	"time"

	"github.com/Zen1th53/marshal/internal/adapter"
)

type workerStallWatch struct{ interval, window time.Duration }

func newWorkerStallWatch(adapter.Command) *workerStallWatch { return nil }
func (*workerStallWatch) output([]byte)                     {}
func (*workerStallWatch) stalled(time.Time) bool            { return false }
