package tui

import (
	"errors"
	"fmt"
)

var errChatMCPBindingMismatch = errors.New("saved MCP conversation belongs to another project, tmux session, provider or pane")

const chatMCPRepairInstructions = "In the chat type /exit, then in the TUI run /marshal chat to resume it with a fresh MCP binding."

// The chat inbox is file-backed so the repair note remains readable even when
// the chat's MCP transport is the part that has failed.
func (w *Workspace) chatMCPRecoveryFailed(root string, cause error) {
	message := fmt.Sprintf("Marshal chat MCP restore failed: %s. Conversation preserved. %s", RedactContent(cause.Error(), nil), chatMCPRepairInstructions)
	w.RecordActivity(message)
	w.noteMCPFailure("The Marshal chat lost its MCP connection. " + chatMCPRepairInstructions)
	view, err := openInboxView(root, "marshal", false)
	if err == nil {
		err = view.write("\n## runtime · chat MCP recovery\n\n" + message + "\n")
	}
	if err != nil {
		w.RecordActivity("Marshal chat recovery note could not be written: " + RedactContent(err.Error(), nil))
	}
}
