package mcp

import (
	"context"
	"encoding/json"
	"math"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type knowledgeWireIDKey struct{}

func knowledgeRequestContext(ctx context.Context, id any) context.Context {
	return context.WithValue(withMCPResponseID(ctx, id), knowledgeWireIDKey{}, id)
}

func knowledgeStringArg(args map[string]any, key string, limit int) (string, error) {
	value, exists := args[key]
	if !exists {
		return "", nil
	}
	text, ok := value.(string)
	if !ok || len(text) > limit || strings.ContainsRune(text, '\x00') {
		return "", model.ErrInvalid
	}
	return text, nil
}

func knowledgeIntArg(args map[string]any, key string, fallback, min, max int) (int, error) {
	value, exists := args[key]
	if !exists {
		return fallback, nil
	}
	var n float64
	switch v := value.(type) {
	case int:
		n = float64(v)
	case float64:
		n = v
	case json.Number:
		var err error
		n, err = v.Float64()
		if err != nil {
			return 0, model.ErrInvalid
		}
	default:
		return 0, model.ErrInvalid
	}
	if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n || n < float64(min) || n > float64(max) {
		return 0, model.ErrInvalid
	}
	return int(n), nil
}

func knowledgePayloadBytes(ctx context.Context, text string) (int, error) {
	data, err := json.Marshal(jsonRPCResponse{JSONRPC: "2.0", ID: ctx.Value(knowledgeWireIDKey{}), Result: map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}})
	return len(data) + 1, err
}
