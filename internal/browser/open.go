// Package browser opens a URL in the user's desktop browser.
package browser

import (
	"context"
	"fmt"
	"os"
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
	command, args := opener(url)
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

// opener names the platform's URL opener by absolute path where the system
// installs it, so a program earlier on PATH cannot stand in for it and receive
// the URL (Marshal hands it one-use owner links).
func opener(url string) (string, []string) {
	switch runtime.GOOS {
	case "darwin":
		return "/usr/bin/open", []string{url}
	case "windows":
		return `C:\Windows\System32\rundll32.exe`, []string{"url.dll,FileProtocolHandler", url}
	}
	for _, path := range []string{"/usr/bin/xdg-open", "/usr/local/bin/xdg-open", "/bin/xdg-open"} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path, []string{url}
		}
	}
	return "xdg-open", []string{url}
}
