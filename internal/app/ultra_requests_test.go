package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/cloud"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestEntitlementRequestApplicationUsesTrustedLocalContextAndExactTarget(t *testing.T) {
	r := runtimeForPlan(t)
	ctx := context.Background()
	client, _ := cloud.NewClient("http://127.0.0.1:18743", "test")
	identity, _ := cloud.NewInstallation()
	requests := cloud.NewRequestStore(t.TempDir())
	service, err := NewULTRARequestService(r, client, identity, "workspace", "cloud-session", requests)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.Target(ctx)
	if err != nil {
		t.Fatal(err)
	}
	e := CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "workspace", TargetID: model.SessionRecoveryDigest(target.Key), IdempotencyKey: "request"}
	if _, err = service.Submit(ctx, e, target); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("anonymous request: %v", err)
	}
	local, err := r.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ctx = local.Context(ctx)
	for _, kind := range []string{"project", "session", "endpoint", "generation", "target", "installation"} {
		t.Run(kind, func(t *testing.T) {
			e2, t2 := e, target
			switch kind {
			case "project":
				e2.ProjectID = "foreign"
			case "session":
				e2.SessionID = "other"
			case "endpoint":
				t2.Key.Endpoint = "http://127.0.0.1:18744"
			case "generation":
				e2.ExpectedVersion = 1
			case "target":
				e2.TargetID = "other"
			case "installation":
				t2.Key.InstallationID = "foreign"
			}
			if _, err := service.Submit(ctx, e2, t2); err == nil {
				t.Fatal("foreign target accepted")
			}
		})
	}
	if _, found, err := service.Read(ctx); err != nil || found {
		t.Fatalf("refusals mutated request state: %v %v", found, err)
	}
}
