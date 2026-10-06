package tmux

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyTargetsRefused(t *testing.T) {
	ctx := context.Background()
	calls := map[string]func(string) error{
		"kill-pane":      func(s string) error { return KillPane(ctx, s) },
		"capture-pane":   func(s string) error { _, err := CapturePane(ctx, s); return err },
		"kill-window":    func(s string) error { return KillWindow(ctx, s) },
		"respawn-window": func(s string) error { return RespawnWindow(ctx, s, nil) },
		"select-window":  func(s string) error { return SelectWindow(ctx, s) },
		"select-pane":    func(s string) error { return SelectPane(ctx, s) },
		"read-only":      func(s string) error { return SetPaneReadOnly(ctx, s, true) },
		"join-source":    func(s string) error { return JoinPane(ctx, s, "%1", true) },
		"join-target":    func(s string) error { return JoinPane(ctx, "%1", s, true) },
		"break-pane":     func(s string) error { return BreakPane(ctx, s) },
		"bind-key":       func(s string) error { return BindWindowKey(ctx, s, "table", "F11", "select-window", "-t", "@1") },
		"bind-action":    func(s string) error { return BindWindowKey(ctx, "%1", "table", "F11", "select-window", "-t", s) },
		"window-option":  func(s string) error { return SetWindowOption(ctx, s, "remain-on-exit", "on") },
		"pane-status":    func(s string) error { _, _, err := PaneDeadStatus(ctx, s); return err },
		"pane-title":     func(s string) error { return SetPaneTitle(ctx, s, "title") },
		"pane-pid":       func(s string) error { _, _, err := PanePIDAndPGID(ctx, s); return err },
		"window-status":  func(s string) error { return SetWindowStatus(ctx, s, "text") },
		"attach":         func(s string) error { return AttachSession(s, nil, nil, nil) },
	}
	path := filepath.Join(t.TempDir(), "tmux")
	log := path + ".log"
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho invoked >> '"+log+"'\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	for name, call := range calls {
		for _, target := range []string{"", " \t"} {
			t.Run(name+target, func(t *testing.T) {
				if err := call(target); err == nil || !strings.Contains(err.Error(), "target") {
					t.Fatalf("empty target accepted: %v", err)
				}
			})
		}
	}
	if HasSession(ctx, "") {
		t.Fatal("empty session accepted")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("empty target reached tmux: %v", err)
	}
}

func TestUnresolvedPaneTargetsRefused(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tmux")
	log := path + ".log"
	script := "#!/bin/sh\nif [ \"$1\" = display-message ]; then exit 0; fi\necho invoked >> '" + log + "'\nexit 0\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	if err := KillPane(ctx, "missing"); err == nil {
		t.Fatal("unresolved kill accepted")
	}
	if _, err := CapturePane(ctx, "missing"); err == nil {
		t.Fatal("unresolved capture accepted")
	}
	if _, err := os.Stat(log); !os.IsNotExist(err) {
		t.Fatalf("unresolved target reached effect: %v", err)
	}
}

func TestPaneEffectsUseResolvedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tmux")
	log := path + ".log"
	script := "#!/bin/sh\nif [ \"$1\" = display-message ]; then printf '%%7\\n'; exit 0; fi\nprintf '%s\\n' \"$*\" >> '" + log + "'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	SetBinaryPath(path)
	t.Cleanup(ResetBinaryPath)
	if _, err := CapturePane(context.Background(), "session:worker"); err != nil {
		t.Fatal(err)
	}
	if err := KillPane(context.Background(), "session:worker"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "capture-pane -p -S - -t %7\nkill-pane -t %7\n" {
		t.Fatalf("effects did not use immutable identity: %s", data)
	}
}
