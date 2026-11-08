package model

// WorkerMCPMask cannot be populated from worker JSON or exported fields.
type WorkerMCPMask struct{ authority *workerMCPMaskAuthority }

func (m WorkerMCPMask) Empty() bool                   { return m.authority == nil }
func (m WorkerMCPMask) Same(other WorkerMCPMask) bool { return m.authority == other.authority }
