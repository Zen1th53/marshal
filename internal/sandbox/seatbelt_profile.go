package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

type seatbeltReadPath struct {
	Path      string
	Directory bool
}

// Canonical paths and file types are collected before this pure compiler runs.
type seatbeltProfileRequest struct {
	Request     model.SandboxRequest
	Scratch     string
	SystemPaths []string
	ReadPaths   []seatbeltReadPath
}

func seatbeltProfile(input seatbeltProfileRequest) (string, []string, error) {
	var profile strings.Builder
	var parameters []string
	profile.WriteString("(version 1)\n(deny default)\n")
	addPath := func(key, path string) error {
		if err := validSeatbeltPath(path); err != nil {
			return err
		}
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("path is not canonical: %q", path)
		}
		parameters = append(parameters, key+"="+path)
		return nil
	}
	allow := func(key, path, filter string, writable bool) error {
		if err := addPath(key, path); err != nil {
			return err
		}
		fmt.Fprintf(&profile, "(allow file-read* (%s (param %q)))\n", filter, key)
		fmt.Fprintf(&profile, "(allow process-exec (%s (param %q)))\n", filter, key)
		if writable {
			fmt.Fprintf(&profile, "(allow file-write* (subpath (param %q)))\n", key)
		}
		return nil
	}
	if err := allow("WORKTREE", input.Request.Worktree, "subpath", true); err != nil {
		return "", nil, err
	}
	if err := allow("SCRATCH", input.Scratch, "subpath", true); err != nil {
		return "", nil, err
	}
	for i, path := range input.Request.WritableDirs {
		if isSeatbeltProviderState(path) || isForbiddenCredentialPath(path) || seatbeltRuntimeStateScope(input.Request, path) {
			return "", nil, fmt.Errorf("protected writable directory: %q", path)
		}
		if err := allow(fmt.Sprintf("WRITE_%d", i), path, "subpath", true); err != nil {
			return "", nil, err
		}
	}
	for i, path := range input.SystemPaths {
		if err := allow(fmt.Sprintf("SYSTEM_%d", i), path, "subpath", false); err != nil {
			return "", nil, err
		}
	}
	for i, read := range input.ReadPaths {
		if isSeatbeltProviderState(read.Path) || isForbiddenCredentialPath(read.Path) || seatbeltRuntimeStateScope(input.Request, read.Path) {
			return "", nil, fmt.Errorf("protected read-only path: %q", read.Path)
		}
		filter := "literal"
		if read.Directory {
			filter = "subpath"
		}
		if err := allow(fmt.Sprintf("READ_%d", i), read.Path, filter, false); err != nil {
			return "", nil, err
		}
	}
	runtimeFilter := ""
	if input.Request.RuntimeDir != "" {
		if err := addPath("RUNTIME", input.Request.RuntimeDir); err != nil {
			return "", nil, err
		}
		// Runtime worktrees live beneath .marshal/worktrees; only the assigned tree
		// is accessible, never sibling trees, state, logs, or the control socket.
		runtimeFilter = `(subpath (param "RUNTIME"))`
		if pathWithin(input.Request.RuntimeDir, input.Request.Worktree) {
			runtimeFilter = `(require-all (subpath (param "RUNTIME")) (require-not (subpath (param "WORKTREE"))))`
		}
		fmt.Fprintf(&profile, "(deny file-read* file-write* process-exec %s)\n", runtimeFilter)
	}
	// Exclude host provider state and credentials even under an allowed ancestor.
	// Fresh provider storage in SCRATCH is private to this invocation.
	fmt.Fprintf(&profile, "(deny file-read* file-write* process-exec (require-all (regex #\"%s\") (require-not (subpath (param \"SCRATCH\")))))\n", seatbeltProtectedPaths)
	profile.WriteString(`(deny file-read* file-write* process-exec (require-all (regex #"/[.]marshal(/|$)") (require-not (subpath (param "WORKTREE")))))
(allow file-read* (literal "/dev/null") (literal "/dev/zero") (literal "/dev/random") (literal "/dev/urandom") (literal "/dev/tty") (regex #"^/dev/ttys[0-9]+$"))
(allow file-write* (literal "/dev/null") (literal "/dev/tty") (regex #"^/dev/ttys[0-9]+$"))
(allow process-fork)
(allow signal (target same-sandbox))
(allow sysctl-read)
(allow pseudo-tty)
`)
	if input.Request.NetworkAllowed {
		profile.WriteString(`(allow system-socket (socket-domain AF_INET) (socket-domain AF_INET6) (socket-domain AF_UNIX))
(allow network-bind (local ip "*:*"))
(allow network-inbound (local ip "*:*"))
(allow network-outbound (remote ip "*:*"))
`)
		for _, key := range append([]string{"WORKTREE", "SCRATCH"}, seatbeltWriteKeys(input.Request.WritableDirs)...) {
			fmt.Fprintf(&profile, "(allow network-bind (local unix-socket (subpath (param %q))))\n", key)
			fmt.Fprintf(&profile, "(allow network-outbound (remote unix-socket (subpath (param %q))))\n", key)
		}
		if runtimeFilter != "" {
			fmt.Fprintf(&profile, "(deny network-bind (local unix-socket %s))\n", runtimeFilter)
			fmt.Fprintf(&profile, "(deny network-outbound (remote unix-socket %s))\n", runtimeFilter)
		}
		for _, operation := range []struct{ name, direction string }{{"network-bind", "local"}, {"network-outbound", "remote"}} {
			fmt.Fprintf(&profile, "(deny %s (%s unix-socket (require-all (path-regex #\"%s\") (require-not (subpath (param \"SCRATCH\"))))))\n", operation.name, operation.direction, seatbeltProtectedPaths)
		}
	} else {
		profile.WriteString("(deny network*)\n")
	}
	return profile.String(), parameters, nil
}

func validSeatbeltPath(path string) error {
	if path == "" || strings.ContainsAny(path, "\x00\n\r") {
		return fmt.Errorf("empty or malformed path: %q", path)
	}
	return nil
}

func isSeatbeltProviderState(path string) bool {
	lower := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	for _, provider := range []string{"codex", "claude", "opencode", "gemini"} {
		for _, parent := range []string{"/.local/share/", "/.cache/"} {
			if strings.Contains(lower, parent+provider+"/") || strings.HasSuffix(lower, parent+provider) {
				return true
			}
		}
	}
	for current := filepath.Clean(path); current != "/" && current != "."; current = filepath.Dir(current) {
		if isProviderStateDirectory(current) {
			return true
		}
	}
	return false
}

func seatbeltWriteKeys(paths []string) []string {
	keys := make([]string, len(paths))
	for i := range paths {
		keys[i] = fmt.Sprintf("WRITE_%d", i)
	}
	return keys
}

func seatbeltRuntimeStateScope(request model.SandboxRequest, path string) bool {
	if request.RuntimeDir == "" {
		return false
	}
	if path != request.RuntimeDir && !pathWithin(request.RuntimeDir, path) {
		return false
	}
	assignedTree := pathWithin(request.RuntimeDir, request.Worktree) && (path == request.Worktree || pathWithin(request.Worktree, path))
	return !assignedTree
}

const seatbeltProtectedPaths = `(^|/)([.]codex|[.]claude|[.]opencode|[.]gemini|[.]ssh|[.]aws|[.]gnupg|[.]kube)(/|$)|/[.]config/(codex|claude|opencode|gemini)(/|$)|/[.](local/share|cache)/(codex|claude|opencode|gemini)(/|$)|/[.]docker/config[.]json$|/(auth[.]json|[.]netrc|[.]git-credentials|[.]vault-token|id_rsa|id_ed25519)$`
