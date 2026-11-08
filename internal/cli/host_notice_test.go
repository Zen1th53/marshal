//go:build !governed

package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
)

func TestHostWorkerFirstRunNotice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MARSHAL_CLOUD_ENDPOINT", "off")
	var errs bytes.Buffer
	c := command{root: t.TempDir(), stderr: &errs}
	for i := 0; i < 2; i++ {
		if err := c.hostWorkerNotice(); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Count(errs.String(), "workers run on the host without a sandbox") != 1 {
		t.Fatal(errs.String())
	}
}

func TestCLIRefusesHomeAndRootBeforeStateWrites(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, root := range []string{"/", home} {
		var out, errs bytes.Buffer
		if code := Execute(context.Background(), root, []string{"init"}, nil, &out, &errs); code == 0 || !strings.Contains(errs.String(), "refuses") {
			t.Fatal(code, errs.String())
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("wrote to refused root", err, entries)
	}
}

func TestHostNoticeOnlyForInteractiveCommands(t *testing.T) {
	for _, args := range [][]string{nil, {"tui"}, {"init"}, {"setup"}} {
		if !showsHostNotice(args) {
			t.Fatalf("%v should show the host notice", args)
		}
	}
	for _, args := range [][]string{{"mcp", "relay"}, {"mcp", "serve", "--stdio"}, {"status"}, {"agent"}, {"events"}} {
		if showsHostNotice(args) {
			t.Fatalf("%v must keep its output clean", args)
		}
	}
}
