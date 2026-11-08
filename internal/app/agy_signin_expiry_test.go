package app

import (
	"errors"
	"testing"
	"time"

	"github.com/Zen1th53/marshal/internal/marshal"
)

func TestAgySigninExpiryClassifiesAuthExpiry(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		exit           int
		stdout, stderr string
		elapsed        time.Duration
	}{
		{name: "token expired on stderr", exit: 1, stderr: "Error: authentication token has expired; please sign in again", elapsed: 3 * time.Second},
		{name: "unauthenticated error", err: errors.New("agy: rpc error: code = Unauthenticated desc = invalid_grant"), exit: 1, elapsed: time.Minute},
		{name: "not signed in on stdout", exit: 2, stdout: "You are not signed in to Antigravity."},
		{name: "immediate exit without output", exit: 1, elapsed: 200 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !classifyAgySigninExpiry(tc.err, tc.exit, []byte(tc.stdout), []byte(tc.stderr), tc.elapsed) {
				t.Fatalf("not classified as an agy sign-in expiry")
			}
		})
	}
}

func TestAgySigninExpiryOrdinaryFailureNotClassified(t *testing.T) {
	for _, tc := range []struct {
		name           string
		err            error
		exit           int
		stdout, stderr string
		elapsed        time.Duration
	}{
		{name: "compile failure", exit: 1, stderr: "main.go:3: undefined: foo", elapsed: 2 * time.Second},
		{name: "rate limit", exit: 1, stderr: "429 rate limit exceeded; unauthenticated retry later", elapsed: time.Second},
		{name: "long run without output", exit: 1, elapsed: 10 * time.Minute},
		{name: "unknown duration without output", exit: 1},
		{name: "success", stdout: "done", elapsed: time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if classifyAgySigninExpiry(tc.err, tc.exit, []byte(tc.stdout), []byte(tc.stderr), tc.elapsed) {
				t.Fatalf("ordinary agy failure classified as a sign-in expiry")
			}
		})
	}
}

func TestAgySigninExpiryFailsOverAsProviderLimit(t *testing.T) {
	limited, reason, cooldown := classifyProviderLimit(errors.Join(errAgySigninExpired, errors.New("exit status 1")), marshal.HandIn{}, nil)
	if !limited || reason != agySigninExpiryReason || cooldown != agySigninExpiryCooldown {
		t.Fatalf("classifyProviderLimit = %v %q %v", limited, reason, cooldown)
	}
	if limited, _, _ := classifyProviderLimit(errors.New("exit status 1"), marshal.HandIn{}, nil); limited {
		t.Fatal("ordinary failure classified as a provider limit")
	}
}
