//go:build !governed

package app

import "github.com/Zen1th53/marshal/internal/model"

// recallsCandidateMemory: Extreme's Marshal self-approves, so memory it
// retained through its bound live launch stays recallable without a separate
// operator promotion. Every other candidate (EGM drafts, imports, worker
// proposals) remains inactive until promoted.
func recallsCandidateMemory(rec model.MemoryRecordV2) bool { return rec.Source.Kind == "live_mcp" }
