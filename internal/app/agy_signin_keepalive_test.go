//go:build governed

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgySigninKeepaliveCommunityIsNoop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	callsPath := filepath.Join(home, "agy-calls.log")
	script := "#!/bin/sh\nprintf '%s\\n' \"$0 $*\" >> \"" + callsPath + "\"\nprintf '%s\\n' 'ok'\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bin, "agy"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MARSHAL_PROVIDER_PATH_ONLY", "1")

	r := openNetpolRuntime(t)
	r.keepAgySigninFresh(t.Context())
	r.refreshAgySigninBeforeDispatch(t.Context())
	r.startAgySigninKeepaliveLoop(t.Context())

	if data, err := os.ReadFile(callsPath); err == nil && strings.TrimSpace(string(data)) != "" {
		t.Fatalf("Community invoked agy for a sign-in refresh: %s", data)
	}
	history, err := r.store.ListEvents(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		switch event.Type {
		case "AGY_SIGNIN_REFRESHED", "AGY_SIGNIN_REFRESH_FAILED", "AGY_SIGNIN_EXPIRED":
			t.Fatalf("Community recorded an agy sign-in event: %s", event.Type)
		}
	}
}
