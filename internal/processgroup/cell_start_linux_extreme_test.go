//go:build !governed && linux

package processgroup

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExtremeHeldSupervisorSettlesReleasedTreeAfterOwnerCrash(t *testing.T) {
	const ownerEnv = "MARSHAL_HELD_CRASH_OWNER"
	const directoryEnv = "MARSHAL_HELD_CRASH_DIR"
	if os.Getenv(ownerEnv) == "1" {
		// Pdeathsig is bound to the launching Linux thread. Keep this owned
		// fixture's launch thread alive until the deliberate process crash.
		runtime.LockOSThread()
		dir := os.Getenv(directoryEnv)
		cmd := exec.Command("/bin/sh", "-c", `echo $$ > "$MARSHAL_HELD_CRASH_DIR/worker.pid"
sleep 120 & echo $! > "$MARSHAL_HELD_CRASH_DIR/ordinary.pid"
setsid /bin/sh -c 'echo $$ > "$MARSHAL_HELD_CRASH_DIR/escaped.pid"; sleep 120' &
while [ ! -s "$MARSHAL_HELD_CRASH_DIR/escaped.pid" ]; do sleep 0.01; done
echo ready > "$MARSHAL_HELD_CRASH_DIR/child.ready"
wait`)
		release, _, err := WrapHeld(cmd)
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		ref, err := Identify(cmd)
		if err != nil || ref.PID == 0 {
			t.Fatalf("supervisor identity: %+v %v", ref, err)
		}
		raw, _ := json.Marshal(ref)
		if err := os.WriteFile(filepath.Join(dir, "supervisor.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		if err := release(); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "crash.owner")); err == nil {
				os.Exit(77)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid unavailable")
	}
	dir := t.TempDir()
	owner := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestExtremeHeldSupervisorSettlesReleasedTreeAfterOwnerCrash$")
	owner.Env = append(os.Environ(), ownerEnv+"=1", directoryEnv+"="+dir)
	// Do not give descendants an inherited output pipe that could hide the
	// owner's exit while the regression leaves them alive.
	if err := owner.Start(); err != nil {
		t.Fatal(err)
	}
	joined := false
	t.Cleanup(func() {
		if !joined {
			_ = owner.Process.Kill()
			_ = owner.Wait()
		}
		// Even a readiness failure must clean only this fixture's pinned
		// supervisor; it owns and reaps any descendants already released.
		if raw, err := os.ReadFile(filepath.Join(dir, "supervisor.json")); err == nil {
			var ref Reference
			if json.Unmarshal(raw, &ref) == nil && ref.PID > 0 {
				if err := StopReference(ref); err != nil {
					t.Errorf("owned early fixture cleanup: %v", err)
				}
			}
		}
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "child.ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("owned worker tree did not become ready")
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "supervisor.json"))
	if err != nil {
		t.Fatal(err)
	}
	var supervisor Reference
	if err := json.Unmarshal(raw, &supervisor); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := StopReference(supervisor); err != nil {
			t.Errorf("owned fixture cleanup: %v", err)
		}
	})
	refs := []Reference{supervisor}
	for _, name := range []string{"worker", "ordinary", "escaped"} {
		raw, err := os.ReadFile(filepath.Join(dir, name+".pid"))
		if err != nil {
			t.Fatal(err)
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		start, alive, err := processIdentity(pid)
		if err != nil || !alive {
			t.Fatalf("%s fixture not alive: %v %v", name, alive, err)
		}
		refs = append(refs, Reference{PID: pid, Start: start})
	}
	if err := os.WriteFile(filepath.Join(dir, "crash.owner"), []byte("crash"), 0600); err != nil {
		t.Fatal(err)
	}
	err = owner.Wait()
	joined = true
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 77 {
		t.Fatalf("owner did not reach deliberate crash: %v", err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for {
		var live []Reference
		for _, ref := range refs {
			alive, err := RunningReference(ref)
			if err != nil {
				t.Fatal(err)
			}
			if alive {
				live = append(live, ref)
			}
		}
		if len(live) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("owner crash retained pinned supervisor/child/escaped descendant: %+v", live)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
