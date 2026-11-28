package tui

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// /marshal web copy puts a fresh one-use Web Board link on the clipboard,
// for a browser other than the one the Board opens (on macOS through
// pbcopy, elsewhere through the terminal's OSC 52). The link never goes to
// the footer or Activity on success.

// webLinkCopy is the seam the copy writes through; nil is copyToClipboard.
// It reports whether the clipboard surely holds the text (pbcopy) or the
// text was only sent to the terminal (OSC 52, which has no reply).
var webLinkCopy func(w *Workspace, text string) (sure bool, err error)

func (w *Workspace) marshalWebCopy() (string, error) {
	srv, err := w.webBoardServer()
	if err != nil {
		return "", err
	}
	url, err := srv.Link()
	if err != nil {
		return "", err
	}
	write := webLinkCopy
	if write == nil {
		write = copyToClipboard
	}
	sure, err := write(w, url)
	if err != nil {
		// Shown as /marshal web shows it with no display: the link and
		// what it can do.
		return url + " · Could not reach the clipboard: open this link in a browser on this computer. It works once and can act for you; keep it private.", nil
	}
	if !sure {
		return "Sent a new Web Board link to your terminal's clipboard. If paste finds nothing, run /marshal web to see it (in tmux, set-clipboard must be on).", nil
	}
	return "Copied a new Web Board link to the clipboard. Paste it in a browser on this computer; it works once and can act for you, so keep it private.", nil
}

// copyToClipboard writes text with pbcopy on macOS, and as OSC 52 to the
// terminal elsewhere.
func copyToClipboard(w *Workspace, text string) (bool, error) {
	if runtime.GOOS == "darwin" {
		if path, err := exec.LookPath("pbcopy"); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, path)
			cmd.Stdin = strings.NewReader(text)
			if err := cmd.Run(); err == nil {
				return true, nil
			}
		}
	}
	if w.terminal == nil || w.terminal.out == nil || !w.terminal.IsTerminal() {
		return false, errors.New("no clipboard")
	}
	if _, err := fmt.Fprintf(w.terminal.out, "\x1b]52;c;%s\x07", base64.StdEncoding.EncodeToString([]byte(text))); err != nil {
		return false, err
	}
	return false, nil
}
