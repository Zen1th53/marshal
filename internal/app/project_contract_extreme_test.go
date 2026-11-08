//go:build !governed

package app

import (
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/authz"
)

func TestProjectContractInventoryNeedsOwnerControlAndPolicyAuthority(t *testing.T) {
	r := openNetpolRuntime(t)
	control, err := r.OpenLocalControl(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	worker := authz.Principal{ID: "worker", Role: authz.Role{Name: "worker", Authorities: []authz.Authority{authz.AuthoritySourceWrite}}}
	owner := authz.Principal{ID: "owner", Role: authz.Role{Name: "marshal", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
	for _, test := range []struct {
		name       string
		principal  authz.Principal
		controlled bool
	}{
		{"worker_unbound", worker, false}, {"worker_local_context", worker, true}, {"policy_label_without_control", owner, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := t.Context()
			if test.controlled {
				ctx = control.Context(ctx)
			}
			if _, err := r.InspectProjectContractInventory(ctx, test.principal, "", 1); !errors.Is(err, authz.ErrDenied) {
				t.Fatalf("untrusted inventory inspection reached canonical IDs: %v", err)
			}
		})
	}
	page, err := r.InspectProjectContractInventory(control.Context(t.Context()), owner, "", 1)
	if err != nil || page.CompletenessDigest == "" || len(page.Entries) != 0 {
		t.Fatalf("trusted owner exact metadata inspection failed: %+v %v", page, err)
	}
}
