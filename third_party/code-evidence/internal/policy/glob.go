package policy

import (
	"fmt"
	"path"
	"strings"
)

// Glob is a compiled path pattern.
//
// Syntax (paths are slash-separated and relative to the repository root):
//   - a pattern without '/' matches the base name at any depth
//     ("*.pem" == "**/*.pem");
//   - "**" as a whole segment matches zero or more segments;
//   - other segments use path.Match syntax (*, ?, [...]).
type Glob struct {
	src  string
	segs []string
}

// CompileGlob validates and compiles a pattern.
func CompileGlob(p string) (Glob, error) {
	if p == "" {
		return Glob{}, fmt.Errorf("policy: empty glob")
	}
	if strings.HasPrefix(p, "/") {
		p = strings.TrimPrefix(p, "/")
	}
	src := p
	if !strings.Contains(p, "/") {
		p = "**/" + p
	}
	segs := strings.Split(p, "/")
	for _, s := range segs {
		if s == "**" {
			continue
		}
		if strings.Contains(s, "**") {
			return Glob{}, fmt.Errorf("policy: glob %q: '**' must be a whole segment", src)
		}
		if _, err := path.Match(s, ""); err != nil {
			return Glob{}, fmt.Errorf("policy: glob %q: %v", src, err)
		}
	}
	return Glob{src: src, segs: segs}, nil
}

// String returns the pattern as written.
func (g Glob) String() string { return g.src }

// Match reports whether the slash-separated relative path p matches.
func (g Glob) Match(p string) bool {
	return matchSegs(g.segs, strings.Split(p, "/"))
}

func matchSegs(pat, name []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			// Collapse consecutive "**".
			for len(pat) > 1 && pat[1] == "**" {
				pat = pat[1:]
			}
			if len(pat) == 1 {
				return true
			}
			for i := 0; i <= len(name); i++ {
				if matchSegs(pat[1:], name[i:]) {
					return true
				}
			}
			return false
		}
		if len(name) == 0 {
			return false
		}
		ok, _ := path.Match(pat[0], name[0])
		if !ok {
			return false
		}
		pat, name = pat[1:], name[1:]
	}
	return len(name) == 0
}
