//go:build governed || !unix

package model

type workerMCPMaskAuthority struct{}

func (WorkerMCPMask) Allows(string, string, string) bool { return false }

func (m WorkerMCPMask) ValidEnvelope(SandboxRequest) bool { return m.Empty() }
