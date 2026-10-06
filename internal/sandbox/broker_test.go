package sandbox

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/netpolicy"
)

func TestBrokerWorkerEnvelopeContainsOnlyPublicMaterial(t *testing.T) {
	const secret = "sk-live-host-only-credential-0123456789"
	broker, err := netpolicy.NewCredentialBroker("codex", "", secret)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	roots, err := SystemRootBundle()
	if err != nil {
		t.Fatal(err)
	}
	env, err := broker.Prepare(home, "/home/marshal", roots)
	if err != nil {
		t.Fatal(err)
	}
	spec, err := NewBwrap("/bin/true").Wrap(model.SandboxRequest{Worktree: t.TempDir(), ScratchHome: home, WritableTmpfs: []string{"/home/marshal/.codex"}, ExtraEnv: env}, []string{"/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range append(spec.Args, spec.Env...) {
		if strings.Contains(value, secret) || strings.Contains(value, "PRIVATE KEY") {
			t.Fatal("private credential material in process envelope")
		}
	}
	if !strings.Contains(strings.Join(spec.Env, "\n"), "CODEX_CA_CERTIFICATE=/home/marshal/") {
		t.Fatal("Codex TLS trust missing")
	}
	err = filepath.WalkDir(home, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Contains(data, []byte(secret)) || bytes.Contains(data, []byte("PRIVATE KEY")) {
			t.Fatal("secret or CA key in sandbox HOME")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
