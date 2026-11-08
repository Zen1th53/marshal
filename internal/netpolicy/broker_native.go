package netpolicy

import (
	"context"
	"net/http"
	"time"
)

// The native OAuth source is host-only and independent of Codex/Claude
// subscription refresh and account-header behavior.
type nativeOAuthCredential struct {
	access       string
	expires      time.Time
	replacements map[string]string
}
type nativeOAuthSource interface {
	read(context.Context, *CredentialBroker) (nativeOAuthCredential, error)
	prepare(string, *CredentialBroker) error
	requestAllowed(*http.Request, []byte) bool
	responseForWorker(*http.Request, *http.Response, []byte, []string) ([]byte, bool)
	refusesAuthHost(string) bool
}
