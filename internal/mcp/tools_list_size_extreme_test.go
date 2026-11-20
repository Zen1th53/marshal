//go:build !governed

package mcp

import (
	"context"
	"encoding/json"
)

// Before shortening: 24720 bytes (wire) / 24686 bytes (result).
// Target (>= 40% reduction): <= 14832 bytes (wire).
const (
	marshalToolsListBeforeBytes = 24720
	marshalToolsListMaxBytes    = 14600
)

func marshalToolsListServer() *Server {
	s := NewExtremeServer(nil, "marshal", "")
	s.proposalCall = func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, nil }
	s.ConfigureChatPermission(func(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil })
	return s
}
