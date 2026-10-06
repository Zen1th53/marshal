package driver

import "github.com/Zen1th53/marshal/internal/processgroup"

// StopRecovered delegates a persisted session's lifetime to its pinned supervisor.
func StopRecovered(ref processgroup.Reference) error { return processgroup.StopReference(ref) }
