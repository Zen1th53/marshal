package app

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"time"
)

// agySigninExpiryReason names an agy worker failure caused by an expired
// sign-in token. Failure text carrying it is a provider limit, not a retry.
const agySigninExpiryReason = "agy sign-in expired"

// agySigninExpiryCooldown is how long agy is skipped after its sign-in expired.
const agySigninExpiryCooldown = time.Hour

// agySigninQuickExit bounds the "ends almost at once with no output" signature
// of an agy run whose sign-in token expired.
const agySigninQuickExit = 5 * time.Second

var errAgySigninExpired = errors.New(agySigninExpiryReason)

// agySigninMarkers name a sign-in or authentication failure in lowercased agy output.
var agySigninMarkers = []string{
	"not signed in", "not logged in", "sign-in expired", "sign in expired", "signin expired",
	"session expired", "session has expired", "token expired", "token has expired", "expired token",
	"invalid_grant", "unauthenticated", "401 unauthorized", "please sign in", "please log in",
	"sign in again", "sign-in required", "login required", "authentication required",
	"authentication failed", "reauthenticate", "re-authenticate", "(code 401)", " 401 ",
}

// classifyAgySigninExpiry reports whether a failed agy run ended on an expired
// sign-in. It reads only the worker's own error, stdout and stderr, never a
// credential, token or sign-in file. A run that failed almost at once with no
// output at all is also treated as a sign-in expiry.
func classifyAgySigninExpiry(runErr error, exitCode int, stdout, stderr []byte, elapsed time.Duration) bool {
	if runErr == nil && exitCode == 0 {
		return false
	}
	if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
		return false
	}
	var sb strings.Builder
	if runErr != nil {
		sb.WriteString(runErr.Error())
	}
	sb.WriteString("\n")
	sb.Write(stdout)
	sb.WriteString("\n")
	sb.Write(stderr)
	sb.WriteString("\n")
	lower := strings.ToLower(sb.String())
	if lineNamesLimit(strings.ToLower(providerLimitText(sb.String()))) {
		return false
	}
	for _, m := range agySigninMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return len(bytes.TrimSpace(stdout)) == 0 && len(bytes.TrimSpace(stderr)) == 0 && elapsed > 0 && elapsed <= agySigninQuickExit
}
