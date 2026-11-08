//go:build governed

package mcp

// Default workers may propose unverified candidate memory and design records
// while their task is live; promotion stays with the operator.
// directCandidateRetention reports whether memory_remember persists a candidate
// directly. Extreme retains memory only through the bound live Marshal.
const directCandidateRetention = true

const workerCandidateWrites = true

func workerCandidateTools() []Tool {
	return []Tool{
		designRecordTool(),
		{Name: "memory_remember", Description: "Propose unverified candidate memory", InputSchema: objectSchema(map[string]any{"title": stringSchema(), "body": stringSchema()}, "title", "body")},
	}
}
