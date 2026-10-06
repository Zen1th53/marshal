package app

import (
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"testing"
)

func TestRuntimeCommitRepointedMetadataCannotExecuteHostHelpers(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			tree, marker := testgit.RepointedWorktree(t, format)
			err := commitTaskChanges(t.Context(), tree, "TASK-test")
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("host helper executed: %v", statErr)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeMergeRepointedMetadataCannotExecuteHostHelpers(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			tree, marker := testgit.RepointedWorktree(t, format)
			if err := commitTaskChanges(t.Context(), tree, "TASK-test"); err != nil {
				t.Fatal(err)
			}
			commit := testgit.SignedCommit(t, tree, format)
			_, err := gitMarshal(t.Context(), tree, "merge", "--no-ff", "--no-edit", commit)
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("host merge helper executed: %v", statErr)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
