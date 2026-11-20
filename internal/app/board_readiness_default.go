//go:build governed

package app

import "github.com/Zen1th53/marshal/internal/marshal"

// WorkerConcurrency: this edition has no per-provider worker limits.
func WorkerConcurrency(marshal.Settings) (limits map[string]int, globalCap int) { return nil, 0 }
