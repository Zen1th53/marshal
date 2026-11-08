//go:build governed

package app

import (
	"context"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/store"
)

// workerMemoryScanLimit bounds the rows examined; unusable rows are skipped
// before the brief cap so they cannot crowd out usable ones.
const (
	workerMemoryScanLimit = 200
	workerMemoryBriefMax  = 10
)

func configureWorkerMemory(*MarshalService, *Runtime) {}

func (s *MarshalService) addWorkerMemory(ctx context.Context, _ string, _ marshal.Run, _ marshal.Task, _ bool, bc *BriefContext) error {
	if s.Store != nil && s.ProjectID != "" {
		records, err := s.Store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: s.ProjectID, Scope: model.ScopeProject, Limit: workerMemoryScanLimit})
		now := time.Now().UTC()
		if err == nil {
			for _, rec := range records {
				if len(bc.Memory) >= workerMemoryBriefMax {
					break
				}
				if !rec.WorkerUsable(now) {
					continue
				}
				if rec.Scope == string(model.ScopeSession) || rec.Source.Kind == "shared_channel" || rec.IsSessionHistory() {
					continue
				}
				bc.Memory = append(bc.Memory, rec)
			}
		}
	}
	return nil
}
