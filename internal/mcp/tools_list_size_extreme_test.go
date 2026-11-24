//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
)

// Before shortening: 24720 bytes (wire) / 24686 bytes (result). Shortened
// prose reached 14586 bytes; the typed proposal and contract schemas of the
// two submit tools then add about 6.1 KB, so the cap is that size plus a
// small margin. Grow it only for schema, never for prose.
const (
	marshalToolsListBeforeBytes = 24720
	marshalToolsListMaxBytes    = 20900
)

func marshalToolsListServer() *Server {
	s := NewExtremeServer(nil, "marshal", "")
	s.proposalCall = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	s.ConfigureChatPermission(func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil })
	return s
}
