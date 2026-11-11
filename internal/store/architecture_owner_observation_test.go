// These fixtures exercise the edition-specific native owner-browser receiver.
package store

import (
	"bytes"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/Zen1th53/marshal/internal/architecturecodec"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/projectid"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
)

// registeredArchitectureOwner models an existing out-of-band enrollment. The
// receiver deliberately has no provisioning API; registration stays test-only.
func registeredArchitectureOwner(t *testing.T) (*Store, SignedArchitectureReceipt, json.RawMessage) {
	t.Helper()
	ctx := t.Context()
	root := testgit.New(t).Path()
	private := filepath.Join(root, statepath.DirName)
	binding, err := projectid.Adopt(ctx, nil, root, private)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, filepath.Join(private, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.InitProject(ctx, model.Project{ID: "PROJECT-local", Repository: root, DefaultBranch: "main", PackVersion: "6.0"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MigrateArchitectureReceiver(ctx); err != nil {
		t.Fatal(err)
	}
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{17}, ed25519.SeedSize))
	g := ArchitectureTargetGeneration{
		ArchitectureTarget: ArchitectureTarget{ProjectID: string(binding.ID), Root: root, StorePath: filepath.Join(private, "state.db"), SourceRoot: root, Protocol: architecturecodec.Version, Policy: "community"},
		Generation:         1, GitDirectory: filepath.Join(root, ".git"), Issuer: key.Public().(ed25519.PublicKey), EnrollmentID: "owner-enrollment",
	}
	payload, err := architectureSignedBytes("marshal.architecture.enrollment.v1", g)
	if err != nil {
		t.Fatal(err)
	}
	enrollment := SignedArchitectureEnrollment{Target: g, Signature: ed25519.Sign(key, payload)}
	if err := enrollment.Verify(key.Public().(ed25519.PublicKey)); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(enrollment)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO architecture_owner_namespaces VALUES(?,?)", g.ProjectID, "PROJECT-local"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO architecture_target_generations VALUES(?,?,?,?)", "PROJECT-local", g.Generation, g.EnrollmentID, raw); err != nil {
		t.Fatal(err)
	}
	fact := json.RawMessage(`{"id":"api","name":"API","responsibility":"Serve requests","technology":"Go"}`)
	graph := json.RawMessage(`{"title":"Service","purpose":"Serve requests","components":[` + string(fact) + `],"connections":[],"constraints":[]}`)
	digest, err := architecturecodec.CanonicalDigest(graph)
	if err != nil {
		t.Fatal(err)
	}
	layouts, err := architecturecodec.GenericLayouts(graph)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := json.Marshal(layouts)
	if err != nil {
		t.Fatal(err)
	}
	_, _, layoutDigest, err := architecturecodec.BindStoredLayouts(graph, layout)
	if err != nil {
		t.Fatal(err)
	}
	ui := map[string]any{"language": "English", "strings": architecturecodec.EnglishUIStrings(), "components": map[string]any{"api": map[string]any{"name": "API", "responsibility": "Serve requests"}}, "connections": map[string]any{}}
	uiRaw, err := json.Marshal(ui)
	if err != nil {
		t.Fatal(err)
	}
	uiDigest, err := architecturecodec.CanonicalDigest(uiRaw)
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]any{"protocol_version": architecturecodec.Version, "project": g.ProjectID, "architecture_id": "architecture-1", "revision": 1, "digest": digest, "architecture": graph, "layout_digest": layoutDigest, "approved": false, "language": "English", "ui": ui, "ui_digest": uiDigest, "ui_architecture_digest": digest, "comments": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	review := filepath.Join(private, "architecture", "review")
	if err := os.MkdirAll(filepath.Join(review, "layouts"), 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string][]byte{
		filepath.Join(review, "state.json"):                    state,
		filepath.Join(review, "layouts", layoutDigest+".json"): layout,
		filepath.Join(root, "bound.go"):                        []byte("package service\n"),
	} {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	c, _, err := s.ObserveRegisteredArchitectureOwner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	c.Binding.ReceiptID = "owner-receipt"
	c.Binding.Origin = "owner_browser"
	c.SessionID = g.EnrollmentID
	c.MessageID = "owner-message"
	c.Message = "Accept this exact architecture"
	BindArchitectureChallenge(&c)
	payload, err = architectureSignedBytes("marshal.architecture.receipt.v1", c)
	if err != nil {
		t.Fatal(err)
	}
	return s, SignedArchitectureReceipt{Challenge: c, Signature: ed25519.Sign(key, payload)}, fact
}

func TestCommunityArchitectureReceiptAcceptsRegisteredOwnerBrowser(t *testing.T) {
	s, receipt, fact := registeredArchitectureOwner(t)
	accepted, err := s.ImportObservedArchitectureReceipt(t.Context(), receipt)
	if err != nil || accepted != receipt.Challenge.Binding {
		t.Fatalf("registered owner browser receipt: %+v %v", accepted, err)
	}
	if !s.ArchitectureFactAvailable(t.Context(), accepted.ProjectID, fact) {
		t.Fatal("accepted graph fact unavailable")
	}
	if s.ArchitectureFactAvailable(t.Context(), accepted.ProjectID, json.RawMessage(`{"id":"invented"}`)) {
		t.Fatal("unsigned fact available")
	}
	for _, table := range []string{"architecture_accepted_revisions", "architecture_owner_audit", "architecture_fact_bindings"} {
		if got := queryInt(t, s.db, "SELECT count(*) FROM "+table); got != 1 {
			t.Fatalf("%s rows=%d, want 1", table, got)
		}
	}
}

func TestCommunityArchitectureReceiptFactsRejectStaleBindings(t *testing.T) {
	for _, change := range []string{"bytes", "mode", "review revision"} {
		t.Run(change, func(t *testing.T) {
			s, receipt, fact := registeredArchitectureOwner(t)
			accepted, err := s.ImportObservedArchitectureReceipt(t.Context(), receipt)
			if err != nil {
				t.Fatal(err)
			}
			if !s.ArchitectureFactAvailable(t.Context(), accepted.ProjectID, fact) {
				t.Fatal("fresh accepted fact unavailable")
			}
			path := filepath.Join(accepted.Root, "bound.go")
			switch change {
			case "bytes":
				err = os.WriteFile(path, []byte("package changed\n"), 0600)
			case "mode":
				err = os.Chmod(path, 0700)
			case "review revision":
				path = filepath.Join(accepted.Root, statepath.DirName, "architecture", "review", "state.json")
				var raw []byte
				raw, err = os.ReadFile(path)
				if err == nil {
					err = os.WriteFile(path, bytes.Replace(raw, []byte(`"revision":1`), []byte(`"revision":2`), 1), 0600)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.ArchitectureFactAvailable(t.Context(), accepted.ProjectID, fact) {
				t.Fatal("stale source or review still credits accepted fact")
			}
		})
	}
}
