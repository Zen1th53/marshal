package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseInventory(t *testing.T) {
	root := filepath.Join("..", "..")
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open("sites.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	table, err := readTable(f)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := report(&output, sites, table); err != nil {
		t.Fatal(err)
	}
	fmt.Print(output.String())
	for _, version := range []string{"0.0.5-rc.16", "0.0.7"} {
		notes, err := os.ReadFile(filepath.Join(root, "release", "RELEASE_NOTES_"+version+".md"))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
			// Release notes publish the percentage only; the counts stay in this report.
			if i := strings.Index(line, " ("); i > 0 {
				line = line[:i]
			}
			if !strings.Contains(string(notes), line) {
				t.Errorf("%s release notes do not contain exact inventory line %q", version, line)
			}
		}
	}
}

func fixture(t *testing.T, root, path, source string) {
	t.Helper()
	target := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestDiscoveryAndNewSitesFailClosed(t *testing.T) {
	root := t.TempDir()
	source := `package example
import (execute "os/exec"; "os"; "net"; "github.com/Zen1th53/marshal/internal/hostgit")
func effects() {
 execute.Command("worker")
 hostgit.Command(nil,".","commit","-m","test")
 net.Listen("tcp","127.0.0.1:0")
 os.WriteFile("memory.json",nil,0600)
 db.ExecContext(nil,"INSERT INTO approvals VALUES (1)")
 engine.RestoreVerified(nil,"checkpoint","digest")
 driver.Launch(nil,request)
 runner.Run(nil,command)
}
var launch = func() { execute.Command("another") }
`
	fixture(t, root, "internal/store/effects.go", source)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	// Both the normal function and the package-level closure are inventory sites.
	if len(sites) != 8 {
		t.Fatalf("discovered %d sites, want 8: %+v", len(sites), sites)
	}
	var table []annotation
	for _, s := range sites {
		table = append(table, annotation{s, "material", "none", "fixture review"})
	}
	if _, _, err := validate(sites, table); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []string{
		strings.Replace(source, `execute.Command("worker")`, `execute.Command("worker"); execute.Command("new worker")`, 1),
		strings.Replace(source, `execute.Command("worker")`, `execute.Command("different worker")`, 1),
	} {
		fixture(t, root, "internal/store/effects.go", mutation)
		changed, err := discover(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err = validate(changed, table); err == nil || !strings.Contains(err.Error(), "unreviewed") {
			t.Fatalf("changed effect passed existing table: %v", err)
		}
	}
	fixture(t, root, "internal/store/effects.go", source)
	fixture(t, root, "internal/new/new.go", `package added; import "os/exec"; func launch(){ exec.Command("new") }`)
	changed, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = validate(changed, table); err == nil {
		t.Fatal("new file passed existing table")
	}
}

func TestGuardChangesRequireReview(t *testing.T) {
	root := t.TempDir()
	before := `package effect; import "os/exec"; func launch() { if !gate.Permits() { return }; exec.Command("worker") }`
	fixture(t, root, "internal/example.go", before)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	table := []annotation{{sites[0], "material", "gate", "gate.Permits refuses before launch"}}
	fixture(t, root, "internal/example.go", strings.Replace(before, `if !gate.Permits() { return }; `, "", 1))
	changed, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = validate(changed, table); err == nil {
		t.Fatal("removing gate did not require re-review")
	}
}

func TestValidationAndTruncatedPercentages(t *testing.T) {
	sites := []site{{File: "a.go", Function: "launch", Effect: "program", Ordinal: 1}, {File: "b.go", Function: "save", Effect: "filesystem", Ordinal: 1}, {File: "c.go", Function: "listen", Effect: "listener", Ordinal: 1}}
	table := []annotation{{sites[0], "material", "gate", "verdict refuses launch"}, {sites[1], "material", "sandbox", "write is confined"}, {sites[2], "material", "none", "listener has no guard"}}
	var output bytes.Buffer
	if err := report(&output, sites, table); err != nil {
		t.Fatal(err)
	}
	want := "Constitution enforcement: 33.3% (1/3)\nScope of protection: 66.6% (2/3)\n"
	if output.String() != want {
		t.Fatalf("got %q, want %q", output.String(), want)
	}
	for _, tc := range []struct {
		part, total int
		want        string
	}{{0, 3, "0.0%"}, {1, 10001, "0.0%"}, {9999, 10000, "99.9%"}, {1, 1, "100.0%"}} {
		if got := percentage(tc.part, tc.total); got != tc.want {
			t.Errorf("percentage(%d,%d)=%s", tc.part, tc.total, got)
		}
	}
	for _, mutate := range []func([]annotation) []annotation{
		func(rows []annotation) []annotation { return rows[:2] },
		func(rows []annotation) []annotation { return append(rows, rows[0]) },
		func(rows []annotation) []annotation { rows[0].Guard = "cas"; return rows },
		func(rows []annotation) []annotation { rows[0].Evidence = ""; return rows },
		func(rows []annotation) []annotation { rows[0].Scope = "support"; return rows },
	} {
		rows := mutate(append([]annotation(nil), table...))
		if _, _, err := validate(sites, rows); err == nil {
			t.Fatal("invalid annotation table passed")
		}
	}
	if _, _, err := validate(sites[:2], table); err == nil {
		t.Fatal("stale annotation passed")
	}
}

func TestExcludedSourcesAndReadOnlyFiles(t *testing.T) {
	root := t.TempDir()
	source := `package effect; import "os"; func read(){os.OpenFile("memory",os.O_RDONLY,0)}`
	fixture(t, root, "internal/read.go", source)
	for _, path := range []string{"tools/tool.go", "internal/testutil/git.go", "internal/example_test.go", "internal/testdata/example.go"} {
		fixture(t, root, path, `package effect; import "os/exec"; func launch(){exec.Command("worker")}`)
	}
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 0 {
		t.Fatalf("excluded or read-only code was counted: %+v", sites)
	}
	if _, _, err := validate(sites, nil); err == nil {
		t.Fatal("empty inventory passed")
	}
}

func TestMigrationCodeIsReviewed(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "internal/store/migrations.go", `package store; func migration(){ db.ExecContext(nil,"CREATE TABLE memory (id TEXT)") }`)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 {
		t.Fatalf("migration effect missing: %+v", sites)
	}
	if _, _, err := validate(sites, nil); err == nil {
		t.Fatal("new migration effect bypassed review")
	}
}

func TestQueryDetectionAndMutationRemainFailClosed(t *testing.T) {
	root := t.TempDir()
	source := `package example
import ("os/exec"; "github.com/Zen1th53/marshal/internal/hostgit"; "github.com/Zen1th53/marshal/internal/adapter")
func inspect() {
 hostgit.Root(".")
 hostgit.Command(nil,".","rev-parse","HEAD")
 hostgit.Command(nil,".","status","--porcelain")
 hostgit.Command(nil,".","diff","HEAD")
 hostgit.Command(nil,".","log","-1")
 hostgit.Command(nil,".","show","HEAD")
 hostgit.Command(nil,".","cat-file","-t","HEAD")
 hostgit.Command(nil,".","merge-base","HEAD","main")
 hostgit.Command(nil,".","worktree","list","--porcelain")
 hostgit.Command(nil,".","config","--get","user.name")
 hostgit.Command(nil,".","symbolic-ref","--quiet","HEAD")
 hostgit.Command(nil,".","hash-object","--",path)
 exec.CommandContext(nil,"go","list","-m","-json","all")
 exec.CommandContext(nil,binary,"--version")
 exec.Command("git","status","--porcelain")
 runner.Run(nil,adapter.Command{Path:binary,Args:[]string{"exec","--help"}})
 for _, args := range [][]string{{"diff","HEAD"},{"ls-files","--others"}} { hostgit.Command(nil,".",args...) }
}
func mutate() {
 hostgit.Command(nil,".","reset","--hard","status")
 exec.Command("git","commit","-m","status")
 hostgit.Command(nil,".","worktree","add","tree")
 hostgit.Command(nil,".","config","user.name","new")
 hostgit.Command(nil,".","symbolic-ref","HEAD","refs/heads/new")
 hostgit.Command(nil,".","hash-object","-w","file")
 hostgit.Command(nil,".","symbolic-ref","--delete","HEAD")
 hostgit.Command(nil,".",args...)
 for _, args := range [][]string{{"diff","HEAD"},{"commit","-m","change"}} { hostgit.Command(nil,".",args...) }
 runner.Run(nil,adapter.Command{Path:"/bin/sh",Args:[]string{"-c",command}})
}
`
	fixture(t, root, "internal/app/effects.go", source)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	var table []annotation
	queries, mutations := 0, 0
	for _, s := range sites {
		scope := "material"
		if s.Function == "inspect" {
			queries++
			if s.SupportReason == "" {
				t.Errorf("query counted as material: %+v", s)
			}
			scope = "support"
		} else {
			mutations++
			if s.SupportReason != "" {
				t.Errorf("mutation excluded: %+v", s)
			}
		}
		table = append(table, annotation{s, scope, "none", "fixture review"})
	}
	if queries != 17 || mutations != 10 {
		t.Fatalf("queries=%d mutations=%d", queries, mutations)
	}
	if _, _, err = validate(sites, table); err != nil {
		t.Fatal(err)
	}
	inflated := append([]annotation(nil), table...)
	for i := range inflated {
		if inflated[i].Scope == "support" {
			inflated[i].Scope = "material"
			inflated[i].Guard = "gate"
			break
		}
	}
	if _, _, err = validate(sites, inflated); err == nil {
		t.Fatal("query inflated coverage")
	}
	fixture(t, root, "internal/app/effects.go", strings.Replace(source, `"worktree","list"`, `"worktree","remove"`, 1))
	changed, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = validate(changed, table); err == nil {
		t.Fatal("query changed to mutation bypassed review")
	}
}

func TestChatPaneStopIsMaterial(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "internal/tui/switch.go", `package tui
import "github.com/Zen1th53/marshal/internal/tmux"
func switchModel() { tmux.KillPane(nil, "%chat") }
`)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].Effect != "program-wrapper" || sites[0].SupportReason != "" {
		t.Fatalf("pane stop not material: %+v", sites)
	}
	if _, _, err := validate(sites, nil); err == nil {
		t.Fatal("unreviewed chat stop passed")
	}
}

func TestContextTmuxAttachmentIsMaterial(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "internal/cli/attach.go", `package cli
import "github.com/Zen1th53/marshal/internal/tmux"
func attach() { tmux.AttachSessionContext(nil,"session",nil,nil,nil) }
`)
	sites, err := discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != 1 || sites[0].Effect != "program-wrapper" {
		t.Fatalf("context attachment escaped inventory: %#v", sites)
	}
}

func TestRootedProposalClaimInventoried(t *testing.T) {
	root := t.TempDir()
	fixture(t, root, "internal/claim.go", `package claim
import filesystem "os"
func consume(){
 project,err:=filesystem.OpenRoot("project")
 if err!=nil{return}
 fs,err:=project.OpenRoot("proposals")
 if err!=nil{return}
 fs.Rename("request.json","request.json.consumed")
}`)
	sites, err := discover(root)
	if err != nil || len(sites) != 1 || sites[0].Effect != "filesystem" {
		t.Fatalf("sites=%v err=%v", sites, err)
	}
}
