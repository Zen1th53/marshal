// Package browser opens a URL in the user's desktop browser.
package browser

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"time"
)

// Open launches the platform's URL opener (open on macOS, rundll32 on
// Windows, xdg-open elsewhere) for url. The browser is not bound to ctx: it
// keeps running after the caller exits.
func Open(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := "xdg-open"
	args := []string{url}
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "windows":
		command = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	}
	process := exec.Command(command, args...)
	if err := process.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	// The user's desktop browser has its own lifetime. Reap its launcher without
	// terminating that browser when the MARSHAL workspace closes. A launcher
	// that fails at once (no browser for the link) is a failed open; one still
	// running after openWait is taken as opened.
	exited := make(chan error, 1)
	go func() { exited <- process.Wait() }()
	select {
	case err := <-exited:
		if err != nil {
			return fmt.Errorf("open browser: %w", err)
		}
	case <-time.After(openWait):
	case <-ctx.Done():
	}
	return nil
}

// openWait is how long a browser launcher may take to fail.
const openWait = 3 * time.Second
