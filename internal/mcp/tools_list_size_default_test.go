//go:build governed

package mcp

// Before shortening: 5158 bytes (wire) / 5124 bytes (result).
// Target (>= 40% reduction): <= 3094 bytes (wire).
const (
	marshalToolsListBeforeBytes = 5158
	marshalToolsListMaxBytes    = 3080
)

func marshalToolsListServer() *Server {
	return NewScopedServer(nil, "marshal", "")
}
