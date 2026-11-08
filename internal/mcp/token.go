package mcp

import (
	"fmt"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/auth"
)

// WorkerTokenCapabilities defines the narrowest capability scope for MCP worker clients:
// memory read (task.read) and unverified candidate memory write (task.execute).
// It deliberately omits promote and tombstone capabilities (auth.CapAll, auth.CapVerifyRun, etc.),
// so worker agents can propose candidates and read memory without any authority to alter
// durable state or revoke records.
var WorkerTokenCapabilities = []string{
	string(auth.CapTaskRead),
	string(auth.CapTaskExecute),
}

// CreateWorkerToken issues a per-session, per-provider MCP client token with the narrowest
// capability scope (memory read + candidate write, never promote/tombstone).
func CreateWorkerToken(authMgr *auth.Manager, provider, sessionID string) (string, auth.TokenRecord, error) {
	if authMgr == nil {
		return "", auth.TokenRecord{}, fmt.Errorf("auth manager is required")
	}
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = "worker"
	}
	name := fmt.Sprintf("mcp-%s", provider)
	if sessionID != "" {
		name = fmt.Sprintf("mcp-%s-%s", provider, sessionID)
	} else {
		name = fmt.Sprintf("mcp-%s-%d", provider, time.Now().UnixNano())
	}

	plaintext, record, err := authMgr.CreateTokenWithTTL(name, auth.KindMCPClient, WorkerTokenCapabilities, 24*time.Hour)
	if err != nil {
		return "", auth.TokenRecord{}, fmt.Errorf("create worker MCP token for %s: %w", provider, err)
	}
	return plaintext, record, nil
}

// RevokeWorkerToken revokes an issued worker MCP token by its ID, preventing any
// subsequent requests from authenticating.
func RevokeWorkerToken(authMgr *auth.Manager, tokenID string) error {
	if authMgr == nil || tokenID == "" {
		return nil
	}
	return authMgr.RevokeToken(tokenID)
}
