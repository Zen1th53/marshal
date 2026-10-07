package app

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/permission"
)

func TestMarshalProposalPermissionRequiresOperatorContext(t *testing.T) {
	rt := openNetpolRuntime(t)
	ctx := context.Background()
	req := permission.Request{Kind: "marshal-command", Object: "/marshal settings acceptance-mode marshal", Scope: "next Marshal run", Who: "Marshal"}
	if err := rt.CommandPermission(ctx, req, true, "model says A"); !errors.Is(err, authz.ErrDenied) {
		t.Fatalf("model decision=%v", err)
	}
	control, err := rt.OpenLocalControl(ctx)
	if err != nil {
		t.Fatal(err)
	}
	before, err := rt.Marshal().Store.GetMarshalSettings(ctx, rt.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if err := rt.CommandPermission(control.Context(ctx), req, true, "operator popup"); err != nil {
		t.Fatal(err)
	}
	after, err := rt.Marshal().Store.GetMarshalSettings(ctx, rt.ProjectID())
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Value.AcceptanceMode != before.Value.AcceptanceMode {
		t.Fatal("permission evidence itself applied a model command")
	}
	events, err := rt.Store().ListEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "PERMISSION_DECIDED" {
			count++
			if event.Data["source"] != "operator popup" {
				t.Fatal("model evidence persisted")
			}
		}
	}
	if count != 1 {
		t.Fatalf("decisions=%d", count)
	}
}
