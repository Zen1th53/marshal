//go:build governed

package tui

import (
	"fmt"

	"github.com/Zen1th53/marshal/internal/chattransport"
)

// startChatPermissionBroker needs the private Unix-socket broker the
// Marshal carries. Without it no provider permission could reach
// the inbox, so the native chat is refused and the tmux dock stays.
func (w *Workspace) startChatPermissionBroker(string) (string, func(), error) {
	return "", nil, fmt.Errorf("%w: this edition has no inbox permission broker for the native chat", chattransport.ErrUnqualified)
}
