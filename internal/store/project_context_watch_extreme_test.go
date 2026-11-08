//go:build !governed

package store

import (
	"testing"
	"time"
)

func TestProjectContextWatchSnapshotJoinsReceiptMemoryAndAuthority(t *testing.T) {
	for _, scenario := range []string{"current-single-connection", "role-revoked", "memory-acl-revoked", "missing-authority", "foreign-receipt", "changed-selection"} {
		t.Run(scenario, func(t *testing.T) {
			s, a, req := authorityFixture(t)
			s.db.SetMaxOpenConns(1)
			authorityRole(t, s, a, "watch-role", a.Binding.CanonicalTaskID)
			observed, err := s.ReadProjectContextAuthority(t.Context(), req, a.Now)
			if err != nil {
				t.Fatal(err)
			}
			a.ExpectedAuthority = &observed
			receipt, err := s.CommitProjectContextAdmission(t.Context(), a)
			if err != nil {
				t.Fatal(err)
			}
			a.Binding = receipt.Binding
			switch scenario {
			case "role-revoked":
				err = s.RevokeRoleBinding(t.Context(), "watch-role", a.Now.Add(time.Second))
			case "memory-acl-revoked":
				_, err = s.db.ExecContext(t.Context(), `UPDATE memory_records_v2 SET acl_scope='foreign' WHERE memory_id=?`, receipt.Loaded[0].ID)
			case "missing-authority":
				a.ExpectedAuthority = nil
			case "foreign-receipt":
				receipt.ID = "foreign"
			case "changed-selection":
				a.Selection.CanonicalTaskID = "foreign"
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := s.ReadProjectContextWatchSnapshot(t.Context(), a, receipt.ID, a.Now.Add(2*time.Second))
			if scenario == "current-single-connection" {
				if err != nil || got.Receipt.ID != receipt.ID || got.Snapshot.StateDigest != a.ExpectedStateDigest || got.Authority.Digest != observed.Digest {
					t.Fatal("coherent current snapshot refused", err)
				}
			} else if err == nil {
				t.Fatal("changed required context accepted")
			}
		})
	}
}
