// effect-inventory checks the reviewed source-site inventory without running effects.
package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"flag"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

type site struct {
	File, Function, Effect, SourceHash string
	Ordinal                            int
	SupportReason                      string
}
type annotation struct {
	Site                   site
	Scope, Guard, Evidence string
}

func (s site) key() string {
	return fmt.Sprintf("%s:%s:%s:%d", s.File, s.Function, s.Effect, s.Ordinal) + ":" + s.SourceHash
}

// The scan is intentionally broader than only the named record types: all
// durable filesystem and SQL writes are candidates, so a new name cannot hide a
// memory, evidence, approval or plan write. Schema migrations are not runtime
// record writes. Tests, test support and development tools are not shipped code.
func discover(root string) ([]site, error) {
	var sites []site
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "testdata" || rel == "tools" || rel == "internal/testutil") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		found, err := scanFile(rel, path)
		if err != nil {
			return err
		}
		sites = append(sites, found...)
		return nil
	})
	sort.Slice(sites, func(i, j int) bool { return sites[i].key() < sites[j].key() })
	return sites, err
}

func scanFile(rel, path string) ([]site, error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		return nil, err
	}
	imports := map[string]string{}
	for _, imp := range file.Imports {
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return nil, err
		}
		name := filepath.Base(value)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "." {
			return nil, fmt.Errorf("%s: dot import needs inventory detector review", rel)
		}
		imports[name] = value
	}
	var sites []site
	scanBody := func(name string, body ast.Node) {
		var formatted bytes.Buffer
		_ = format.Node(&formatted, fset, body)
		sourceHash := fmt.Sprintf("%x", sha256.Sum256(formatted.Bytes()))
		// Track locally opened os.Root handles so rooted mutations are inventoried
		// like their os package equivalents rather than disappearing at a method call.
		localImports := map[string]string{}
		for name, path := range imports {
			localImports[name] = path
		}
		ast.Inspect(body, func(node ast.Node) bool {
			assignment, ok := node.(*ast.AssignStmt)
			if !ok || len(assignment.Rhs) != 1 || len(assignment.Lhs) == 0 {
				return true
			}
			call, ok := assignment.Rhs[0].(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || selector.Sel.Name != "OpenRoot" {
				return true
			}
			pkg, ok := selector.X.(*ast.Ident)
			handle, handleOK := assignment.Lhs[0].(*ast.Ident)
			if ok && handleOK && localImports[pkg.Name] == "os" {
				localImports[handle.Name] = "os"
			}
			return true
		})
		ordinals := map[string]int{}
		ast.Inspect(body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			effect := classify(call, localImports, rel)
			if effect != "" {
				ordinals[effect]++
				sites = append(sites, site{rel, name, effect, sourceHash, ordinals[effect], supportReason(call, localImports, rel, name, body)})
			}
			return true
		})
	}
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.FuncDecl:
			if decl.Body == nil {
				continue
			}
			name := decl.Name.Name
			if decl.Recv != nil {
				var b bytes.Buffer
				_ = format.Node(&b, fset, decl.Recv.List[0].Type)
				name = b.String() + "." + name
			}
			scanBody(name, decl.Body)
		case *ast.GenDecl:
			for _, spec := range decl.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				var names []string
				for _, name := range value.Names {
					names = append(names, name.Name)
				}
				scanBody("var:"+strings.Join(names, "+"), value)
			}
		}
	}

	return sites, nil
}

func classify(call *ast.CallExpr, imports map[string]string, file string) string {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		if id, ok := call.Fun.(*ast.Ident); ok && (id.Name == "git" || id.Name == "gitMarshal" || id.Name == "runGit" || id.Name == "gitOutput" || id.Name == "execGit" || id.Name == "diffGit" || id.Name == "restoreSnapshot" || id.Name == "RestoreDatabase" || id.Name == "RestoreDatabaseExpected") {
			return "git-or-restore-wrapper"
		}
		return ""
	}
	name := selector.Sel.Name
	receiver, _ := selector.X.(*ast.Ident)
	pkg := ""
	if receiver != nil {
		pkg = imports[receiver.Name]
	}
	switch pkg {
	case "os/exec":
		if name == "Command" || name == "CommandContext" {
			return "program"
		}
	case "syscall", "golang.org/x/sys/unix":
		if name == "Exec" || name == "ForkExec" || name == "StartProcess" {
			return "program"
		}
	case "os":
		switch name {
		case "StartProcess":
			return "program"
		case "WriteFile", "Create", "CreateTemp", "Rename", "Remove", "RemoveAll", "Truncate", "Link", "Symlink":
			return "filesystem"
		case "OpenFile":
			if len(call.Args) > 1 {
				if flag, ok := call.Args[1].(*ast.SelectorExpr); ok && flag.Sel.Name == "O_RDONLY" {
					return ""
				}
			}
			return "filesystem"
		}
	case "github.com/Zen1th53/marshal/internal/learning":
		if name == "Restore" {
			return "restore"
		}
	case "github.com/Zen1th53/marshal/internal/tmux":
		if name == "RunCommand" || name == "NewSession" || name == "AttachSession" || name == "AttachSessionContext" || name == "KillPane" {
			return "program-wrapper"
		}
	case "net":
		if strings.HasPrefix(name, "Listen") {
			return "listener"
		}
	case "github.com/Zen1th53/marshal/internal/hostgit":
		if name == "Command" || name == "Root" {
			return "git-program"
		}
	}
	// Git wrappers are effect dispatch sites as well as their concrete command
	// boundary. Count source sites, not executions or distinct user operations.
	if name == "git" || name == "gitCommand" || name == "runGit" {
		return "git-wrapper"
	}
	if name == "Run" {
		if receiver != nil && receiver.Name == "runner" && (strings.HasPrefix(file, "internal/adapter/") || strings.HasPrefix(file, "internal/worker/")) {
			return "program-wrapper"
		}
		if field, ok := selector.X.(*ast.SelectorExpr); ok && (strings.HasPrefix(file, "internal/adapter/") || strings.HasPrefix(file, "internal/worker/")) && (field.Sel.Name == "runner" || field.Sel.Name == "process" || field.Sel.Name == "base") {
			return "program-wrapper"
		}
		for _, arg := range call.Args {
			if literal, ok := arg.(*ast.CompositeLit); ok {
				if typ, ok := literal.Type.(*ast.SelectorExpr); ok && typ.Sel.Name == "Command" {
					if pkg, ok := typ.X.(*ast.Ident); ok && imports[pkg.Name] == "github.com/Zen1th53/marshal/internal/adapter" {
						return "program-wrapper"
					}
				}
			}
		}
	}
	if name == "ListenAndServe" || name == "ListenAndServeTLS" {
		return "listener"
	}
	if (name == "Exec" || name == "ExecContext") && (strings.HasPrefix(file, "internal/store/") || strings.HasPrefix(file, "internal/memory/")) {
		return "record-write"
	}
	switch name {
	case "Launch":
		return "agent-launch"
	case "RestoreDatabase", "RestoreDatabaseExpected", "RestoreCheckpoint", "RestoreVerified", "RestoreBackup", "RestoreState", "RollbackToCheckpoint":
		return "restore"
	case "Rollback":
		// Optimization rollback appends a durable canary status record; it does
		// not restore state. Keep its dispatch as a material record-write wrapper.
		if call, ok := selector.X.(*ast.CallExpr); ok {
			if method, ok := call.Fun.(*ast.SelectorExpr); ok && method.Sel.Name == "Optimization" {
				return "record-write-wrapper"
			}
		}
		if receiver == nil || (receiver.Name != "tx" && receiver.Name != "newTx") {
			return "restore"
		}
	}
	return ""
}

func readTable(reader io.Reader) ([]annotation, error) {
	rows, err := csv.NewReader(reader).ReadAll()
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 || strings.Join(rows[0], ",") != "file,function,effect,ordinal,source_hash,scope,guard,evidence" {
		return nil, fmt.Errorf("invalid inventory header")
	}
	var table []annotation
	for _, row := range rows[1:] {
		if len(row) != 8 {
			return nil, fmt.Errorf("invalid inventory row: %v", row)
		}
		ordinal, err := strconv.Atoi(row[3])
		if err != nil || ordinal < 1 {
			return nil, fmt.Errorf("invalid ordinal: %v", row)
		}
		table = append(table, annotation{site{row[0], row[1], row[2], row[4], ordinal, ""}, row[5], row[6], row[7]})
	}
	return table, nil
}

func validate(sites []site, table []annotation) (int, int, error) {
	reviewed := map[string]annotation{}
	for _, row := range table {
		if row.Scope != "material" && row.Scope != "support" {
			return 0, 0, fmt.Errorf("%s: invalid scope", row.Site.key())
		}
		if row.Scope == "support" && row.Guard != "none" {
			return 0, 0, fmt.Errorf("%s: support site cannot inflate coverage", row.Site.key())
		}
		switch row.Guard {
		case "gate", "approval-binding", "policy-authorisation", "sandbox", "none":
		default:
			return 0, 0, fmt.Errorf("%s: invalid guard %q", row.Site.key(), row.Guard)
		}
		if strings.TrimSpace(row.Evidence) == "" {
			return 0, 0, fmt.Errorf("%s: missing review rationale", row.Site.key())
		}
		if _, exists := reviewed[row.Site.key()]; exists {
			return 0, 0, fmt.Errorf("duplicate annotation: %s", row.Site.key())
		}
		reviewed[row.Site.key()] = row
	}
	gated, protected := 0, 0
	for _, s := range sites {
		row, ok := reviewed[s.key()]
		if !ok {
			return 0, 0, fmt.Errorf("unreviewed material effect site: %s", s.key())
		}
		if s.SupportReason != "" && (row.Scope != "support" || row.Guard != "none") {
			return 0, 0, fmt.Errorf("%s: detector requires support, none: %s", s.key(), s.SupportReason)
		}
		if row.Scope == "material" {
			if row.Guard == "gate" {
				gated++
			}
			if row.Guard != "none" {
				protected++
			}
		}
		delete(reviewed, s.key())
	}
	if len(reviewed) != 0 {
		var stale []string
		for key := range reviewed {
			stale = append(stale, key)
		}
		sort.Strings(stale)
		return 0, 0, fmt.Errorf("stale annotation: %s", stale[0])
	}
	if materialCount(table) == 0 {
		return 0, 0, fmt.Errorf("empty material effect inventory")
	}
	return gated, protected, nil
}

func materialCount(table []annotation) int {
	count := 0
	for _, row := range table {
		if row.Scope == "material" {
			count++
		}
	}
	return count
}

// Integer tenths truncate before formatting: 2/3 is 66.6%, never 66.7%.
func percentage(part, total int) string {
	tenths := part * 1000 / total
	return fmt.Sprintf("%d.%d%%", tenths/10, tenths%10)
}
func report(w io.Writer, sites []site, table []annotation) error {
	gated, protected, err := validate(sites, table)
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "Constitution enforcement: %s (%d/%d)\nScope of protection: %s (%d/%d)\n", percentage(gated, materialCount(table)), gated, materialCount(table), percentage(protected, materialCount(table)), protected, materialCount(table))
	return nil
}

func main() {
	root := flag.String("root", ".", "repository root")
	list := flag.Bool("list", false, "print discovered sites for manual review; does not write the table")
	flag.Parse()
	sites, err := discover(*root)
	if err == nil && *list {
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"file", "function", "effect", "ordinal", "source_hash", "scope", "guard", "evidence"})
		for _, s := range sites {
			scope, evidence := "material", "No mandatory gate, approval binding, policy authorisation or sandbox established at this boundary."
			if s.SupportReason != "" {
				scope, evidence = "support", s.SupportReason
			}
			_ = w.Write([]string{s.File, s.Function, s.Effect, strconv.Itoa(s.Ordinal), s.SourceHash, scope, "none", evidence})
		}
		w.Flush()
		err = w.Error()
	} else if err == nil {
		var f *os.File
		f, err = os.Open(filepath.Join(*root, "tools/effect-inventory/sites.csv"))
		if err == nil {
			var table []annotation
			table, err = readTable(f)
			_ = f.Close()
			if err == nil {
				err = report(os.Stdout, sites, table)
			}
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
