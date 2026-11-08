//go:build !governed

package mcp

// Extreme workers are read-only: retention requires the bound live Marshal.
// directCandidateRetention reports whether memory_remember persists a candidate
// directly. Extreme retains memory only through the bound live Marshal.
const directCandidateRetention = false

const workerCandidateWrites = false

func workerCandidateTools() []Tool { return nil }
