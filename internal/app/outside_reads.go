package app

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Zen1th53/marshal/internal/execution"
)

type outsideReadHintKey struct{}

// withOutsideReadHints carries the governed task's own text (description,
// expected outputs) to the sandbox builder, which may mount absolute folders
// it names read-only. Only the private edition reads the hints.
func withOutsideReadHints(ctx context.Context, texts ...string) context.Context {
	joined := strings.Join(texts, "\n")
	if strings.TrimSpace(joined) == "" {
		return ctx
	}
	return context.WithValue(ctx, outsideReadHintKey{}, joined)
}

// withProcess05OutsideReadHints hints everything the approved task states:
// its description, the goal outcome and constraints it carries, its expected
// outputs, verification obligations and required evidence. A Marshal often
// names the outside path only in the evidence it requires.
func withProcess05OutsideReadHints(ctx context.Context, task execution.TaskExecution, pkg execution.ConstraintPackage) context.Context {
	texts := []string{task.Description, pkg.TaskDescription, pkg.GoalOutcome}
	for _, group := range [][]string{pkg.HardConstraints, pkg.ExpectedOutputs, pkg.VerificationObligations, task.RequiredEvidence} {
		texts = append(texts, group...)
	}
	return withOutsideReadHints(ctx, texts...)
}

func outsideReadHints(ctx context.Context) string {
	text, _ := ctx.Value(outsideReadHintKey{}).(string)
	return text
}

var outsidePathPattern = regexp.MustCompile("(?:^|[\\s\"'`(=:,])((?:~/|/)[^\\s\"'`()<>,;]+)")

// outsidePathCandidates returns the absolute paths a text names, with "~/"
// expanded against home and trailing sentence punctuation removed.
func outsidePathCandidates(text, home string) []string {
	var paths []string
	seen := map[string]bool{}
	for _, match := range outsidePathPattern.FindAllStringSubmatch(text, -1) {
		path := strings.TrimRight(match[1], ".:!?")
		if strings.HasPrefix(path, "~/") {
			if home == "" {
				continue
			}
			path = filepath.Join(home, path[2:])
		}
		path = filepath.Clean(path)
		if path == "/" || seen[path] {
			continue
		}
		seen[path] = true
		paths = append(paths, path)
	}
	return paths
}

// credentialRootNames are the per-user credential and provider state stores
// a worker never sees through an outside read, matching the roots the CLI
// installation mount protects plus the common key, token and browser stores.
var credentialRootNames = []string{
	".codex", ".claude", ".claude.json", ".gemini", ".opencode", ".antigravity", ".config",
	".aws", ".ssh", ".gnupg", ".docker", ".kube", ".password-store", ".pki", ".mozilla",
	".netrc", ".git-credentials", ".vault-token", ".local/share/keyrings",
	".local/share/opencode", ".marshal", ".marshal-extreme",
}

// credentialFileNames are hidden wherever they appear directly inside an
// outside folder.
var credentialFileNames = map[string]bool{
	"auth.json": true, ".credentials.json": true, ".netrc": true, ".git-credentials": true,
	"id_rsa": true, "id_ed25519": true, "id_ecdsa": true, ".vault-token": true, ".env": true,
	"credentials": true, "credentials.json": true, "service-account.json": true,
}

func within(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sameOrWithin(root, candidate string) bool {
	return filepath.Clean(root) == filepath.Clean(candidate) || within(root, candidate)
}

// outsideReadExcluded reports folders an outside read never mounts: the
// filesystem and system roots, HOME itself or its ancestors, anything that
// holds or sits in the project, and credential stores.
func outsideReadExcluded(folder, home, project string) bool {
	for _, system := range []string{"/usr", "/bin", "/lib", "/lib64", "/etc", "/proc", "/sys", "/dev", "/run", "/boot", "/var/run"} {
		if sameOrWithin(system, folder) {
			return true
		}
	}
	if folder == "/" || folder == "/tmp" || folder == "/home" || (home != "" && sameOrWithin(folder, home)) {
		return true
	}
	if project != "" && (sameOrWithin(folder, project) || sameOrWithin(project, folder)) {
		return true
	}
	// MARSHAL's own temporary state (honeypot homes with synthetic
	// credentials, probe worktrees, schema files) is named in evidence a
	// verifier reads; it is never an owner's outside folder.
	if tmp := filepath.Clean(os.TempDir()); within(tmp, folder) {
		rel, _ := filepath.Rel(tmp, folder)
		if strings.HasPrefix(strings.SplitN(rel, string(filepath.Separator), 2)[0], "marshal") {
			return true
		}
	}
	if home != "" {
		for _, name := range credentialRootNames {
			if sameOrWithin(filepath.Join(home, name), folder) {
				return true
			}
		}
	}
	return false
}

// outsideReadMasks lists what to hide inside one mounted folder: credential
// roots below it, MARSHAL state directories, credential-named files and Unix
// sockets among its direct children, and every explicitly denied folder.
func outsideReadMasks(folder, home string, denied []string) (dirs, files []string) {
	add := func(path string, info os.FileInfo) {
		if info.IsDir() {
			dirs = append(dirs, path)
		} else {
			files = append(files, path)
		}
	}
	seen := map[string]bool{}
	consider := func(path string) {
		if seen[path] || !within(folder, path) {
			return
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			// A link is resolved inside the sandbox, where its target is
			// either masked at its own path or not mounted at all.
			return
		}
		seen[path] = true
		add(path, info)
	}
	if home != "" {
		for _, name := range credentialRootNames {
			consider(filepath.Join(home, name))
		}
	}
	for _, path := range denied {
		consider(path)
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return dirs, files
	}
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(folder, name)
		switch {
		case entry.Type()&os.ModeSocket != 0, credentialFileNames[strings.ToLower(name)],
			name == ".marshal" || name == ".marshal-extreme":
			consider(path)
		}
	}
	return dirs, files
}
