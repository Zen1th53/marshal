package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgyHarnessMessageDoesNotClaimSandbox(t *testing.T) {
	w, _ := realControlWorkspace(t, "SESSION-native-agy-probe-message", false)
	dir := t.TempDir()
	stub := `#!/bin/sh
case "$1" in
--version) printf 'native-probe-version\n'; exit 0;;
--help) printf '%s\n' '--print --output-format --model --mode --conversation --sandbox --new-project --disable-slash-commands'; exit 0;;
esac
printf '%s\n' '{"status":"SUCCESS","conversation_id":"native-message","response":"MARSHAL_PROBE_OK"}'
`
	if err := os.WriteFile(filepath.Join(dir, "agy"), []byte(stub), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	text, err := NewCommandHandler(w).Handle(t.Context(), "/harness verify-native agy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "native print") || strings.Contains(text, "in sandbox through proxy") {
		t.Fatalf("native verification misrepresented: %s", text)
	}
}
