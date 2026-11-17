// Package archconform checks code against the accepted project architecture:
// every changed file belongs to a part, a task stays inside its parts, and
// code only uses another part the architecture says it depends on. Imports
// are read for Go, TypeScript/JavaScript and Python. It reads source text
// only and never runs anything.
package archconform

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/Zen1th53/marshal/internal/projectcontext"
)

// Finding is one place where code and architecture disagree.
type Finding struct {
	Kind      string `json:"kind"` // unowned, outside_task or undeclared_dependency
	File      string `json:"file"`
	Part      string `json:"part,omitempty"`
	Uses      string `json:"uses,omitempty"`       // the part used without a declared dependency
	Import    string `json:"import,omitempty"`     // the import that uses it
	TaskParts string `json:"task_parts,omitempty"` // for outside_task
}

func (f Finding) String() string {
	switch f.Kind {
	case "unowned":
		return fmt.Sprintf("%s belongs to no part of the architecture", f.File)
	case "outside_task":
		return fmt.Sprintf("%s belongs to part %s, outside this task's parts (%s)", f.File, f.Part, f.TaskParts)
	default:
		return fmt.Sprintf("%s in part %s uses part %s (%s), but the architecture declares no %s → %s dependency", f.File, f.Part, f.Uses, f.Import, f.Part, f.Uses)
	}
}

// Map answers which part owns a path.
type Map struct {
	contract projectcontext.Contract
	parent   map[string]string
	deps     map[string]map[string]bool
}

func NewMap(c projectcontext.Contract) *Map {
	m := &Map{contract: c, parent: map[string]string{}, deps: map[string]map[string]bool{}}
	for _, v := range c.Components {
		m.parent[v.ID] = v.Parent
		m.deps[v.ID] = map[string]bool{}
		for _, d := range v.Dependencies {
			m.deps[v.ID][d] = true
		}
	}
	return m
}

// Owner is the part with the most specific path that contains p, or "".
func (m *Map) Owner(p string) string {
	owner, _ := m.owner(p)
	return owner
}

func (m *Map) owner(p string) (string, int) {
	p = clean(p)
	best, length := "", -1
	for _, v := range m.contract.Components {
		for _, q := range v.Paths {
			q = clean(q)
			if (q == "." || p == q || strings.HasPrefix(p, q+"/")) && len(q) > length {
				best, length = v.ID, len(q)
			}
		}
	}
	return best, length
}

// DeclaresDependencies reports whether any part declares a dependency.
func (m *Map) DeclaresDependencies() bool {
	for _, deps := range m.deps {
		if len(deps) > 0 {
			return true
		}
	}
	return false
}

// importOwner owns an import target; an extension-less module path also
// matches a part declared by its file name, and the most specific match wins.
func (m *Map) importOwner(p string) string {
	best, length := m.owner(p)
	for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".vue", ".svelte", ".py", ".go"} {
		if owner, l := m.owner(p + ext); l > length {
			best, length = owner, l
		}
	}
	return best
}

// ancestors is id followed by the parts that contain it.
func (m *Map) ancestors(id string) []string {
	chain, seen := []string{}, map[string]bool{}
	for x := id; x != "" && !seen[x]; x = m.parent[x] {
		chain = append(chain, x)
		seen[x] = true
	}
	return chain
}

// Contains reports whether container is id or contains it.
func (m *Map) Contains(container, id string) bool {
	for _, x := range m.ancestors(id) {
		if x == container {
			return true
		}
	}
	return false
}

// Allowed reports whether part from may use part to: the same part, one
// inside the other, or a dependency declared by from (or a part containing
// it) on to (or a part containing it).
func (m *Map) Allowed(from, to string) bool {
	if from == to || m.Contains(from, to) || m.Contains(to, from) {
		return true
	}
	for _, a := range m.ancestors(from) {
		for _, b := range m.ancestors(to) {
			if m.deps[a][b] {
				return true
			}
		}
	}
	return false
}

// Source reads a file's text at the checked commit; ok is false when it
// does not exist there (deleted, or not a file).
type Source func(path string) (text string, ok bool)

// Check compares changed files with the architecture. taskParts limits
// where a task may write (with the parts inside them); nil means no task.
// module is the Go module path from go.mod ("" when there is none).
func Check(m *Map, files []string, taskParts []string, module string, read Source) []Finding {
	var findings []Finding
	allowed := func(owner string) bool {
		for _, p := range taskParts {
			if m.Contains(p, owner) {
				return true
			}
		}
		return false
	}
	sorted := append([]string(nil), files...)
	sort.Strings(sorted)
	for _, f := range sorted {
		f = clean(f)
		owner := m.Owner(f)
		if owner == "" {
			findings = append(findings, Finding{Kind: "unowned", File: f})
			continue
		}
		if taskParts != nil && !allowed(owner) {
			findings = append(findings, Finding{Kind: "outside_task", File: f, Part: owner, TaskParts: strings.Join(taskParts, ", ")})
		}
		text, ok := read(f)
		if !ok {
			continue
		}
		seen := map[string]bool{}
		for _, target := range imports(f, text, module) {
			used := m.importOwner(target.path)
			if used == "" || seen[used] || m.Allowed(owner, used) {
				continue
			}
			seen[used] = true
			findings = append(findings, Finding{Kind: "undeclared_dependency", File: f, Part: owner, Uses: used, Import: target.spec})
		}
	}
	return findings
}

type importTarget struct{ spec, path string }

var (
	goImportLine  = regexp.MustCompile(`(?m)^\s*import\s+(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
	goImportBlock = regexp.MustCompile(`(?s)import\s*\((.*?)\)`)
	goBlockLine   = regexp.MustCompile(`(?m)^\s*(?:[A-Za-z_.]+\s+)?"([^"]+)"`)
	jsImport      = regexp.MustCompile(`(?:import|export)\s[^'"]*?from\s*['"]([^'"]+)['"]|import\s*\(\s*['"]([^'"]+)['"]\s*\)|require\s*\(\s*['"]([^'"]+)['"]\s*\)|^\s*import\s+['"]([^'"]+)['"]`)
	pyFrom        = regexp.MustCompile(`(?m)^\s*from\s+(\.*)([A-Za-z_][\w.]*)?\s+import\s`)
	pyImport      = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_][\w.]*(?:\s*,\s*[A-Za-z_][\w.]*)*)`)
)

// imports are the repository paths a file uses, by language. Only imports
// that name repository paths count: the module's own packages for Go,
// relative paths for JavaScript and TypeScript, and dotted module paths for
// Python (resolved as directories from the repository root or relatively).
func imports(file, text, module string) []importTarget {
	dir := path.Dir(file)
	var out []importTarget
	switch ext := path.Ext(file); ext {
	case ".go":
		if module == "" {
			return nil
		}
		var specs []string
		for _, m := range goImportLine.FindAllStringSubmatch(text, -1) {
			specs = append(specs, m[1])
		}
		for _, block := range goImportBlock.FindAllStringSubmatch(text, -1) {
			for _, m := range goBlockLine.FindAllStringSubmatch(block[1], -1) {
				specs = append(specs, m[1])
			}
		}
		for _, s := range specs {
			if s == module {
				out = append(out, importTarget{s, "."})
			} else if rest, ok := strings.CutPrefix(s, module+"/"); ok {
				out = append(out, importTarget{s, rest})
			}
		}
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts", ".vue", ".svelte":
		for _, line := range strings.Split(text, "\n") {
			for _, m := range jsImport.FindAllStringSubmatch(line, -1) {
				spec := m[1] + m[2] + m[3] + m[4]
				if strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") {
					out = append(out, importTarget{spec, clean(path.Join(dir, spec))})
				}
			}
		}
	case ".py":
		for _, m := range pyFrom.FindAllStringSubmatch(text, -1) {
			dots, name := m[1], m[2]
			base := ""
			if dots != "" {
				base = dir
				for i := 1; i < len(dots); i++ {
					base = path.Dir(base)
				}
			}
			out = append(out, importTarget{dots + name, clean(path.Join(base, strings.ReplaceAll(name, ".", "/")))})
		}
		for _, m := range pyImport.FindAllStringSubmatch(text, -1) {
			for _, name := range strings.Split(m[1], ",") {
				name = strings.TrimSpace(name)
				out = append(out, importTarget{name, strings.ReplaceAll(name, ".", "/")})
			}
		}
	}
	return out
}

func clean(p string) string {
	p = path.Clean(strings.TrimPrefix(strings.TrimSpace(p), "./"))
	return strings.TrimSuffix(p, "/")
}

// GoModule reads the module path from go.mod text.
func GoModule(gomod string) string {
	for _, line := range strings.Split(gomod, "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}
