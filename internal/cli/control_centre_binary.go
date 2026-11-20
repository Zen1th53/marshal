package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/tmux"
)

// controlCentreOutdatedNotice is shown when the running control centre was
// started by an older Marshal and the owner did not restart it.
const controlCentreOutdatedNotice = "Marshal was updated, but this project's control centre still runs the previous version. Type /quit in it and run marshal again to load the update."

// controlCentreBinaryFile records which Marshal binary started the control
// centre, so a later launch can tell it was updated since.
func controlCentreBinaryFile(root string) string {
	return statepath.Path(root, "runtime", "control-centre-binary")
}

func binaryIdentity(exe string) string {
	info, err := os.Stat(exe)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%s %d %d", exe, info.Size(), info.ModTime().UnixNano())
}

func recordControlCentreBinary(root, exe string) {
	identity := binaryIdentity(exe)
	if identity == "" {
		return
	}
	path := controlCentreBinaryFile(root)
	if err := os.MkdirAll(statepath.Path(root, "runtime"), 0o700); err != nil {
		return
	}
	_ = os.WriteFile(path, []byte(identity+"\n"), 0o600)
}

// controlCentreOutdated reports whether the running control centre was
// started by a different build of Marshal than this one. An unknown start
// (no record, from before this check existed) is not called outdated.
func controlCentreOutdated(root, exe string) bool {
	data, err := os.ReadFile(controlCentreBinaryFile(root))
	if err != nil {
		return false
	}
	current := binaryIdentity(exe)
	return current != "" && strings.TrimSpace(string(data)) != current
}

// confirmRestartUpdated asks the owner, on the terminal, before replacing a
// running control centre; it may be supervising work.
func (c *command) confirmRestartUpdated() bool {
	fmt.Fprint(c.stderr, "Marshal was updated, but this project's control centre still runs the previous version.\nRestart it now to load the update? Anything the control centre is running stops. [Y/n] ")
	line, err := bufio.NewReader(c.stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes"
}

// olderControlCentreNotice is shown when an older control centre for this
// project runs on another tmux server and the owner keeps it.
const olderControlCentreNotice = "An older control centre for this project is still running on another tmux server; opening it. Type /quit in it and run marshal again to start a new one."

// The tmux seams of olderControlCentreElsewhere; tests replace them.
var (
	olderControlCentreSockets = tmux.OtherServerSockets
	olderControlCentreFind    = tmux.SessionElsewhere
	olderControlCentreKill    = tmux.KillSessionOnServer
	olderControlCentreAttach  = tmux.AttachSessionOnServer
	olderControlCentreHeld    = app.ProjectHeldElsewhere
	// olderControlCentreConfirm asks the owner whether to replace it.
	olderControlCentreConfirm = func(c *command) bool { return c.isInteractiveTerminal() && c.confirmReplaceOlder() }
)

// olderControlCentreElsewhere finds a control centre for this project on a
// tmux server other than MARSHAL's own: started under another TMUX_TMPDIR
// or temporary directory, by an older build or another shell. A new session
// beside it cannot own the project (the older one holds its lifecycle), so
// the launch opened only a bare chat and no Board. The owner is asked to
// replace it; on yes it is stopped and attached is false, so the caller
// starts the new one. Otherwise the terminal attaches to the older one and
// attached is true, with the attach's result.
func (c *command) olderControlCentreElsewhere(ctx context.Context, root, session string) (attached bool, err error) {
	socket := olderControlCentreFind(ctx, session, olderControlCentreSockets())
	if socket == "" {
		return false, nil
	}
	if olderControlCentreConfirm(c) {
		if err := olderControlCentreKill(ctx, socket, session); err != nil {
			fmt.Fprintf(c.stderr, "Could not stop the older control centre: %v\n", err)
		} else {
			// The old control centre lets go of the project as it exits.
			for deadline := time.Now().Add(5 * time.Second); olderControlCentreHeld(root) && time.Now().Before(deadline); {
				time.Sleep(100 * time.Millisecond)
			}
			return false, nil
		}
	}
	fmt.Fprintln(c.stderr, olderControlCentreNotice)
	return true, tuiChildResult(root, olderControlCentreAttach(ctx, socket, session, c.stdin, c.stdout, c.stderr))
}

// confirmReplaceOlder asks the owner, on the terminal, before stopping a
// control centre on another tmux server; it may be supervising work.
func (c *command) confirmReplaceOlder() bool {
	fmt.Fprint(c.stderr, "An older control centre for this project is still running on another tmux server, so a new one cannot show the Board.\nReplace it now? Anything it is running stops. [Y/n] ")
	line, err := bufio.NewReader(c.stdin).ReadString('\n')
	if err != nil && line == "" {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "" || answer == "y" || answer == "yes"
}
