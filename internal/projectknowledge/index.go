// Package projectknowledge holds reproducible, non-authoritative source metadata.
// It does not retain source bodies, runtime decisions, memory or provider history.
package projectknowledge

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Zen1th53/marshal/internal/constitution"
	"github.com/Zen1th53/marshal/internal/hostgit"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/statepath"
	_ "modernc.org/sqlite"
)

const Version = 3
const MaxFileBytes = 4 << 20

// Bump Version on extraction/firewall/path-policy changes. The policy binding
// also follows the verified protected protocol; incompatible caches fail stale.
var policyDigest = func() string {
	sum := sha256.Sum256([]byte("project-knowledge-v3:compact-file-identity:go-ast-top-level:private-paths-v1:syntax-binding-firewall-v1:" + sourceKnowledgeProtocolDigest()))
	return hex.EncodeToString(sum[:])
}()

var ErrStale = errors.New("project knowledge stale: refresh required")
var ErrBinding = errors.New("project knowledge root binding mismatch")
var locks sync.Map

// This cache contains only verdicts for exact bytes under this process's fixed
// firewall/protocol policy. It is never persisted or used as project/task ACL.
// Every read still hashes current contained source bytes. Restart rechecks the
// firewall; a changed byte cannot reuse an old verdict.
var privacyCache = struct {
	sync.Mutex
	verdicts map[string]bool
	order    []string
}{verdicts: map[string]bool{}}

// privacyCacheRoot keys the verdict cache by repository, not by checkout: a
// linked worktree (each Marshal task lane is one) shares its main
// repository's verdicts, so dispatching a lane does not re-scan every file
// for secrets. Unrelated repositories still never share verdicts.
func privacyCacheRoot(root string) string {
	data, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return root // a directory .git (main checkout) or no repository
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir: ")
	if !ok {
		return root
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(root, gitdir)
	}
	// <common>/.git/worktrees/<name> belongs to the checkout at <common>.
	worktrees := filepath.Dir(filepath.Clean(gitdir))
	if filepath.Base(worktrees) != "worktrees" || filepath.Base(filepath.Dir(worktrees)) != ".git" {
		return root
	}
	return filepath.Dir(filepath.Dir(worktrees))
}

func contentVerdict(root, hash string, data []byte, paths ...string) (bool, bool) {
	path := ""
	if len(paths) != 0 {
		path = paths[0]
	}
	// Fixed-size root/policy/content binding: at most 8192 64-byte keys,
	// plus booleans and bounded FIFO metadata. No source or root text retained.
	bound := sha256.Sum256([]byte(privacyCacheRoot(root) + "\x00" + strconv.Itoa(Version) + "\x00" + sourceKnowledgeProtocolDigest() + "\x00" + strconv.FormatBool(filepath.Ext(path) == ".go") + "\x00" + hash))
	hash = hex.EncodeToString(bound[:])
	privacyCache.Lock()
	verdict, ok := privacyCache.verdicts[hash]
	privacyCache.Unlock()
	if ok {
		return verdict, true
	}
	verdict = safeSourceContent(path, data)
	privacyCache.Lock()
	defer privacyCache.Unlock()
	if _, ok = privacyCache.verdicts[hash]; !ok {
		if len(privacyCache.order) >= 8192 {
			delete(privacyCache.verdicts, privacyCache.order[0])
			privacyCache.order = privacyCache.order[1:]
		}
		privacyCache.verdicts[hash] = verdict
		privacyCache.order = append(privacyCache.order, hash)
	}
	return verdict, false
}

type Index struct{ root, database, state string }
type Entry struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Name     string `json:"name,omitempty"`
	Kind     string `json:"kind"`
	Package  string `json:"package,omitempty"`
	Receiver string `json:"receiver,omitempty"`
	Target   string `json:"target,omitempty"`
	FileHash string `json:"file_hash"`
}
type File struct {
	Path       string  `json:"path"`
	Hash       string  `json:"hash"`
	Size       int     `json:"size"`
	Lines      int     `json:"lines"`
	Package    string  `json:"package,omitempty"`
	Role       string  `json:"role"`
	Language   string  `json:"language"`
	ParseError bool    `json:"parse_error,omitempty"`
	Entries    []Entry `json:"-"`
	data       []byte
}
type Stats struct {
	Tracked          int   `json:"tracked_files"`
	Files            int   `json:"indexed_files"`
	GoFiles          int   `json:"go_files"`
	GoLines          int   `json:"go_lines"`
	Definitions      int   `json:"definitions"`
	References       int   `json:"syntactic_references"`
	Imports          int   `json:"imports"`
	Skipped          int   `json:"excluded_files"`
	ParseErrors      int   `json:"parse_errors"`
	Changed          int   `json:"changed_files"`
	Deleted          int   `json:"deleted_files"`
	Reused           int   `json:"reused_files"`
	ScannedBytes     int64 `json:"scanned_bytes"`
	ContentChecks    int   `json:"content_checks"`
	PrivacyCacheHits int   `json:"privacy_cache_hits"`
}
type Snapshot struct {
	Version      int    `json:"version"`
	Head         string `json:"head"`
	Digest       string `json:"tree_digest"`
	Dirty        bool   `json:"dirty_tracked"`
	PolicyDigest string `json:"policy_digest"`
	Stats        Stats  `json:"stats"`
}
type Query struct {
	Mode, Text, Path string
	Offset, Limit    int
	// Paths is an internal, approved-task narrowing for brief construction.
	// MCP callers cannot supply it through tool arguments.
	Paths []string
}
type Result struct {
	Snapshot   Snapshot `json:"snapshot"`
	Precision  string   `json:"precision"`
	Items      []Entry  `json:"items"`
	Total      int      `json:"total"`
	NextOffset int      `json:"next_offset,omitempty"`
}

// Open binds a private, derived database to one canonical root. Only Refresh
// creates anything. Read calls never initialize state or change the main store.
func Open(root, state string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil || resolved != abs {
		return nil, ErrBinding
	}
	if state != statepath.Path(abs) {
		return nil, ErrBinding
	}
	idx := &Index{root: abs, state: state, database: filepath.Join(state, "knowledge", "index-v1.db")}
	if err := idx.safeState(); err != nil {
		return nil, err
	}
	return idx, nil
}

func (i *Index) safeState() error {
	root, err := filepath.EvalSymlinks(i.root)
	if err != nil || root != i.root {
		return ErrBinding
	}
	paths := []string{i.database, i.database + "-wal", i.database + "-shm", i.database + "-journal"}
	for dir := filepath.Dir(i.database); ; dir = filepath.Dir(dir) {
		paths = append(paths, dir)
		if dir == i.state {
			break
		}
		if dir == filepath.Dir(dir) {
			return ErrBinding
		}
	}
	for _, path := range paths {
		if err := statepath.CheckPath(filepath.Dir(i.state), path); err != nil {
			return fmt.Errorf("%w: %v", ErrBinding, err)
		}
		st, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return ErrBinding
		}
		if path != i.database && !strings.HasPrefix(path, i.database+"-") && (!st.IsDir() || st.Mode().Perm()&0077 != 0) {
			return ErrBinding
		}
		if (path == i.database || strings.HasPrefix(path, i.database+"-")) && (!st.Mode().IsRegular() || st.Mode().Perm()&0077 != 0) {
			return ErrBinding
		}
	}
	return nil
}

func (i *Index) lock() func() {
	value, _ := locks.LoadOrStore(i.database, &sync.Mutex{})
	mu := value.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func (i *Index) connection(ctx context.Context, write bool) (*sql.DB, error) {
	if err := i.safeState(); err != nil {
		return nil, err
	}
	mode := "ro"
	if write {
		if err := os.MkdirAll(filepath.Dir(i.database), 0700); err != nil {
			return nil, err
		}
		if err := os.Chmod(filepath.Dir(i.database), 0700); err != nil {
			return nil, err
		}
		f, err := os.OpenFile(i.database, os.O_CREATE|os.O_RDWR, 0600)
		if err != nil {
			return nil, err
		}
		if err = f.Close(); err != nil {
			return nil, err
		}
		mode = "rw"
	} else if _, err := os.Stat(i.database); os.IsNotExist(err) {
		return nil, ErrStale
	} else if err != nil {
		return nil, err
	}
	if err := i.safeState(); err != nil {
		return nil, err
	}
	uri := url.URL{Scheme: "file", Path: filepath.ToSlash(i.database)}
	params := url.Values{"mode": {mode}, "_pragma": {"busy_timeout(5000)"}}
	uri.RawQuery = params.Encode()
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func gitHead(ctx context.Context, root string) (string, error) {
	cmd, err := hostgit.Command(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	b, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("read project HEAD: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func gitDirty(ctx context.Context, root string) (bool, error) {
	cmd, err := hostgit.Command(ctx, root, "status", "--porcelain=v1", "-z", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	data, err := cmd.Output()
	return len(data) > 0, err
}

func gitFiles(ctx context.Context, root string) ([]string, map[string]bool, error) {
	cmd, err := hostgit.Command(ctx, root, "ls-files", "--cached", "-z")
	if err != nil {
		return nil, nil, err
	}
	b, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}
	cmd, err = hostgit.Command(ctx, root, "ls-files", "--cached", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, err
	}
	ignoredBytes, err := cmd.Output()
	if err != nil {
		return nil, nil, err
	}
	ignored := map[string]bool{}
	for _, path := range strings.Split(string(ignoredBytes), "\x00") {
		ignored[path] = true
	}
	set := map[string]bool{}
	for _, path := range strings.Split(string(b), "\x00") {
		if path != "" {
			set[path] = true
		}
	}
	paths := make([]string, 0, len(set))
	for path := range set {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	return paths, ignored, nil
}

func deniedPath(path string) bool {
	if !utf8.ValidString(path) || len(path) > 1024 || strings.ContainsAny(path, "\r\n\x00\\") || filepath.IsAbs(path) || filepath.Clean(path) != path || path == ".." || strings.HasPrefix(path, "../") {
		return true
	}
	lower := strings.ToLower(filepath.ToSlash(path))
	for _, part := range strings.Split(lower, "/") {
		if part == ".git" || part == statepath.DirName || part == ".marshal-extreme" || part == ".env" || strings.HasPrefix(part, ".env.") || part == ".codex" || part == ".claude" || part == ".opencode" || part == "node_modules" || part == "vendor" || part == "credentials" || part == "secrets" || part == "transcripts" || part == "sessions" {
			return true
		}
	}
	base := filepath.Base(lower)
	if strings.HasPrefix(lower, "internal/constitution/") && strings.Contains(base, "protocol") {
		return true
	}
	return strings.Contains(base, "transcript") || strings.HasPrefix(base, "rollout-") || strings.HasPrefix(base, "id_rsa") || strings.HasPrefix(base, "id_ed25519") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || strings.HasSuffix(base, ".jsonl") || strings.Contains(base, "credential")
}

var protocolLines = func() []string {
	text, _ := constitution.MarshalProtocol()
	lines := []string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= 80 {
			lines = append(lines, line)
		}
	}
	return lines
}()

func safeContent(data []byte) bool {
	if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return false
	}
	text := string(data)
	if security.NewFirewall(security.FirewallConfig{}).ScanText(text) != nil {
		return false
	}
	if strings.Contains(strings.ToUpper(text), "MARSHAL PROTOCOL") {
		return false
	}
	for _, line := range protocolLines {
		if strings.Contains(text, line) {
			return false
		}
	}
	return true
}

// Go binding punctuation is syntax, not a prose credential assignment. This
// fallback changes only AST-proven punctuation before scanning the complete
// original text with the unchanged firewall. Comments, strings, identifiers and
// protocol bytes remain intact. It authorizes metadata extraction, never raw
// source export. Parse errors and literal-valued bindings remain conservative.
func safeSourceContent(path string, data []byte) bool {
	if safeContent(data) {
		return true
	}
	if filepath.Ext(path) != ".go" || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return false
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, path, data, 0)
	if err != nil {
		return false
	}
	masked := append([]byte(nil), data...)
	mask := func(pos token.Pos, width int) {
		offset := fset.Position(pos).Offset
		if offset < 0 || offset+width > len(masked) {
			return
		}
		for n := 0; n < width; n++ {
			masked[offset+n] = ' '
		}
	}
	ast.Inspect(tree, func(node ast.Node) bool {
		switch binding := node.(type) {
		case *ast.AssignStmt:
			if binding.Tok != token.DEFINE && binding.Tok != token.ASSIGN {
				break
			}
			for _, rhs := range binding.Rhs {
				if !nonLiteralBinding(rhs) {
					return true
				}
			}
			width := 1
			if binding.Tok == token.DEFINE {
				width = 2
			}
			mask(binding.TokPos, width)
		case *ast.KeyValueExpr:
			if _, namedField := binding.Key.(*ast.Ident); namedField && nonLiteralBinding(binding.Value) {
				mask(binding.Colon, 1)
			}
		case *ast.RangeStmt:
			if nonLiteralBinding(binding.X) {
				if binding.Tok == token.DEFINE {
					mask(binding.TokPos, 2)
				} else if binding.Tok == token.ASSIGN {
					mask(binding.TokPos, 1)
				}
			}
		}
		return true
	})
	return safeContent(masked)
}

func nonLiteralBinding(expr ast.Expr) bool {
	// This exact prefix removal contains an authentication marker, not a
	// credential value. No other call containing a literal gets an exemption.
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 2 {
		selector, selected := call.Fun.(*ast.SelectorExpr)
		if selected && selector.Sel.Name == "TrimPrefix" {
			pkg, named := selector.X.(*ast.Ident)
			prefix, literal := call.Args[1].(*ast.BasicLit)
			if named && pkg.Name == "strings" && literal && prefix.Kind == token.STRING {
				value, err := strconv.Unquote(prefix.Value)
				if err == nil && value == "Bearer " && nonLiteralBinding(call.Args[0]) {
					return true
				}
			}
		}
	}
	safe := true
	ast.Inspect(expr, func(node ast.Node) bool {
		if _, literal := node.(*ast.BasicLit); literal {
			safe = false
		}
		return safe
	})
	return safe
}

// readFile refuses symlinks in every component, not only the final path. The
// post-open inode and second component check detects ordinary replacement races.
func (i *Index) readFile(path string) ([]byte, bool, error) {
	current := i.root
	for _, part := range strings.Split(path, "/") {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return nil, false, nil
		}
	}
	st, err := os.Lstat(current)
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() || st.Size() > MaxFileBytes {
		return nil, false, nil
	}
	root, err := os.OpenRoot(i.root)
	if err != nil {
		return nil, false, err
	}
	defer root.Close()
	f, err := root.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return nil, false, err
	}
	if !os.SameFile(st, opened) {
		return nil, false, ErrStale
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil || resolved != current {
		return nil, false, ErrStale
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > MaxFileBytes {
		return nil, false, nil
	}
	return data, true, nil
}

func (i *Index) scan(ctx context.Context, classify bool) (Snapshot, map[string]File, error) {
	head, err := gitHead(ctx, i.root)
	if err != nil {
		return Snapshot{}, nil, err
	}
	paths, ignored, err := gitFiles(ctx, i.root)
	if err != nil {
		return Snapshot{}, nil, err
	}
	s := Snapshot{Version: Version, Head: head, PolicyDigest: policyDigest, Stats: Stats{Tracked: len(paths)}}
	s.Dirty, err = gitDirty(ctx, i.root)
	if err != nil {
		return Snapshot{}, nil, err
	}
	files := map[string]File{}
	digest := sha256.New()
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return Snapshot{}, nil, err
		}
		fmt.Fprintf(digest, "%d:%s\x00", len(path), path)
		if ignored[path] || deniedPath(path) || !safeContent([]byte(path)) {
			s.Stats.Skipped++
			io.WriteString(digest, "excluded\x00")
			continue
		}
		data, ok, err := i.readFile(path)
		if err != nil {
			return Snapshot{}, nil, err
		}
		if !ok {
			s.Stats.Skipped++
			io.WriteString(digest, "unavailable\x00")
			continue
		}
		sum := sha256.Sum256(data)
		hash := hex.EncodeToString(sum[:])
		io.WriteString(digest, hash+"\x00")
		s.Stats.ScannedBytes += int64(len(data))
		if classify {
			safe, reused := contentVerdict(i.root, hash, data, path)
			if reused {
				s.Stats.PrivacyCacheHits++
			} else {
				s.Stats.ContentChecks++
			}
			if !safe {
				s.Stats.Skipped++
				continue
			}
		} else if !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
			continue
		}
		file := File{Path: path, Hash: hash, Size: len(data), Lines: strings.Count(string(data), "\n"), Role: "source", Language: strings.TrimPrefix(filepath.Ext(path), "."), data: data}
		if strings.HasSuffix(path, "_test.go") {
			file.Role = "test"
		}
		files[path] = file
	}
	s.Digest = hex.EncodeToString(digest.Sum(nil))
	s.Stats.Files = len(files)
	end, err := gitHead(ctx, i.root)
	if err != nil {
		return Snapshot{}, nil, err
	}
	if end != head {
		return Snapshot{}, nil, ErrStale
	}
	return s, files, nil
}

func parseFile(file File) File {
	add := func(line int, name, kind, receiver, target string) {
		file.Entries = append(file.Entries, Entry{Path: file.Path, Line: line, Name: name, Kind: kind, Package: file.Package, Receiver: receiver, Target: target, FileHash: file.Hash})
	}
	add(1, filepath.Base(file.Path), "file", "", file.Language)
	if file.Language != "go" {
		if filepath.Base(file.Path) == "go.mod" {
			for n, line := range strings.Split(string(file.data), "\n") {
				if strings.HasPrefix(line, "module ") || strings.HasPrefix(line, "go ") {
					add(n+1, strings.TrimSpace(line), "project_fact", "", "")
				}
			}
		}
		if strings.EqualFold(filepath.Ext(file.Path), ".md") && (strings.HasPrefix(strings.ToLower(filepath.Base(file.Path)), "readme") || strings.HasPrefix(file.Path, "docs/")) {
			for n, line := range strings.Split(string(file.data), "\n") {
				if strings.HasPrefix(line, "#") && len(line) <= 256 {
					add(n+1, strings.TrimLeft(line, "# "), "doc_heading", "", "")
				}
			}
		}
		return file
	}
	fset := token.NewFileSet()
	tree, err := parser.ParseFile(fset, file.Path, file.data, 0)
	if err != nil {
		file.ParseError = true
		return file
	}
	file.Package = tree.Name.Name
	// File identity is shared by compact rows, including the file entry added
	// before parsing. Give every entry the same canonical package binding.
	file.Entries[0].Package = file.Package
	add(fset.Position(tree.Name.Pos()).Line, tree.Name.Name, "package", "", "")
	defs := map[token.Pos]bool{tree.Name.Pos(): true}
	for _, decl := range tree.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			receiver := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				typ := d.Recv.List[0].Type
				if ptr, ok := typ.(*ast.StarExpr); ok {
					typ = ptr.X
				}
				switch r := typ.(type) {
				case *ast.Ident:
					receiver = r.Name
				case *ast.IndexExpr:
					if id, ok := r.X.(*ast.Ident); ok {
						receiver = id.Name
					}
				case *ast.IndexListExpr:
					if id, ok := r.X.(*ast.Ident); ok {
						receiver = id.Name
					}
				}
			}
			kind := "function"
			if receiver != "" {
				kind = "method"
			}
			if file.Role == "test" && (strings.HasPrefix(d.Name.Name, "Test") || strings.HasPrefix(d.Name.Name, "Benchmark") || strings.HasPrefix(d.Name.Name, "Example") || strings.HasPrefix(d.Name.Name, "Fuzz")) {
				kind = "test"
			}
			defs[d.Name.Pos()] = true
			add(fset.Position(d.Name.Pos()).Line, d.Name.Name, kind, receiver, "")
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					defs[spec.Name.Pos()] = true
					add(fset.Position(spec.Pos()).Line, spec.Name.Name, "type", "", "")
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						defs[name.Pos()] = true
						add(fset.Position(name.Pos()).Line, name.Name, d.Tok.String(), "", "")
					}
				case *ast.ImportSpec:
					target, _ := strconv.Unquote(spec.Path.Value)
					alias := filepath.Base(target)
					if spec.Name != nil {
						alias = spec.Name.Name
						defs[spec.Name.Pos()] = true
					}
					add(fset.Position(spec.Pos()).Line, alias, "import", "", target)
				}
			}
		}
	}
	ast.Inspect(tree, func(node ast.Node) bool {
		id, ok := node.(*ast.Ident)
		if ok && !defs[id.Pos()] && id.Name != "_" {
			add(fset.Position(id.Pos()).Line, id.Name, "syntactic_reference", "", "")
		}
		return true
	})
	return file
}

func loadSnapshot(ctx context.Context, db interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, root string) (Snapshot, error) {
	var bound, data string
	if err := db.QueryRowContext(ctx, "SELECT root,snapshot FROM metadata WHERE id=1").Scan(&bound, &data); err != nil {
		if errors.Is(err, sql.ErrNoRows) || strings.Contains(err.Error(), "no such table") {
			return Snapshot{}, ErrStale
		}
		return Snapshot{}, err
	}
	if bound != root {
		return Snapshot{}, ErrBinding
	}
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(data), &snapshot); err != nil {
		return Snapshot{}, err
	}
	if snapshot.Version != Version || snapshot.PolicyDigest != policyDigest {
		return Snapshot{}, ErrStale
	}
	return snapshot, nil
}

// Refresh reparses only changed eligible files and atomically replaces derived
// rows. A failed parse is visible in coverage, never a stale prior definition.
func (i *Index) Refresh(ctx context.Context) (Snapshot, error) {
	// Another MARSHAL process on the same project may refresh the shared
	// projection at the same moment; a deferred transaction that loses that
	// race fails at once with SQLITE_BUSY. The projection is disposable and
	// derived from the same repository, so wait and retry instead of failing
	// the window's startup.
	var snapshot Snapshot
	var err error
	for attempt := 0; ; attempt++ {
		if snapshot, err = i.refresh(ctx); err == nil || !sqliteBusy(err) || attempt >= 40 {
			return snapshot, err
		}
		timer := time.NewTimer(time.Duration(attempt+1) * 25 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Snapshot{}, ctx.Err()
		case <-timer.C:
		}
	}
}

func sqliteBusy(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func (i *Index) refresh(ctx context.Context) (Snapshot, error) {
	unlock := i.lock()
	defer unlock()
	s, files, err := i.scan(ctx, true)
	if err != nil {
		return Snapshot{}, err
	}
	db, err := i.connection(ctx, true)
	if err != nil {
		return Snapshot{}, err
	}
	defer db.Close()
	_, err = db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS metadata(id INTEGER PRIMARY KEY CHECK(id=1),root TEXT NOT NULL,snapshot TEXT NOT NULL);`)
	if err != nil {
		return Snapshot{}, err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	_, err = loadSnapshot(ctx, tx, i.root)
	if err != nil && !errors.Is(err, ErrStale) {
		return Snapshot{}, err
	}
	rebuilt := errors.Is(err, ErrStale)
	compact := false
	if rebuilt {
		// Only an explicit authorized Refresh rebuilds incompatible derived
		// metadata. loadSnapshot above checks root binding before any removal;
		// this database contains no memory, tasks, decisions or grant tables.
		var retained int
		if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM metadata").Scan(&retained); err != nil {
			return Snapshot{}, err
		}
		compact = retained != 0
		if _, err = tx.ExecContext(ctx, "DROP TABLE IF EXISTS entries; DROP TABLE IF EXISTS files"); err != nil {
			return Snapshot{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS files(id INTEGER PRIMARY KEY,path TEXT NOT NULL UNIQUE,hash TEXT NOT NULL,package TEXT NOT NULL,metadata TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS entries(file_id INTEGER NOT NULL,line INTEGER NOT NULL,name TEXT NOT NULL,kind TEXT NOT NULL,receiver TEXT NOT NULL,target TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS entry_name ON entries(name,kind,file_id,line);
CREATE INDEX IF NOT EXISTS entry_path ON entries(file_id,kind,line);
CREATE INDEX IF NOT EXISTS entry_target ON entries(target,kind,file_id);`); err != nil {
		return Snapshot{}, err
	}
	rows, err := tx.QueryContext(ctx, "SELECT path,hash,metadata FROM files")
	if err != nil {
		return Snapshot{}, err
	}
	old := map[string]File{}
	for rows.Next() {
		var path, hash, data string
		if err = rows.Scan(&path, &hash, &data); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		var f File
		if err = json.Unmarshal([]byte(data), &f); err != nil {
			rows.Close()
			return Snapshot{}, err
		}
		f.Hash = hash
		old[path] = f
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Snapshot{}, err
	}
	rows.Close()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err = ctx.Err(); err != nil {
			return Snapshot{}, err
		}
		file := files[path]
		if prior, ok := old[path]; ok && prior.Hash == file.Hash {
			file = prior
			s.Stats.Reused++
		} else {
			file = parseFile(file)
			s.Stats.Changed++
			data, _ := json.Marshal(file)
			var fileID int64
			if err = tx.QueryRowContext(ctx, "INSERT INTO files(path,hash,package,metadata) VALUES(?,?,?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash,package=excluded.package,metadata=excluded.metadata RETURNING id", path, file.Hash, file.Package, string(data)).Scan(&fileID); err != nil {
				return Snapshot{}, err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE file_id=?", fileID); err != nil {
				return Snapshot{}, err
			}
			for _, e := range file.Entries {
				if _, err = tx.ExecContext(ctx, "INSERT INTO entries VALUES(?,?,?,?,?,?)", fileID, e.Line, e.Name, e.Kind, e.Receiver, e.Target); err != nil {
					return Snapshot{}, err
				}
			}
		}
		if file.Language == "go" {
			s.Stats.GoFiles++
			s.Stats.GoLines += file.Lines
		}
		if file.ParseError {
			s.Stats.ParseErrors++
		}
		delete(old, path)
	}
	for path := range old {
		s.Stats.Deleted++
		if _, err = tx.ExecContext(ctx, "DELETE FROM entries WHERE file_id=(SELECT id FROM files WHERE path=?)", path); err != nil {
			return Snapshot{}, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM files WHERE path=?", path); err != nil {
			return Snapshot{}, err
		}
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries WHERE kind IN ('function','method','type','const','var','test')").Scan(&s.Stats.Definitions); err != nil {
		return Snapshot{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries WHERE kind='syntactic_reference'").Scan(&s.Stats.References); err != nil {
		return Snapshot{}, err
	}
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entries WHERE kind='import'").Scan(&s.Stats.Imports); err != nil {
		return Snapshot{}, err
	}
	// Validate bytes again before committing; callers never see a half refresh.
	end, _, err := i.scan(ctx, false)
	if err != nil {
		return Snapshot{}, err
	}
	if end.Head != s.Head || end.Digest != s.Digest || end.Dirty != s.Dirty {
		return Snapshot{}, ErrStale
	}
	data, _ := json.Marshal(s)
	if _, err = tx.ExecContext(ctx, "INSERT OR REPLACE INTO metadata VALUES(1,?,?)", i.root, string(data)); err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	if compact {
		// Reclaim pages containing duplicated old-format paths/hashes. A
		// cancellation here leaves the committed derived index usable; SQLite
		// owns vacuum atomicity. Never vacuum the canonical runtime store.
		if _, err = db.ExecContext(ctx, "VACUUM"); err != nil {
			return Snapshot{}, err
		}
	}
	return s, nil
}

func (i *Index) Query(ctx context.Context, q Query) (Result, error) {
	if q.Offset < 0 || q.Limit < 1 || q.Limit > 100 {
		return Result{}, errors.New("invalid knowledge pagination")
	}
	unlock := i.lock()
	defer unlock()
	live, eligible, err := i.scan(ctx, false)
	if err != nil {
		return Result{}, err
	}
	db, err := i.connection(ctx, false)
	if err != nil {
		return Result{}, err
	}
	defer db.Close()
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback()
	s, err := loadSnapshot(ctx, tx, i.root)
	if err != nil {
		return Result{}, err
	}
	if s.Head != live.Head || s.Digest != live.Digest || s.Dirty != live.Dirty {
		return Result{}, fmt.Errorf("%w: source snapshot changed (head=%t, bytes=%t, dirty=%t)", ErrStale, s.Head != live.Head, s.Digest != live.Digest, s.Dirty != live.Dirty)
	}
	result := Result{Snapshot: s, Precision: "Go AST definitions; references are syntactic identifier occurrences, not resolved calls/types; related files are package/import/test candidates", Items: []Entry{}}
	// Intersect cached candidates with current eligible path/hash authority
	// before SQL ordering/pagination. A copied/tampered excluded row cannot
	// starve legitimate results or export a sensitive source path.
	allowed := map[string]string{}
	for path, file := range eligible {
		allowed[path] = file.Hash
	}
	allowedJSON, _ := json.Marshal(allowed)
	where := ""
	args := []any{string(allowedJSON)}
	switch q.Mode {
	case "context":
		where = "kind='file' AND (lower(retained.path) IN ('readme.md','project.md','mission.md','architecture.md','docs/architecture.md','docs/mission.md','docs/project.md') OR substr(lower(retained.path),1,18)='docs/architecture/')"
	case "overview":
		where = "kind IN ('package','project_fact','doc_heading')"
	case "symbol":
		where = "name=? AND kind IN ('function','method','type','const','var','test')"
		args = append(args, q.Text)
	case "references":
		where = "name=? AND kind='syntactic_reference'"
		args = append(args, q.Text)
	case "search":
		where = "(instr(lower(name),lower(?))>0 OR instr(lower(retained.path),lower(?))>0) AND kind!='syntactic_reference'"
		args = append(args, q.Text, q.Text)
	case "related":
		if deniedPath(q.Path) {
			return Result{}, errors.New("invalid knowledge path")
		}
		source, ok := eligible[q.Path]
		if !ok {
			return result, nil
		}
		var retainedHash string
		if err = tx.QueryRowContext(ctx, "SELECT hash FROM files WHERE path=?", q.Path).Scan(&retainedHash); errors.Is(err, sql.ErrNoRows) {
			return result, nil
		} else if err != nil {
			return Result{}, err
		}
		if retainedHash != source.Hash || !safeSourceContent(source.Path, source.data) {
			return Result{}, ErrStale
		}
		pkg := parseFile(source).Package
		dir := filepath.ToSlash(filepath.Dir(q.Path))
		prefix := ""
		if dir != "." {
			prefix = dir + "/"
		}
		where = "kind IN ('package','import','test') AND (retained.path=? OR (package=? AND substr(retained.path,1,?)=? AND instr(substr(retained.path,?),'/')=0))"
		args = append(args, q.Path, pkg, len(prefix), prefix, len(prefix)+1)
	case "brief":
		if len(q.Paths) == 0 || len(q.Paths) > 32 {
			return Result{}, errors.New("invalid knowledge brief scope")
		}
		parts := []string{}
		for _, path := range q.Paths {
			if deniedPath(path) || !safeContent([]byte(path)) || path == "." || filepath.ToSlash(filepath.Clean(path)) != path {
				return Result{}, errors.New("invalid knowledge brief path")
			}
			if _, file := eligible[path]; !file && filepath.Ext(path) == "" {
				prefix := strings.TrimSuffix(path, "/") + "/"
				parts = append(parts, "substr(retained.path,1,?)=?")
				args = append(args, len(prefix), prefix)
				continue
			}
			prefix := filepath.ToSlash(filepath.Dir(path)) + "/"
			if prefix == "./" {
				prefix = ""
			}
			parts = append(parts, "(retained.path=? OR (kind='test' AND substr(retained.path,1,?)=? AND instr(substr(retained.path,?),'/')=0))")
			args = append(args, path, len(prefix), prefix, len(prefix)+1)
		}
		where = "kind IN ('function','method','type','const','var','test','import','doc_heading') AND (" + strings.Join(parts, " OR ") + ")"
	default:
		return Result{}, errors.New("unknown knowledge query")
	}
	from := " FROM entries JOIN files AS retained ON entries.file_id=retained.id JOIN json_each(?) AS eligible ON retained.path=eligible.key AND retained.hash=eligible.value WHERE (" + where + ")"
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*)"+from, args...).Scan(&result.Total); err != nil {
		return Result{}, err
	}
	args = append(args, q.Limit, q.Offset)
	order := "retained.path,line,name,kind"
	if q.Mode == "brief" {
		// Round-robin definitions, imports and adjacent test candidates so a
		// long source file cannot consume the complete brief before test links.
		bucket := "CASE WHEN kind='test' THEN 0 WHEN kind='import' THEN 1 ELSE 2 END"
		order = "ROW_NUMBER() OVER (PARTITION BY " + bucket + " ORDER BY retained.path,line,name,kind)," + bucket + "," + order
	}
	rows, err := tx.QueryContext(ctx, "SELECT retained.path,line,name,kind,retained.package,receiver,target,retained.hash"+from+" ORDER BY "+order+" LIMIT ? OFFSET ?", args...)
	if err != nil {
		return Result{}, err
	}
	validated := map[string]map[Entry]bool{}
	for rows.Next() {
		var e Entry
		if err = rows.Scan(&e.Path, &e.Line, &e.Name, &e.Kind, &e.Package, &e.Receiver, &e.Target, &e.FileHash); err != nil {
			rows.Close()
			return Result{}, err
		}
		if !safeContent([]byte(e.Name + "\n" + e.Package + "\n" + e.Receiver + "\n" + e.Target)) {
			rows.Close()
			return Result{}, ErrStale
		}
		if validated[e.Path] == nil {
			if !safeSourceContent(e.Path, eligible[e.Path].data) {
				rows.Close()
				return Result{}, ErrStale
			}
			valid := map[Entry]bool{}
			for _, actual := range parseFile(eligible[e.Path]).Entries {
				valid[actual] = true
			}
			validated[e.Path] = valid
		}
		if !validated[e.Path][e] {
			rows.Close()
			return Result{}, fmt.Errorf("%w: cached entry does not match source metadata", ErrStale)
		}
		result.Items = append(result.Items, e)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return Result{}, err
	}
	rows.Close()
	if q.Offset+len(result.Items) < result.Total {
		result.NextOffset = q.Offset + len(result.Items)
	}
	end, _, err := i.scan(ctx, false)
	if err != nil {
		return Result{}, err
	}
	if end.Head != s.Head || end.Digest != s.Digest || end.Dirty != s.Dirty {
		return Result{}, ErrStale
	}
	return result, nil
}

// RebuildAfterMove discards only derived metadata bound to the previous path.
// The runtime invokes it only after canonical admission confirms a repository move.
// Query and Ensure deliberately never repair a root mismatch themselves.
func (i *Index) RebuildAfterMove(ctx context.Context, previousRoot string) (Snapshot, error) {
	err := func() error {
		unlock := i.lock()
		defer unlock()
		db, err := i.connection(ctx, true)
		if err != nil {
			return err
		}
		defer db.Close()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		var bound string
		if err := tx.QueryRowContext(ctx, "SELECT root FROM metadata WHERE id=1").Scan(&bound); err != nil {
			return err
		}
		if previousRoot == "" || bound != previousRoot || bound == i.root {
			return ErrBinding
		}
		if _, err = tx.ExecContext(ctx, "DROP TABLE IF EXISTS entries; DROP TABLE IF EXISTS files; DROP TABLE IF EXISTS metadata"); err != nil {
			return err
		}
		return tx.Commit()
	}()
	if err != nil {
		return Snapshot{}, err
	}
	return i.Refresh(ctx)
}

// DeniedSourcePath reports whether path is outside what MARSHAL ever exposes
// as derived source facts (state folders, provider homes, credentials,
// transcripts, vendored trees and similar). Other evidence surfaces use it to
// apply the same privacy rule.
func DeniedSourcePath(path string) bool { return deniedPath(path) }
