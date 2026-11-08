package goanalyzer

import (
	"bytes"
	"path"
	"strings"
)

// FactsOf returns the (cached) facts of one visible file, or nil when the
// source has no bytes for it.
func FactsOf(src Source, cache *Cache, p string) *FileFacts { return factsOf(src, cache, p) }

// Failed reports whether the file counts as a parse failure for coverage.
func (ff *FileFacts) Failed() bool { return ff.ParseError != "" || ff.constraintErr }

// SubjectParts splits a decl id "<key>#[Recv.]Name" into its package key
// and bare name.
func SubjectParts(id string) (key, name string) {
	key, rest, ok := strings.Cut(id, "#")
	if !ok {
		return "", ""
	}
	if i := strings.LastIndexByte(rest, '.'); i >= 0 {
		rest = rest[i+1:]
	}
	return key, rest
}

// Closure returns the analyzable paths whose facts can influence the
// tier-1 relations (declared_in, callers, callees, references, tests_of,
// method-population deps) of the given subjects, plus the extra paths.
// It is a sound over-approximation derived from how the index resolves
// names:
//
//   - every decl with id "<key>#..." lives in directory key (or key
//     without a "_test" suffix), so those directories are included whole;
//   - every use, member call, method decl or test reference that can
//     match a subject mentions the subject's bare name, so files whose
//     cached facts name it, or (without cached facts) whose bytes contain
//     it, are included (Go identifiers have no escape syntax, so a byte
//     search cannot miss one);
//   - import resolution maps an import path to the last analyzed
//     directory with that path, so every directory that maps to the
//     subject packages' import paths, or to a path imported by a subject
//     package file (callees), is included whole.
//
// go.mod files are always consulted (BuildPartial reads them from src).
func Closure(src Source, cache *Cache, subjects, extra []string) map[string]bool {
	mods := findModules(src)
	var goPaths []string
	byDir := map[string][]string{}
	for _, p := range src.Paths() {
		if Analyzable(p) {
			goPaths = append(goPaths, p)
			d := path.Dir(p)
			byDir[d] = append(byDir[d], p)
		}
	}
	inc := map[string]bool{}
	for _, p := range extra {
		if Analyzable(p) {
			inc[p] = true
		}
	}
	dirs := map[string]bool{}
	names := map[string]bool{}
	for _, id := range subjects {
		key, name := SubjectParts(id)
		if key == "" {
			continue
		}
		dirs[key] = true
		dirs[strings.TrimSuffix(key, "_test")] = true
		if name != "" {
			names[name] = true
		}
	}
	imports := map[string]bool{}
	for d := range dirs {
		if ip := importPathOf(mods, d); ip != "" {
			imports[ip] = true
		}
		for _, p := range byDir[d] {
			inc[p] = true
			if ff := factsOf(src, cache, p); ff != nil {
				for _, im := range ff.Imports {
					imports[im.Path] = true
				}
			}
		}
	}
	if len(imports) > 0 {
		for d, ps := range byDir {
			if imports[importPathOf(mods, d)] {
				for _, p := range ps {
					inc[p] = true
				}
			}
		}
	}
	if len(names) > 0 {
		needles := make([][]byte, 0, len(names))
		for n := range names {
			needles = append(needles, []byte(n))
		}
		for _, p := range goPaths {
			if inc[p] {
				continue
			}
			b, ok := src.Bytes(p)
			if !ok {
				continue
			}
			// Cached facts list every decl and use name of the file, which
			// is exactly what the relations can match (a file that fails
			// to parse is never part of an index); otherwise fall back to
			// a byte search.
			if fn, ok := cache.cachedNames(factsKey(src, p, b)); ok {
				for n := range names {
					if fn[n] {
						inc[p] = true
						break
					}
				}
				continue
			}
			for _, n := range needles {
				if bytes.Contains(b, n) {
					inc[p] = true
					break
				}
			}
		}
	}
	return inc
}
