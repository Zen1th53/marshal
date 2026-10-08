package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/permission"
	"strings"
	"testing"
	"time"
)

func TestAcceptanceCredentialWaitsForOperator(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(map[bool]string{true: "allow", false: "deny"}[allow], func(t *testing.T) {
			r := openNetpolRuntime(t)
			control, err := r.OpenLocalControl(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			queued := make(chan permission.Request, 1)
			r.SetPermissionSink(func(req permission.Request) { queued <- req })
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			done := make(chan bool, 1)
			go func() {
				allowed, err := r.waitCredentialGrant(ctx, "codex", time.Second)
				if err != nil {
					t.Error(err)
				}
				done <- allowed
			}()
			req := <-queued
			select {
			case <-done:
				t.Fatal("pending decision failed immediately")
			case <-time.After(30 * time.Millisecond):
			}
			if err := r.CommandPermission(control.Context(ctx), req, allow, "operator popup"); err != nil {
				t.Fatal(err)
			}
			select {
			case got := <-done:
				if got != allow {
					t.Fatalf("grant=%v want %v", got, allow)
				}
			case <-ctx.Done():
				t.Fatal("decision did not continue automatically")
			}
		})
	}
}
func TestAcceptanceCredentialWaitTimeout(t *testing.T) {
	r := openNetpolRuntime(t)
	r.SetPermissionSink(func(permission.Request) {})
	start := time.Now()
	if allowed, err := r.waitCredentialGrant(t.Context(), "codex", 30*time.Millisecond); err != nil || allowed {
		t.Fatal("timeout allowed")
	}
	if time.Since(start) < 30*time.Millisecond {
		t.Fatal("did not wait for timeout")
	}
}

func TestAcceptanceProviderBrokerContinuesAfterAllow(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "synthetic-acceptance-key")
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	queued := make(chan permission.Request, 1)
	r.SetPermissionSink(func(req permission.Request) { queued <- req })
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		broker, err := r.providerBroker(ctx, "codex", "")
		if err == nil && broker == nil {
			err = fmt.Errorf("missing broker after allow")
		}
		result <- err
	}()
	req := <-queued
	select {
	case err := <-result:
		t.Fatalf("run failed before decision: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	if err := r.CommandPermission(control.Context(ctx), req, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("broker did not continue after A")
	}
}

func TestAcceptanceCredentialUnavailableIsNotDenied(t *testing.T) {
	r := openNetpolRuntime(t)
	if _, err := r.providerBroker(t.Context(), "codex", ""); err == nil || strings.Contains(err.Error(), "credential use denied") {
		t.Fatalf("unavailable decision surface: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := r.providerBroker(ctx, "codex", ""); !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "credential use denied") {
		t.Fatalf("cancelled permission wait: %v", err)
	}
}
