package tui

import (
	"context"
)

func (w *Workspace) handleBoardInboxKey(ctx context.Context, event KeyEvent) bool {
	if !w.boardVisible() || w.hostView.Load() || w.memView.Load() || w.provView.Load() || w.verView.Load() || w.secView.Load() || w.planView.Load() || w.archView.Load() || len(w.boardInbox()) == 0 || w.diffViewer.IsOpen() || w.palette.IsOpen() {
		return false
	}
	w.inboxView.Update(w.boardInbox())
	if w.inboxView.Confirming() {
		switch event.Type {
		case KeyEsc:
			w.inboxView.Cancel()
		case KeyBackspace:
			w.inboxView.Backspace()
		case KeyRune:
			if event.Rune == 'x' && !w.inboxView.WantsText() {
				// x cancels a confirmation that reads no text, as the help
				// says; in a typed id or reason it is just a letter.
				w.inboxView.Cancel()
				break
			}
			w.inboxView.Input(string(event.Rune))
		case KeyEnter:
			if d, ok := w.inboxView.Confirm(); ok {
				if err := w.decideInbox(ctx, d); err != nil {
					w.setOutput("Permission decision failed: "+err.Error(), false)
				}
			}
		default:
			return false
		}
		return true
	}
	if w.composing() {
		return false
	}
	switch event.Type {
	case KeyUp:
		w.inboxView.Move(-1)
	case KeyDown:
		w.inboxView.Move(1)
	case KeyEnter:
		w.inboxView.Begin('i')
	case KeyEsc:
		w.inboxView.Cancel()
	case KeyRune:
		switch event.Rune {
		case 'j':
			w.inboxView.Move(1)
		case 'k':
			w.inboxView.Move(-1)
		case 'a', 'A', 'd', 'i', 'r', 'x':
			w.inboxView.Begin(event.Rune)
		default:
			return false
		}
	default:
		return false
	}
	return true
}
