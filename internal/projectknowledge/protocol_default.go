//go:build governed

package projectknowledge

import "github.com/Zen1th53/marshal/internal/constitution"

func sourceKnowledgeProtocolDigest() string { return constitution.MarshalProtocolDigest }
