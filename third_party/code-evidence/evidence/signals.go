package evidence

import (
	"bytes"
	"math"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Zen1th53/code-evidence/internal/rank"
)

// Task signals beyond bag-of-terms BM25F (ranker bm25f-hop1/2). Each is a
// pure function of the task text and the visible snapshot. They were
// chosen from the offline oracle's failure analysis (docs/M2-REPORT.md):
//
//   - identifier mentions: a task that spells an identifier exactly
//     ("processMetric", "Gatherers.Gather", `Open`) names a declaration;
//     camelCase splitting alone dilutes it into common words;
//   - scope mentions: a "pkg: subject" or "type(scope): subject" prefix,
//     or a path written in the task, names a package or file;
//   - generated code (the "Code generated ... DO NOT EDIT." header) is
//     demoted: it is not edited by hand.
const (
	identWeight     = 3.0  // × log(1 + decls / decls with that name)
	identMaxDocs    = 24   // ignore names declared more often than this
	scopeDirFactor  = 1.6  // decl in a package the task scopes to
	scopeFileFactor = 1.4  // additionally, decl in a file the task names
	mentionFactor   = 1.25 // decl in a package whose name the task mentions
	generatedFactor = 0.25
)

// identRE matches identifier-like words, optionally qualified (a.B.c).
var identRE = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*`)

// identShaped reports whether a bare word looks like a code identifier
// rather than prose: an inner upper-case letter, an inner underscore or a
// digit (mixedCase, snake_case, v2).
func identShaped(w string) bool {
	if len(w) < 3 {
		return false
	}
	for i, r := range w {
		if i == 0 {
			continue
		}
		if unicode.IsUpper(r) || unicode.IsDigit(r) || (r == '_' && i < len(w)-1) {
			return true
		}
	}
	return false
}

// mention is an identifier the task spells out.
type mention struct {
	qual string // qualifier ("" if none): receiver type or package name
	name string
}

// identMentions extracts identifier mentions from the task. A word counts
// when it is identifier-shaped, qualified (X.Y), written in backticks, or
// followed by "(" (a call).
func identMentions(task string) []mention {
	ticks := map[int]bool{}
	in, start := false, 0
	for i := 0; i < len(task); i++ {
		if task[i] == '`' {
			if in {
				for j := start; j < i; j++ {
					ticks[j] = true
				}
			}
			in, start = !in, i+1
		}
	}
	seen := map[mention]bool{}
	var out []mention
	add := func(m mention) {
		if !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	for _, loc := range identRE.FindAllStringIndex(task, -1) {
		w := task[loc[0]:loc[1]]
		call := loc[1] < len(task) && task[loc[1]] == '('
		quoted := ticks[loc[0]]
		parts := strings.Split(w, ".")
		last := parts[len(parts)-1]
		if len(parts) >= 2 && len(last) >= 2 && !isFileExt(last) {
			add(mention{qual: parts[len(parts)-2], name: last})
		}
		if identShaped(last) || ((quoted || call) && len(last) >= 2) {
			add(mention{name: last})
		}
	}
	return out
}

func isFileExt(s string) bool {
	switch s {
	case "go", "md", "mod", "sum", "txt", "json", "yaml", "yml", "toml", "sh", "proto":
		return true
	}
	return false
}

// conventional-commit or "pkg/path: subject" prefix of the first line.
var scopeRE = regexp.MustCompile(`^\s*(?:[A-Za-z]+\(([^)]{1,80})\)!?|([A-Za-z0-9_./*-]{2,80}(?:\s*,\s*[A-Za-z0-9_./*-]{2,80})*)):\s`)

// pathRE matches path-like words written in the task (a/b, x.go).
var pathRE = regexp.MustCompile(`[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)+|[A-Za-z0-9_-]+\.go\b`)

// commitTypes are conventional-commit types; as a bare prefix
// ("fix: ...") they name no scope.
var commitTypes = map[string]bool{
	"fix": true, "feat": true, "chore": true, "docs": true, "doc": true, "refactor": true, "perf": true,
	"test": true, "tests": true, "style": true, "build": true, "ci": true, "revert": true, "deps": true,
	"release": true, "wip": true, "cleanup": true, "lint": true, "all": true, "misc": true,
}

// taskScopes returns the scopes (package or file paths, or path
// suffixes) a task names explicitly.
func taskScopes(task string) []string {
	first, _, _ := strings.Cut(task, "\n")
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		s = strings.Trim(strings.TrimSpace(s), "./*")
		if len(s) >= 2 && !seen[s] && !commitTypes[strings.ToLower(s)] {
			seen[s] = true
			out = append(out, s)
		}
	}
	if m := scopeRE.FindStringSubmatch(first); m != nil {
		for _, g := range m[1:] {
			for _, s := range strings.Split(g, ",") {
				add(s)
			}
		}
	}
	for _, loc := range pathRE.FindAllStringIndex(task, -1) {
		if strings.HasSuffix(task[:loc[0]], "://") {
			continue // URL
		}
		add(task[loc[0]:loc[1]])
	}
	return out
}

// pathMatches reports whether a slash path equals scope or ends with
// "/"+scope.
func pathMatches(p, scope string) bool {
	return p == scope || strings.HasSuffix(p, "/"+scope)
}

// scopeFactor returns the multiplicative scope boost for a declaration in
// file p (directory dir).
func scopeFactor(scopes []string, dir, p string) float64 {
	f := 1.0
	dirHit, fileHit := false, false
	for _, s := range scopes {
		if strings.HasSuffix(s, ".go") {
			if pathMatches(p, s) {
				fileHit, dirHit = true, true
			}
			continue
		}
		if pathMatches(dir, s) {
			dirHit = true
		}
		if pathMatches(strings.TrimSuffix(p, ".go"), s) {
			fileHit, dirHit = true, true
		}
	}
	if dirHit {
		f *= scopeDirFactor
	}
	if fileHit {
		f *= scopeFileFactor
	}
	return f
}

// mentionedDirs returns the package directories whose last path segment
// equals a task word (case-insensitive), excluding the root.
func mentionedDirs(task string, dirs map[string]bool) map[string]bool {
	words := map[string]bool{}
	for _, w := range strings.FieldsFunc(task, func(r rune) bool {
		return !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')
	}) {
		if len(w) >= 3 {
			words[strings.ToLower(w)] = true
		}
	}
	out := map[string]bool{}
	for d := range dirs {
		if d == "." {
			continue
		}
		base := strings.ToLower(path.Base(d))
		if words[base] && !rank.IsStopWord(base) {
			out[d] = true
		}
	}
	return out
}

// generated reports whether src carries the Go generated-code header
// (https://go.dev/s/generatedcode) before the package clause.
func generated(src []byte) bool {
	head := src
	if i := bytes.Index(src, []byte("\npackage ")); i >= 0 {
		head = src[:i+1]
	} else if bytes.HasPrefix(src, []byte("package ")) {
		return false
	}
	for len(head) > 0 {
		line := head
		if i := bytes.IndexByte(head, '\n'); i >= 0 {
			line, head = head[:i], head[i+1:]
		} else {
			head = nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if bytes.HasPrefix(line, []byte("// Code generated ")) && bytes.HasSuffix(line, []byte(" DO NOT EDIT.")) && utf8.Valid(line) {
			return true
		}
	}
	return false
}

func identScore(nDocs, df int) float64 {
	return identWeight * math.Log(1+float64(nDocs)/float64(df))
}
