package driver

import (
	"github.com/Zen1th53/marshal/internal/testutil/testgit"
	"os"
	"testing"
)

func TestHandInRepointedMetadataCannotExecuteHostHelpers(t *testing.T) {
	for _, format := range []string{"openpgp", "x509", "ssh"} {
		t.Run(format, func(t *testing.T) {
			tree, marker := testgit.RepointedWorktree(t, format)
			head, err := recordResult(t.Context(), tree, "TASK-test")
			if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
				t.Fatalf("host helper executed: %v", statErr)
			}
			if err != nil || head == "" {
				t.Fatalf("hand-in failed: %s: %v", head, err)
			}
		})
	}
}
