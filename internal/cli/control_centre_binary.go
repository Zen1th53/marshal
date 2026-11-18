package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/Zen1th53/marshal/internal/statepath"
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
