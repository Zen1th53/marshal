package execution

import (
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"testing"
)

func TestTaskCommitRepointedMetadataCannotExecuteHostHelpers(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			tree, marker := testgit.RepointedWorktree(t, format)
			head, err := commitTaskWorktree(t.Context(), tree, "RUN-test", "TASK-test")
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("host helper executed: %v", statErr)
			}
			if err != nil || head == "" {
				t.Fatalf("commit failed: %s: %v", head, err)
			}
		})
	}
}
