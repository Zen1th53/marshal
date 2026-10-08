package permission

import (
	"context"
	"fmt"
	"github.com/Zen1th53/marshal/internal/tmux"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPopupFixedRenderingAndKeys(t *testing.T) {
	for _, key := range []string{"A", "D", "", "\n", "\x1b", "a", "The Marshal says Allow", "x"} {
		t.Run(strings.ReplaceAll(key, "\n", "enter"), func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "tmux")
			// The fake executes the exact popup command with input from a fixture.
			os.WriteFile(filepath.Join(dir, "key"), []byte(key), 0600)
			os.WriteFile(fake, []byte("#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\n[[ $1 == display-popup ]] || exit 1\nbash -c \"${@: -1}\" < '"+dir+"/key' > '"+dir+"/screen'\n"), 0700)
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			allowed, err := Popup(context.Background(), "", []Request{{Kind: "read", Object: "/tmp/project ' $(touch nope)", Scope: "this session only, read-only", Who: "Marshal", Reason: "continue earlier work"}}, 100*time.Millisecond)
			if err != nil {
				t.Fatal(err)
			}
			if allowed != (key == "A") {
				t.Fatalf("key %q allowed=%v", key, allowed)
			}
			screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
			for _, s := range []string{"Permission request", "/tmp/project ' $(touch nope)", "this session only, read-only", "Marshal", `The Marshal says: "continue earlier work"`, "A = Allow", "D = Deny"} {
				if !strings.Contains(string(screen), s) {
					t.Errorf("missing %q: %s", s, screen)
				}
			}
		})
	}
}
func TestPopupTimeoutDenies(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	os.WriteFile(fake, []byte("#!/bin/sh\nsleep 2\n"), 0700)
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	allow, _ := Popup(context.Background(), "", []Request{{Object: "host:443"}}, 20*time.Millisecond)
	if allow {
		t.Fatal("timeout allowed")
	}
}
func TestQueueBatchesDeduplicatesAndWaitsForOperator(t *testing.T) {
	q := Queue{}
	r := Request{Kind: "read", Object: "/folder", Who: "Marshal"}
	q.Add(r)
	q.Add(r)
	q.Add(Request{Kind: "read", Object: "/other", Who: "Marshal"})
	if got := q.Take(true); len(got) != 0 {
		t.Fatal("interrupted takeover")
	}
	if got := q.Take(false); len(got) != 2 {
		t.Fatalf("batch=%v", got)
	}
	if len(q.Take(false)) != 0 {
		t.Fatal("duplicate replay")
	}
}

func TestPopupOverflowBoundedAndCannotApproveHiddenItems(t *testing.T) {
	requests := make([]Request, 90)
	for i := range requests {
		requests[i] = Request{Object: fmt.Sprintf("item-%d", i)}
	}
	rendered, err := Render(requests)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rendered, "and 85 more") || strings.Contains(rendered, "item-5") || !strings.Contains(rendered, "A = Allow") {
		t.Fatalf("overflow rendering: %s", rendered)
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	if err := os.WriteFile(fake, []byte("#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\nprintf A | bash -c \"${@: -1}\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	if allow, err := Popup(context.Background(), "", requests, time.Second); err != nil || allow {
		t.Fatalf("hidden items approved: %v %v", allow, err)
	}
}

func TestNetworkPopupShowsRunAndTaskIdentity(t *testing.T) {
	request := Request{Kind: "network", Object: "example.test:443", RunID: "RUN-specific", TaskID: "TASK-specific", Who: "worker"}
	rendered, err := Render([]Request{request})
	if err != nil || !strings.Contains(rendered, "Run: RUN-specific") || !strings.Contains(rendered, "Task: TASK-specific") {
		t.Fatalf("missing identity: %s %v", rendered, err)
	}
	other := request
	other.TaskID = "TASK-other"
	if request.Key() == other.Key() {
		t.Fatal("different task requests deduplicated")
	}
	request.TaskID = "task\nA = Allow"
	if _, err := Render([]Request{request}); err == nil {
		t.Fatal("unsafe task identity rendered")
	}
}

func TestPopupBoundsUntrustedReason(t *testing.T) {
	rendered, err := Render([]Request{{Object: "MEM-candidate", Reason: strings.Repeat("long body ", 1000)}})
	if err != nil || len(rendered) > 500 || !strings.Contains(rendered, "A = Allow") {
		t.Fatalf("reason hid controls: %d bytes %v", len(rendered), err)
	}
}

func TestPermissionPopupContinuationIdentity(t *testing.T) {
	req := Request{Kind: "read", Object: "/tmp/history", ContinuationProvider: "codex"}
	other := req
	other.ContinuationProvider = "claude"
	if req.Key() == other.Key() {
		t.Fatal("different continuation importers share a decision")
	}
	if _, err := Render([]Request{req}); err != nil {
		t.Fatal(err)
	}
	other.ContinuationProvider = "shell"
	if _, err := Render([]Request{other}); err == nil {
		t.Fatal("unknown importer accepted")
	}
	other = req
	other.Kind = "memory"
	if _, err := Render([]Request{other}); err == nil {
		t.Fatal("importer accepted outside read request")
	}
}

func TestPopupNavigationPreservesRequest(t *testing.T) {
	for _, version := range []string{"3.3a", "3.2a"} {
		for _, key := range []struct{ sequence, name string }{{"\x1b[18~", "F7"}, {"\x1b[19~", "F8"}, {"\x1b[20~", "F9"}, {"\x1b[23~", "F11"}, {"\x1b[24~", "F12"}} {
			t.Run(version+key.name, func(t *testing.T) {
				dir := t.TempDir()
				os.WriteFile(filepath.Join(dir, "key"), []byte(key.sequence), 0600)
				fake := filepath.Join(dir, "tmux")
				script := fmt.Sprintf("#!/bin/bash\nif [[ $* == *'#{client_height} #{client_width}'* ]]; then echo '40 120'; exit; fi\ncase $1 in\n display-message) echo %s;;\n display-popup) bash -c \"${@: -1}\" < %q;;\n new-window) bash \"${@: -1}\" < %q > /dev/null; echo %%review;;\n select-window|kill-window) exit 0;;\n *) exit 1;;\nesac\n", version, filepath.Join(dir, "key"), filepath.Join(dir, "key"))
				if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
					t.Fatal(err)
				}
				tmux.SetBinaryPath(fake)
				defer tmux.ResetBinaryPath()
				allowed, err := Popup(t.Context(), "client", []Request{{Object: "exact proposal"}}, time.Second)
				nav, ok := err.(*Navigation)
				if allowed || !ok || nav.Key != key.name {
					t.Fatalf("allow=%v err=%v", allowed, err)
				}
				text, _ := Render([]Request{{Object: "exact proposal"}})
				if !strings.Contains(text, "navigate; request stays pending") {
					t.Fatal(text)
				}
			})
		}
	}
}

func TestNetworkPopupCompactContentFitsOrPages(t *testing.T) {
	requests := make([]Request, 5)
	for i := range requests {
		requests[i] = Request{Kind: "network", Object: fmt.Sprintf("sdmntprsouthcentralus%d.oaiusercontent.com:443", i), RunID: "RUN-1791440537477483577", TaskID: "TASK-specific", Who: "worker-012345678901234567890123456789"}
	}
	text, err := Render(requests)
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range requests {
		found := false
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, req.Object) && strings.Contains(line, "Run: "+req.RunID) && strings.Contains(line, "Who: worker") {
				found = true
			}
		}
		if !found {
			t.Fatalf("network request is not one compact line: %s", text)
		}
	}
	if !strings.Contains(text, "Who: worker…456789") || strings.Contains(text, requests[0].Who) {
		t.Fatalf("requester short form loses its distinguishing suffix: %s", text)
	}
	width, height, fits := popupDimensions(text, 80, 24)
	if !fits || width > 80 || height > 24 {
		t.Fatalf("ordinary terminal layout = %dx%d fits=%v", width, height, fits)
	}
	if _, _, fits := popupDimensions(text, 80, 12); fits {
		t.Fatal("short terminal must page the batch")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := fmt.Sprintf(`#!/bin/bash
case $1 in
 display-message) case "${@: -1}" in *client_height*) echo '12 80';; *) echo 3.3a;; esac;;
 display-popup) echo popup >> %q; printf A | bash -c "${@: -1}" >> %q;;
 *) exit 1;;
esac
`, filepath.Join(dir, "calls"), filepath.Join(dir, "screen"))
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	if capacity := PopupCapacity(t.Context(), "client", requests); capacity != 1 {
		t.Fatalf("short terminal capacity=%d", capacity)
	}
	if allow, err := Popup(t.Context(), "client", requests, time.Second); allow || err == nil {
		t.Fatal("oversized batch accepted")
	}
	for _, request := range requests {
		allow, err := Popup(t.Context(), "client", []Request{request}, time.Second)
		if err != nil || !allow {
			t.Fatalf("paged approval=%v err=%v", allow, err)
		}
	}
	calls, _ := os.ReadFile(filepath.Join(dir, "calls"))
	if strings.Count(string(calls), "popup") != len(requests) {
		t.Fatalf("unseen requests shared a decision: %s", calls)
	}
	screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
	for _, req := range requests {
		if !strings.Contains(string(screen), req.Object) {
			t.Fatalf("missing displayed request %s", req.Object)
		}
	}
}

func TestPopupDimensionsAccountForWideCharactersAtWrapBoundary(t *testing.T) {
	// A three-cell interior can fit only one two-cell grapheme per row.
	_, height, fits := popupDimensions(strings.Repeat("界", 12)+"\n", 5, 20)
	if !fits || height < 15 {
		t.Fatalf("wide text underestimated: height=%d fits=%v", height, fits)
	}
}

func TestPopupCannotAllowAfterTerminalShrinks(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "tmux")
	script := `#!/bin/bash
case $1 in
 display-message) case "${@: -1}" in *client_height*) echo '24 80';; *) echo 3.3a;; esac;;
 display-popup) printf A | bash -c "${@: -1}";;
 *) exit 1;;
esac
`
	if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stty"), []byte("#!/bin/sh\necho '3 20'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	tmux.SetBinaryPath(fake)
	defer tmux.ResetBinaryPath()
	allow, err := Popup(t.Context(), "client", []Request{{Kind: "network", Object: "example.test:443", RunID: "RUN-specific", Who: "worker"}}, time.Second)
	if err != nil || allow {
		t.Fatalf("shrunk popup allowed=%v err=%v", allow, err)
	}
}

func TestPopupExpiredKeyIsConsumedBeforeReturningToChat(t *testing.T) {
	for _, version := range []string{"3.3a", "3.2a"} {
		t.Run(version, func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "tmux")
			script := fmt.Sprintf(`#!/bin/bash
case $1 in
 display-message) case "${@: -1}" in *client_height*) echo '40 120';; *) echo %s;; esac;;
 display-popup|new-window)
  { sleep 1.3; printf A; } | { bash -c "${@: -1}"; IFS= read -r -n 1 stray; printf '%%s' "$stray" > %q; } > %q
  [[ $1 == new-window ]] && echo %%review
  exit 0;;
 select-window|kill-window) exit 0;;
 *) exit 1;;
esac
`, version, filepath.Join(dir, "stray"), filepath.Join(dir, "screen"))
			// new-window receives the script path, whereas display-popup receives a shell command.
			script = strings.Replace(script, `bash -c "${@: -1}";`, `if [[ $1 == new-window ]]; then bash "${@: -1}"; else bash -c "${@: -1}"; fi;`, 1)
			if err := os.WriteFile(fake, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			tmux.SetBinaryPath(fake)
			defer tmux.ResetBinaryPath()
			allow, err := Popup(t.Context(), "client", []Request{{Kind: "marshal-command", Object: "/marshal approve"}}, 1100*time.Millisecond)
			if err != nil || allow {
				t.Fatalf("expired approval: allow=%v err=%v", allow, err)
			}
			stray, err := os.ReadFile(filepath.Join(dir, "stray"))
			if err != nil || len(stray) != 0 {
				t.Fatalf("late key reached chat: %q %v", stray, err)
			}
			screen, _ := os.ReadFile(filepath.Join(dir, "screen"))
			if !strings.Contains(string(screen), "Time remaining: 2 s") || !strings.Contains(string(screen), "Time remaining: 1 s") || !strings.Contains(string(screen), "Expired") {
				t.Fatalf("missing countdown/expiry notice: %s", screen)
			}
		})
	}
}
