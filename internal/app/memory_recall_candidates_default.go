//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/model"

// recallsCandidateMemory reports whether an unreviewed candidate is eligible
// for recall. Default requires operator approval first.
func recallsCandidateMemory(model.MemoryRecordV2) bool { return false }
