//go:build !governed

package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/projectcontext"

	"github.com/Zen1th53/marshal/internal/archconform"
	"github.com/Zen1th53/marshal/internal/marshal"
)

// architectureFindingLimit bounds the return reason.
const architectureFindingLimit = 8

// architectureHandInEdition checks the files a hand-in changed against the
// accepted project contract: a task writes only inside its parts (and the
// parts inside them), and code uses another part only through a declared
// dependency. A newly added production source file with no owning part returns the task. With
// no accepted contract there is nothing to check.
func (s *MarshalService) architectureHandInEdition(ctx context.Context, t marshal.Task, h marshal.HandIn) error {
	accepted, err := s.runtime.store.ReadAcceptedProjectContract(ctx, s.runtime.ProjectContractBinding())
	if err != nil || h.ResultCommit == "" || len(h.FilesTouched) == 0 {
		return nil
	}
	m := archconform.NewMap(accepted.Contract)
	parts := taskArchitectureParts(m, t)
	read := func(p string) (string, bool) {
		text, err := gitMarshal(ctx, s.Repository, "show", h.ResultCommit+":"+p)
		return text, err == nil
	}
	gomod, _ := read("go.mod")
	// A contract that declares no dependency at all says nothing about how
	// parts use each other yet; its imports are reported by
	// marshal_architecture_conformance instead of returning tasks.
	declares := m.DeclaresDependencies()
	var blocking []string
	base := t.BaseCommit
	if base == "" {
		base = h.BaseCommit
	}
	added := map[string]bool{}
	if base != "" {
		names, diffErr := gitMarshal(ctx, s.Repository, "diff", "--no-ext-diff", "--no-textconv", "--no-renames", "--diff-filter=A", "--name-only", "-z", base, h.ResultCommit)
		if diffErr != nil {
			return fmt.Errorf("architecture: cannot determine newly added files: %w", diffErr)
		}
		for _, name := range strings.Split(names, "\x00") {
			added[name] = true
		}
	}

	for _, f := range archconform.Check(m, h.FilesTouched, parts, archconform.GoModule(gomod), read) {
		if f.Kind == "unowned" && added[f.File] && architectureProductionSource(f.File) {
			blocking = append(blocking, f.String()+"; place it inside a declared owning part, or ask the Marshal to revise the architecture")
		}
		if f.Kind == "outside_task" || (f.Kind == "undeclared_dependency" && declares) {
			blocking = append(blocking, f.String())
		}
	}
	if len(blocking) == 0 {
		return nil
	}
	shown := blocking
	if len(shown) > architectureFindingLimit {
		shown = append(append([]string(nil), shown[:architectureFindingLimit]...), fmt.Sprintf("and %d more", len(blocking)-architectureFindingLimit))
	}
	return errors.New("architecture: the code does not match the accepted architecture: " + strings.Join(shown, "; ") +
		". Change the code to fit the architecture; if the architecture itself must change, the Marshal submits a revised project contract and a new part or dependency waits for the owner's Accept in the browser")
}

// taskArchitectureParts are the parts a task builds: its declared
// components, else the parts owning its file scope; nil when neither names
// a part (the task is then not limited by part).
func taskArchitectureParts(m *archconform.Map, t marshal.Task) []string {
	if len(t.Components) > 0 {
		return t.Components
	}
	seen := map[string]bool{}
	var parts []string
	for _, f := range t.Files {
		if owner := m.Owner(f); owner != "" && !seen[owner] {
			seen[owner] = true
			parts = append(parts, owner)
		}
	}
	return parts
}

// ArchitectureConformance is how a whole commit compares with the accepted
// architecture.
type ArchitectureConformance struct {
	Commit               string                `json:"commit"`
	Contract             string                `json:"contract"`
	DeclaresDependencies bool                  `json:"declares_dependencies"`
	Findings             []archconform.Finding `json:"findings"`
	MoreFindings         int                   `json:"more_findings,omitempty"`
	UnimplementedParts   []string              `json:"unimplemented_parts"`
	UnownedFiles         int                   `json:"unowned_files"`
	UnownedSample        []string              `json:"unowned_sample,omitempty"`
	CheckedFiles         int                   `json:"checked_files"`
	Truncated            bool                  `json:"truncated,omitempty"`
}

const (
	conformanceFileLimit    = 4000
	conformanceFindingLimit = 30
	conformanceByteLimit    = 512 << 10
)

var conformanceCode = map[string]bool{".go": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true, ".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".vue": true, ".svelte": true, ".py": true}

// ArchitectureConformance checks every tracked file at a commit: parts with
// no file at all, files that belong to no part, and code that uses another
// part without a declared dependency. It reads Git objects only.
func (r *Runtime) ArchitectureConformance(ctx context.Context, commit string) (ArchitectureConformance, error) {
	var out ArchitectureConformance
	if r == nil || r.store == nil {
		return out, errors.New("no project runtime")
	}
	accepted, err := r.store.ReadAcceptedProjectContract(ctx, r.ProjectContractBinding())
	if err != nil {
		return out, errors.New("no accepted architecture to check against")
	}
	if commit == "" {
		commit = "HEAD"
	}
	if !commitArgument.MatchString(commit) || strings.Contains(commit, "..") {
		return out, errors.New("commit must be a commit id, branch, tag or HEAD expression")
	}
	root := r.ProjectRoot()
	sha, err := gitMarshal(ctx, root, "rev-parse", "--verify", "--end-of-options", commit+"^{commit}")
	if err != nil {
		return out, errors.New("no such commit in this repository")
	}
	listing, err := gitMarshal(ctx, root, "ls-tree", "-r", "--name-only", "-z", sha)
	if err != nil {
		return out, err
	}
	files := strings.Split(strings.TrimRight(listing, "\x00"), "\x00")
	m := archconform.NewMap(accepted.Contract)
	out.Commit, out.Contract, out.DeclaresDependencies = sha, fmt.Sprintf("%s rev %d", accepted.Contract.ID, accepted.Contract.Revision), m.DeclaresDependencies()
	used := map[string]bool{}
	var code []string
	for _, f := range files {
		if f == "" {
			continue
		}
		owner := m.Owner(f)
		if owner == "" {
			out.UnownedFiles++
			if len(out.UnownedSample) < 12 {
				out.UnownedSample = append(out.UnownedSample, f)
			}
			continue
		}
		for _, x := range ancestorsOf(accepted.Contract, owner) {
			used[x] = true
		}
		if conformanceCode[pathExt(f)] {
			code = append(code, f)
		}
	}
	for _, v := range accepted.Contract.Components {
		if v.Removed {
			continue
		}
		if !used[v.ID] {
			out.UnimplementedParts = append(out.UnimplementedParts, v.ID)
		}
	}
	if len(code) > conformanceFileLimit {
		code, out.Truncated = code[:conformanceFileLimit], true
	}
	texts, err := r.gitBlobs(ctx, root, sha, code)
	if err != nil {
		return out, err
	}
	gomod, _ := gitMarshal(ctx, root, "show", sha+":go.mod")
	read := func(p string) (string, bool) { t, ok := texts[p]; return t, ok }
	all := archconform.Check(m, code, nil, archconform.GoModule(gomod), read)
	out.CheckedFiles = len(code)
	out.Findings = []archconform.Finding{}
	for _, f := range all {
		if f.Kind != "undeclared_dependency" {
			continue
		}
		if len(out.Findings) == conformanceFindingLimit {
			out.MoreFindings++
			continue
		}
		out.Findings = append(out.Findings, f)
	}
	return out, nil
}

// gitBlobs reads files at a commit in one git cat-file process, skipping
// files over the byte limit.
func (r *Runtime) gitBlobs(ctx context.Context, dir, commit string, files []string) (map[string]string, error) {
	texts := map[string]string{}
	if len(files) == 0 {
		return texts, nil
	}
	cmd, err := hostgit.Command(ctx, dir, "cat-file", "--batch")
	if err != nil {
		return nil, err
	}
	var input strings.Builder
	for _, f := range files {
		input.WriteString(commit + ":" + f + "\n")
	}
	cmd.Stdin = strings.NewReader(input.String())
	raw, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	rest := raw
	for _, f := range files {
		nl := bytes.IndexByte(rest, '\n')
		if nl < 0 {
			break
		}
		header := strings.Fields(string(rest[:nl]))
		rest = rest[nl+1:]
		if len(header) != 3 || header[1] != "blob" {
			continue
		}
		size, err := strconv.Atoi(header[2])
		if err != nil || size+1 > len(rest) {
			break
		}
		if size <= conformanceByteLimit {
			texts[f] = string(rest[:size])
		}
		rest = rest[size+1:]
	}
	return texts, nil
}

func ancestorsOf(c projectcontext.Contract, id string) []string {
	parent := map[string]string{}
	for _, v := range c.Components {
		parent[v.ID] = v.Parent
	}
	var chain []string
	seen := map[string]bool{}
	for x := id; x != "" && !seen[x]; x = parent[x] {
		chain = append(chain, x)
		seen[x] = true
	}
	return chain
}

func pathExt(p string) string {
	i := strings.LastIndexByte(p, '.')
	if i < 0 || strings.ContainsRune(p[i:], '/') {
		return ""
	}
	return p[i:]
}

// Tests, fixtures and documentation are advisory when unowned. Source extensions
// match the conformance scanner; test naming conventions are excluded.
func architectureProductionSource(file string) bool {
	lower := strings.ToLower(file)
	for _, segment := range strings.Split(lower, "/") {
		switch segment {
		case "docs", "test", "tests", "testdata", "__tests__", "fixtures":
			return false
		}
	}
	name := lower[strings.LastIndex(lower, "/")+1:]
	if strings.HasSuffix(name, "_test.go") || strings.HasPrefix(name, "test_") || strings.HasSuffix(name, "_test.py") || strings.Contains(name, ".test.") || strings.Contains(name, ".spec.") {
		return false
	}
	return conformanceCode[pathExt(lower)]
}
