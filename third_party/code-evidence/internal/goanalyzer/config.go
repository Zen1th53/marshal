package goanalyzer

import (
	"fmt"
	"go/build/constraint"
	"path"
	"slices"
	"strconv"
	"strings"
)

// Config is a named build configuration. Tier-1 analysis evaluates file
// membership for each configuration from file names and build constraints
// only; it never runs the go command.
type Config struct {
	Name      string   `json:"name"`
	GOOS      string   `json:"goos"`
	GOARCH    string   `json:"goarch"`
	Tags      []string `json:"tags,omitempty"`
	Cgo       bool     `json:"cgo"`
	Tests     bool     `json:"tests"`
	GoVersion string   `json:"go_version"` // e.g. "go1.25"
}

// DefaultGoVersion is the language version assumed for release tags.
const DefaultGoVersion = "go1.25"

// DefaultConfigs is used when the caller names no configuration.
func DefaultConfigs() []Config {
	return []Config{{Name: "linux-amd64", GOOS: "linux", GOARCH: "amd64", Cgo: true, Tests: true, GoVersion: DefaultGoVersion}}
}

// ParseConfig parses "name=goos/goarch[,opt...]" where opt is a build tag,
// "cgo", "nocgo", "notests" or "goX.Y". Example: "pro=darwin/arm64,pro,nocgo".
func ParseConfig(s string) (Config, error) {
	name, rest, ok := strings.Cut(s, "=")
	if !ok || name == "" || rest == "" {
		return Config{}, fmt.Errorf("config %q: want name=goos/goarch[,opt...]", s)
	}
	parts := strings.Split(rest, ",")
	osArch := strings.SplitN(parts[0], "/", 2)
	if len(osArch) != 2 || !knownOS[osArch[0]] || !knownArch[osArch[1]] {
		return Config{}, fmt.Errorf("config %q: unknown goos/goarch %q", s, parts[0])
	}
	c := Config{Name: name, GOOS: osArch[0], GOARCH: osArch[1], Cgo: true, Tests: true, GoVersion: DefaultGoVersion}
	for _, o := range parts[1:] {
		switch {
		case o == "cgo":
			c.Cgo = true
		case o == "nocgo":
			c.Cgo = false
		case o == "notests":
			c.Tests = false
		case strings.HasPrefix(o, "go1."):
			if _, err := strconv.Atoi(strings.TrimPrefix(o, "go1.")); err != nil {
				return Config{}, fmt.Errorf("config %q: bad go version %q", s, o)
			}
			c.GoVersion = o
		case o == "":
		default:
			if !validTag(o) {
				return Config{}, fmt.Errorf("config %q: bad tag %q", s, o)
			}
			c.Tags = append(c.Tags, o)
		}
	}
	slices.Sort(c.Tags)
	c.Tags = slices.Compact(c.Tags)
	return c, nil
}

func validTag(t string) bool {
	for _, r := range t {
		if !(r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z') {
			return false
		}
	}
	return t != ""
}

// String renders the config in ParseConfig syntax.
func (c Config) String() string {
	var b strings.Builder
	b.WriteString(c.Name + "=" + c.GOOS + "/" + c.GOARCH)
	if c.Cgo {
		b.WriteString(",cgo")
	} else {
		b.WriteString(",nocgo")
	}
	if !c.Tests {
		b.WriteString(",notests")
	}
	if c.GoVersion != "" {
		b.WriteString("," + c.GoVersion)
	}
	for _, t := range c.Tags {
		b.WriteString("," + t)
	}
	return b.String()
}

func (c Config) minor() int {
	n, err := strconv.Atoi(strings.TrimPrefix(c.GoVersion, "go1."))
	if err != nil {
		n, _ = strconv.Atoi(strings.TrimPrefix(DefaultGoVersion, "go1."))
	}
	return n
}

// tagOK mirrors go/build's tag semantics for tier-1 evaluation.
func (c Config) tagOK(tag string) bool {
	switch {
	case tag == c.GOOS || tag == c.GOARCH || tag == "gc":
		return true
	case tag == "unix" && unixOS[c.GOOS]:
		return true
	case tag == "cgo" && c.Cgo:
		return true
	case tag == "linux" && c.GOOS == "android":
		return true
	case tag == "solaris" && c.GOOS == "illumos":
		return true
	case tag == "darwin" && c.GOOS == "ios":
		return true
	case strings.HasPrefix(tag, "go1."):
		n, err := strconv.Atoi(strings.TrimPrefix(tag, "go1."))
		return err == nil && n <= c.minor()
	}
	return slices.Contains(c.Tags, tag)
}

// nameOK applies the _GOOS / _GOARCH / _GOOS_GOARCH file-name rules.
func (c Config) nameOK(file string) bool {
	name := strings.TrimSuffix(path.Base(file), ".go")
	name = strings.TrimSuffix(name, "_test")
	i := strings.Index(name, "_")
	if i < 0 {
		return true
	}
	l := strings.Split(name[i:], "_")
	n := len(l)
	if n >= 2 && knownOS[l[n-2]] && knownArch[l[n-1]] {
		return c.tagOK(l[n-2]) && c.tagOK(l[n-1])
	}
	if n >= 1 && (knownOS[l[n-1]] || knownArch[l[n-1]]) {
		return c.tagOK(l[n-1])
	}
	return true
}

// HeaderConstraint extracts the build constraint from a Go file header:
// comment lines before the first non-comment line. //go:build wins over
// // +build lines, as in go/build.
func HeaderConstraint(src []byte) (constraint.Expr, string, error) {
	var plus []string
	inBlock := false
	for len(src) > 0 {
		var line []byte
		if i := indexByte(src, '\n'); i >= 0 {
			line, src = src[:i], src[i+1:]
		} else {
			line, src = src, nil
		}
		t := strings.TrimSpace(string(line))
		if inBlock {
			if strings.Contains(t, "*/") {
				inBlock = false
			}
			continue
		}
		switch {
		case t == "":
			continue
		case strings.HasPrefix(t, "/*"):
			if !strings.Contains(t[2:], "*/") {
				inBlock = true
			}
			continue
		case !strings.HasPrefix(t, "//"):
			src = nil
			continue
		}
		if constraint.IsGoBuild(t) {
			x, err := constraint.Parse(t)
			if err != nil {
				return nil, t, err
			}
			return x, x.String(), nil
		}
		if constraint.IsPlusBuild(t) {
			plus = append(plus, t)
		}
	}
	if len(plus) == 0 {
		return nil, "", nil
	}
	var expr constraint.Expr
	for _, p := range plus {
		x, err := constraint.Parse(p)
		if err != nil {
			return nil, p, err
		}
		if expr == nil {
			expr = x
		} else {
			expr = &constraint.AndExpr{X: expr, Y: x}
		}
	}
	return expr, expr.String(), nil
}

func indexByte(b []byte, c byte) int {
	for i, x := range b {
		if x == c {
			return i
		}
	}
	return -1
}

// Lists from go/build's syslist (GOOS and GOARCH values known to go/build).
var knownOS = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js", "linux", "nacl", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows", "zos")

var unixOS = set("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "linux", "netbsd", "openbsd", "solaris")

var knownArch = set("386", "amd64", "amd64p32", "arm", "armbe", "arm64", "arm64be", "loong64", "mips", "mipsle", "mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm")

func set(xs ...string) map[string]bool {
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}
