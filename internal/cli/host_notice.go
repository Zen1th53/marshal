package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Zen1th53/marshal/internal/tui"
)

func usesProject(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "version", "--version", "-version", "-v", "telemetry", "update", "help", "--help", "-h":
		return false
	}
	return true
}

// showsHostNotice limits the one-time host notice to commands a person runs to
// start or prepare work. Machine-facing subcommands (MCP relays, workers,
// status) keep their output clean.
func showsHostNotice(args []string) bool {
	if len(args) == 0 {
		return true
	}
	switch args[0] {
	case "tui", "init", "setup", "run", "goal":
		return true
	}
	return false
}

func (c *command) hostWorkerNotice() error {
	if tui.Edition() != "Marshal" {
		return nil
	}
	show := true
	defer func() {
		if show {
			fmt.Fprintln(c.stderr, "Marshal workers run on the host without a sandbox, with your account's file access and network access. Use a dedicated project directory and review commands before starting workers.")
		}
	}()
	dir, err := c.machineCloudDir()
	if err != nil {
		return nil
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(dir, "host-workers-notice"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if os.IsExist(err) {
		show = false
		return nil
	}
	if err != nil {
		return nil
	}
	if err := file.Close(); err != nil {
		return nil
	}
	return nil
}
