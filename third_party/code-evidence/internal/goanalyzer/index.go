package goanalyzer

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Version is the analyzer version (part of the receipt version vector).
const Version = "go-syntactic/1"

// Source supplies visible file bytes. Implementations must only expose
// policy-visible files.
type Source interface {
	Paths() []string
	Bytes(path string) ([]byte, bool)
}

// Coverage summarizes how much of the Go universe was analyzed.
type Coverage struct {
	GoFiles  int  `json:"go_files"`
	Parsed   int  `json:"parsed"`
	Failed   int  `json:"failed"`
	Skipped  int  `json:"skipped"` // visible Go files without captured bytes
	Complete bool `json:"complete"`
}

// Index is the cross-file tier-1 model of a snapshot.
type Index struct {
	Configs []Config
	Files   []*FileFacts // analyzed Go files, sorted by path
	// Member[i][c] reports whether Files[i] is part of Configs[c].
	Member   [][]bool
	Coverage Coverage
	// FailedFiles lists Go files that failed to parse (sorted).
	FailedFiles []string

	partial    bool
	byPath     map[string]int
	modules    []module          // sorted by dir length desc
	importDir  map[string]string // import path -> dir
	pkgDecls   map[string][]DeclRef
	methods    map[string][]DeclRef // method name -> decls
	declByID   map[string][]DeclRef
	usesByEncl map[string][]UseRef
	refsTo     map[string][]UseRef // decl id -> scope-resolved uses
	memberUses map[string][]UseRef // member name -> member call uses
	testsTo    map[string][]DeclRef
	testsName  map[string][]DeclRef
}

type module struct{ dir, path string }

// DeclRef locates a decl in the index.
type DeclRef struct {
	File int
	Decl int
}

// UseRef locates a use in the index.
type UseRef struct {
	File int
	Use  int
}

// Hasher is optionally implemented by a Source that already knows the
// SHA-256 (hex) of each file's bytes, so Build need not re-hash them.
type Hasher interface {
	SHA256(path string) (string, bool)
}

// Cache memoizes per-file facts keyed by (path, content hash). Facts are a
// pure function of path and bytes, so the cache never changes results.
type Cache struct {
	mu    sync.Mutex
	m     map[string]*FileFacts
	names map[string]map[string]bool // key -> identifier names in facts
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{m: map[string]*FileFacts{}, names: map[string]map[string]bool{}}
}

// cachedNames returns the decl and use names of the cached facts for key
// (ok false when the facts are not cached).
func (c *Cache) cachedNames(k string) (map[string]bool, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if n, ok := c.names[k]; ok {
		return n, true
	}
	ff := c.m[k]
	if ff == nil {
		return nil, false
	}
	n := make(map[string]bool, len(ff.Decls)+len(ff.Uses)/2)
	for _, d := range ff.Decls {
		n[d.Name] = true
	}
	for _, u := range ff.Uses {
		n[u.Name] = true
	}
	c.names[k] = n
	return n, true
}

func (c *Cache) get(k string) *FileFacts {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.m[k]
}

func (c *Cache) put(k string, ff *FileFacts) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) > 200000 {
		c.m = map[string]*FileFacts{}
		c.names = map[string]map[string]bool{}
	}
	c.m[k] = ff
}

// Analyzable reports whether a visible path is part of the Go analysis
// universe: .go files outside testdata, vendor and _/. directories, with
// names not starting with _ or '.'.
func Analyzable(p string) bool {
	if !strings.HasSuffix(p, ".go") {
		return false
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg[0] == '_' || seg[0] == '.' || seg == "testdata" || seg == "vendor" {
			return false
		}
	}
	return true
}

// Build analyzes every Go file of src under the given configurations.
func Build(src Source, configs []Config, cache *Cache) *Index {
	return build(src, configs, cache, nil, nil)
}

// BuildPartial analyzes only the Go files in include (see Closure) and
// reports cov as the index coverage. For the subjects a closure was
// computed for, Evaluate, DeclsByID, MethodDecls and File give the same
// answers as on Build's full index; anything else is unspecified.
func BuildPartial(src Source, configs []Config, cache *Cache, include map[string]bool, cov Coverage) *Index {
	return build(src, configs, cache, include, &cov)
}

func findModules(src Source) []module {
	var mods []module
	for _, p := range src.Paths() {
		if path.Base(p) == "go.mod" {
			if b, ok := src.Bytes(p); ok {
				if mp := modulePath(b); mp != "" {
					mods = append(mods, module{dir: path.Dir(p), path: mp})
				}
			}
		}
	}
	sort.Slice(mods, func(i, j int) bool {
		if len(mods[i].dir) != len(mods[j].dir) {
			return len(mods[i].dir) > len(mods[j].dir)
		}
		return mods[i].dir < mods[j].dir
	})
	return mods
}

func factsKey(src Source, p string, b []byte) string {
	var sum string
	if h, ok := src.(Hasher); ok {
		sum, _ = h.SHA256(p)
	}
	if sum == "" {
		s := sha256.Sum256(b)
		sum = hex.EncodeToString(s[:])
	}
	return p + "\x00" + sum
}

// factsOf returns the (cached) facts for a file, or nil without bytes.
func factsOf(src Source, cache *Cache, p string) *FileFacts {
	b, ok := src.Bytes(p)
	if !ok {
		return nil
	}
	key := factsKey(src, p, b)
	ff := cache.get(key)
	if ff == nil {
		ff = AnalyzeFile(p, b)
		cache.put(key, ff)
	}
	return ff
}

func build(src Source, configs []Config, cache *Cache, include map[string]bool, cov *Coverage) *Index {
	if len(configs) == 0 {
		configs = DefaultConfigs()
	}
	ix := &Index{Configs: configs, byPath: map[string]int{}, modules: findModules(src)}
	var goPaths []string
	for _, p := range src.Paths() {
		if Analyzable(p) && (include == nil || include[p]) {
			goPaths = append(goPaths, p)
		}
	}
	ix.Coverage.GoFiles = len(goPaths)
	facts := make([]*FileFacts, len(goPaths))
	var wg sync.WaitGroup
	next := make(chan int)
	for w := 0; w < min(runtime.GOMAXPROCS(0), 16); w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				facts[i] = factsOf(src, cache, goPaths[i])
			}
		}()
	}
	for i := range goPaths {
		next <- i
	}
	close(next)
	wg.Wait()
	for _, ff := range facts {
		if ff == nil {
			ix.Coverage.Skipped++
			continue
		}
		if ff.ParseError != "" || ff.constraintErr {
			ix.Coverage.Failed++
			ix.FailedFiles = append(ix.FailedFiles, ff.Path)
			continue
		}
		ix.Coverage.Parsed++
		ix.byPath[ff.Path] = len(ix.Files)
		ix.Files = append(ix.Files, ff)
	}
	ix.Coverage.Complete = ix.Coverage.Failed == 0 && ix.Coverage.Skipped == 0
	if cov != nil {
		ix.Coverage = *cov
		ix.partial = true
	}
	ix.Member = make([][]bool, len(ix.Files))
	for i, ff := range ix.Files {
		m := make([]bool, len(configs))
		for c, cfg := range configs {
			m[c] = member(ff, cfg)
		}
		ix.Member[i] = m
	}
	ix.buildMaps()
	return ix
}

func member(ff *FileFacts, c Config) bool {
	if ff.IsTest && !c.Tests {
		return false
	}
	if !c.nameOK(ff.Path) {
		return false
	}
	if ff.ImportsC && !c.Cgo {
		return false
	}
	if ff.constraintExpr != nil && !ff.constraintExpr.Eval(c.tagOK) {
		return false
	}
	return true
}

// modulePath extracts the module path from go.mod bytes without
// evaluating anything else in the file.
func modulePath(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if i := strings.Index(line, "//"); i >= 0 {
			line = strings.TrimSpace(line[:i])
		}
		if rest, ok := strings.CutPrefix(line, "module"); ok && (rest == "" || rest[0] == ' ' || rest[0] == '\t') {
			return strings.Trim(strings.TrimSpace(rest), `"`)
		}
	}
	return ""
}

// ImportPathOf returns the import path of a repository directory, if it
// lies inside a module.
func (ix *Index) ImportPathOf(dir string) string { return importPathOf(ix.modules, dir) }

func importPathOf(mods []module, dir string) string {
	for _, m := range mods {
		if m.dir == "." {
			if dir == "." {
				return m.path
			}
			return m.path + "/" + dir
		}
		if dir == m.dir {
			return m.path
		}
		if strings.HasPrefix(dir, m.dir+"/") {
			return m.path + "/" + strings.TrimPrefix(dir, m.dir+"/")
		}
	}
	return ""
}

func (ix *Index) buildMaps() {
	ix.importDir = map[string]string{}
	ix.pkgDecls = map[string][]DeclRef{}
	ix.methods = map[string][]DeclRef{}
	ix.declByID = map[string][]DeclRef{}
	ix.usesByEncl = map[string][]UseRef{}
	for fi, ff := range ix.Files {
		if ip := ix.ImportPathOf(ff.Dir); ip != "" {
			ix.importDir[ip] = ff.Dir
		}
		pk := PkgKey(ff.Dir, ff.Package)
		for di, d := range ff.Decls {
			r := DeclRef{fi, di}
			ix.declByID[d.ID] = append(ix.declByID[d.ID], r)
			if d.Kind == KindMethod {
				ix.methods[d.Name] = append(ix.methods[d.Name], r)
			} else {
				ix.pkgDecls[pk+"\x00"+d.Name] = append(ix.pkgDecls[pk+"\x00"+d.Name], r)
			}
		}
		for ui, u := range ff.Uses {
			if u.Enclosing != "" {
				k := ff.Path + "\x00" + u.Enclosing
				ix.usesByEncl[k] = append(ix.usesByEncl[k], UseRef{fi, ui})
			}
		}
	}
	ix.refsTo = map[string][]UseRef{}
	ix.memberUses = map[string][]UseRef{}
	for fi, ff := range ix.Files {
		for ui := range ff.Uses {
			u := &ff.Uses[ui]
			if u.Kind == UseMember {
				ix.memberUses[u.Name] = append(ix.memberUses[u.Name], UseRef{fi, ui})
				continue
			}
			for _, id := range ix.resolveIDs(fi, u) {
				ix.refsTo[id] = append(ix.refsTo[id], UseRef{fi, ui})
			}
		}
	}
	ix.testsTo = map[string][]DeclRef{}
	ix.testsName = map[string][]DeclRef{}
	for fi, ff := range ix.Files {
		if !ff.IsTest {
			continue
		}
		for di, d := range ff.Decls {
			if !d.TestFunc {
				continue
			}
			seenID, seenName := map[string]bool{}, map[string]bool{}
			for _, ur := range ix.UsesIn(ff.Path, d.ID) {
				u := &ff.Uses[ur.Use]
				if u.Kind == UseMember {
					if !seenName[u.Name] {
						seenName[u.Name] = true
						ix.testsName[u.Name] = append(ix.testsName[u.Name], DeclRef{fi, di})
					}
					continue
				}
				for _, id := range ix.resolveIDs(fi, u) {
					if !seenID[id] {
						seenID[id] = true
						ix.testsTo[id] = append(ix.testsTo[id], DeclRef{fi, di})
					}
				}
			}
		}
	}
}

// resolveIDs returns the distinct decl ids a scope-resolved use denotes,
// restricted to decls sharing at least one configuration with the use.
func (ix *Index) resolveIDs(fi int, u *Use) []string {
	refs, _ := ix.Resolve(fi, u)
	var out []string
	for _, r := range refs {
		if !ix.shareConfig(fi, r.File) {
			continue
		}
		id := ix.Files[r.File].Decls[r.Decl].ID
		if len(out) == 0 || out[len(out)-1] != id {
			out = append(out, id)
		}
	}
	return out
}

func (ix *Index) shareConfig(a, b int) bool {
	for c := range ix.Configs {
		if ix.Member[a][c] && ix.Member[b][c] {
			return true
		}
	}
	return false
}

// Partial reports whether the index was built by BuildPartial.
func (ix *Index) Partial() bool { return ix.partial }

// Decl returns the decl for a reference.
func (ix *Index) Decl(r DeclRef) (*FileFacts, *Decl) {
	ff := ix.Files[r.File]
	return ff, &ff.Decls[r.Decl]
}

// Use returns the use for a reference.
func (ix *Index) Use(r UseRef) (*FileFacts, *Use) {
	ff := ix.Files[r.File]
	return ff, &ff.Uses[r.Use]
}

// File returns the facts for a path.
func (ix *Index) File(p string) (*FileFacts, int, bool) {
	i, ok := ix.byPath[p]
	if !ok {
		return nil, 0, false
	}
	return ix.Files[i], i, true
}

// DeclsByID returns all decls (possibly in several files/configs) with id.
func (ix *Index) DeclsByID(id string) []DeclRef { return ix.declByID[id] }

// AllDecls iterates over all decls in path/offset order.
func (ix *Index) AllDecls(fn func(DeclRef)) {
	for fi, ff := range ix.Files {
		for di := range ff.Decls {
			fn(DeclRef{fi, di})
		}
	}
}

// InAnyConfig reports whether file fi is in at least one configuration.
func (ix *Index) InAnyConfig(fi int) bool {
	for _, b := range ix.Member[fi] {
		if b {
			return true
		}
	}
	return false
}

// ConfigNames returns names of the configurations containing file fi.
func (ix *Index) ConfigNames(fi int) []string {
	var out []string
	for c, b := range ix.Member[fi] {
		if b {
			out = append(out, ix.Configs[c].Name)
		}
	}
	return out
}

// Resolve maps a use in file fi to the package-level decls it may denote.
// It returns (refs, exact): exact is true when the resolution is by
// package scope (local/import), false for name-matched members.
func (ix *Index) Resolve(fi int, u *Use) ([]DeclRef, bool) {
	ff := ix.Files[fi]
	switch u.Kind {
	case UseLocal:
		return ix.pkgDecls[PkgKey(ff.Dir, ff.Package)+"\x00"+u.Name], true
	case UsePkg:
		dir, ok := ix.importDir[u.Qual]
		if !ok {
			return nil, true
		}
		return ix.pkgDecls[dir+"\x00"+u.Name], true
	case UseMember:
		return ix.methods[u.Name], false
	}
	return nil, false
}

// MethodsNamed returns all method decls named name.
func (ix *Index) MethodsNamed(name string) []DeclRef { return ix.methods[name] }

// UsesIn returns the uses attributed to a decl (by file and decl id).
func (ix *Index) UsesIn(path, declID string) []UseRef {
	return ix.usesByEncl[path+"\x00"+declID]
}
