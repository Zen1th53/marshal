package codeevidence

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/code-evidence/evidence"
)

func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func write(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

const ledgerV1 = `package ledger

// Balance returns the account balance in cents.
func Balance(entries []int) int {
	total := 0
	for _, e := range entries {
		total += e
	}
	return total
}
`

const ledgerV2 = `package ledger

// Balance returns the account balance in cents, ignoring refunds.
func Balance(entries []int) int {
	total := 0
	for _, e := range entries {
		if e > 0 {
			total += e
		}
	}
	return total
}
`

// fixture is a repository with a ledger package, a secret and a state folder.
func fixture(t *testing.T) (repo, state string, base string) {
	t.Helper()
	repo = t.TempDir()
	state = filepath.Join(t.TempDir(), ".marshal-extreme")
	gitT(t, repo, "init", "-q", "-b", "main")
	write(t, repo, "go.mod", "module example.com/fx\n\ngo 1.25\n")
	write(t, repo, "ledger/ledger.go", ledgerV1)
	write(t, repo, "report/report.go", "package report\n\nimport \"example.com/fx/ledger\"\n\n// Total prints the Balance.\nfunc Total() int { return ledger.Balance(nil) }\n")
	write(t, repo, "ledger/secrets/token.go", "package secrets\n\nconst Balance = \"sk-not-a-real-token\"\n")
	write(t, repo, "ledger/session.jsonl", "{\"Balance\":1}\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-q", "-m", "base")
	return repo, state, gitT(t, repo, "rev-parse", "HEAD")
}

func TestPolicyScopeAndPrivacy(t *testing.T) {
	pol, err := Policy([]string{"ledger/", "report/report.go"})
	if err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{
		"ledger/ledger.go":         true,
		"ledger/sub/x.go":          true,
		"report/report.go":         true,
		"report/other.go":          false,
		"ledgerx/a.go":             false,
		"main.go":                  false,
		"ledger/secrets/token.go":  false,
		"ledger/session.jsonl":     false,
		"ledger/.env":              false,
		"ledger/.marshal/x.go":     false,
		"ledger/vendor/v.go":       false,
		"ledger/server.pem":        false,
		"ledger/.claude/notes.md":  false,
		"ledger/credentials.json":  false,
		"ledger/transcripts/a.txt": false,
	} {
		if got := pol.Visible(p); got != want {
			t.Errorf("Visible(%q) = %v, want %v", p, got, want)
		}
	}
	again, _ := Policy([]string{"report/report.go", "ledger"})
	if again.ID() != pol.ID() {
		t.Fatal("equal scopes must give equal policy ids")
	}
	other, _ := Policy([]string{"ledger"})
	if other.ID() == pol.ID() {
		t.Fatal("different scopes must give different policy ids")
	}
	for _, bad := range [][]string{nil, {}, {"../x"}, {"/abs"}, {"a/*"}, {"a\\b"}, {"."}} {
		if _, err := Policy(bad); err == nil {
			t.Errorf("Policy(%q) accepted", bad)
		}
	}
	// Capture and output checks share the same project-knowledge predicate.
	if err := checkPaths("ledger/ledger.go"); err != nil {
		t.Fatal(err)
	}
	if err := checkPaths("ledger/Secrets/x.go"); err != ErrPrivacy {
		t.Fatalf("checkPaths(case variant) = %v", err)
	}
}

func TestSettings(t *testing.T) {
	h := Host{StateDir: t.TempDir()}
	s, err := h.LoadSettings()
	if err != nil || s != DefaultSettings() || s.Push != PushOff || s.OnInvalidated != OnInvalidatedAdvisory {
		t.Fatalf("defaults: %+v %v", s, err)
	}
	write(t, h.Dir(), SettingsFile, `{"push":"gated","on_invalidated":"return","profile":"codex"}`)
	if s, err = h.LoadSettings(); err != nil || s.Push != PushGated || s.OnInvalidated != OnInvalidatedReturn || s.Profile != "codex" {
		t.Fatalf("load: %+v %v", s, err)
	}
	for _, bad := range []string{`{"push":"always"}`, `{"on_invalidated":"block"}`, `{"pushh":"off"}`, `not json`} {
		write(t, h.Dir(), SettingsFile, bad)
		if s, err = h.LoadSettings(); err == nil || s != DefaultSettings() {
			t.Errorf("%s: %+v %v", bad, s, err)
		}
	}
}

// TestReceiptValidatesAgainstChangedCommit is the adapter-level core of the
// cross-task check: evidence on ledger.go recorded at the base, then another
// commit changes ledger.go, and validation of the receipt against that commit
// reports it invalidated, while the base commit still holds.
func TestReceiptValidatesAgainstChangedCommit(t *testing.T) {
	repo, state, base := fixture(t)
	h := Host{StateDir: state, Repository: repo}
	scope := []string{"ledger"}
	ctx := context.Background()
	d, err := h.Context(ctx, repo, scope, "fix Balance to ignore refunds", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if d.Receipt == nil || d.Receipt.ID == "" || len(d.Text) > MaxContextBytes {
		t.Fatalf("dossier: %+v", d)
	}
	if strings.Contains(d.Text, "sk-not-a-real-token") || strings.Contains(d.Text, "report.go") || strings.Contains(d.Text, "session.jsonl") {
		t.Fatalf("dossier leaked outside the scope:\n%s", d.Text)
	}
	if !strings.Contains(d.Text, "ledger/ledger.go") {
		t.Fatalf("dossier misses the scoped file:\n%s", d.Text)
	}
	if strings.HasPrefix(h.StoreDir(), repo) {
		t.Fatal("store inside the repository")
	}
	x, err := h.Expand(ctx, repo, scope, evidence.ExpandRequest{Receipt: d.Receipt.ID})
	if err != nil || x.Receipt != d.Receipt.ID || len(x.Text) > MaxExpandBytes {
		t.Fatalf("expand: %+v %v", x, err)
	}
	if _, err := h.Expand(ctx, repo, scope, evidence.ExpandRequest{Receipt: d.Receipt.ID, Select: "report/report.go:1-3"}); err == nil {
		t.Fatal("expand reached a path outside the scope")
	}
	if _, err := h.Expand(ctx, repo, []string{"report"}, evidence.ExpandRequest{Receipt: d.Receipt.ID}); err == nil {
		t.Fatal("receipt authorized under another scope")
	}

	same, err := h.ValidateCommit(ctx, base, scope, []string{d.Receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if s := Summarize(same); s.Invalidated != 0 || s.Holds == 0 || s.Receipts != 1 {
		t.Fatalf("base: %+v\n%s", s, same.Text)
	}

	write(t, repo, "ledger/ledger.go", ledgerV2)
	gitT(t, repo, "commit", "-q", "-am", "other task changes Balance")
	changed := gitT(t, repo, "rev-parse", "HEAD")
	rep, err := h.ValidateCommit(ctx, changed, scope, []string{d.Receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	s := Summarize(rep)
	if s.Invalidated == 0 || len(s.Paths) == 0 || s.Paths[0] != "ledger/ledger.go" {
		t.Fatalf("changed: %+v\n%s", s, rep.Text)
	}
	if !strings.Contains(s.Text(), "invalidated") {
		t.Fatal(s.Text())
	}
	entries, _ := os.ReadDir(h.Dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "tree-") {
			t.Fatalf("materialized tree left behind: %s", e.Name())
		}
	}
}

func TestMaterializeBoundsAndSafety(t *testing.T) {
	repo, state, base := fixture(t)
	h := Host{StateDir: state, Repository: repo}
	pol, _ := Policy([]string{"ledger"})
	dir, cleanup, err := h.Materialize(context.Background(), base, pol)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger", "ledger.go")); err != nil {
		t.Fatal(err)
	}
	for _, hidden := range []string{"report/report.go", "ledger/secrets/token.go", "ledger/session.jsonl", "go.mod"} {
		if _, err := os.Stat(filepath.Join(dir, hidden)); err == nil {
			t.Errorf("materialized invisible file %s", hidden)
		}
	}
	cleanup()
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("cleanup left the tree")
	}
	for _, bad := range []string{"HEAD", "main", base[:12], strings.ToUpper(base), "--output=/tmp/x"} {
		if _, _, err := h.Materialize(context.Background(), bad, pol); err == nil {
			t.Errorf("Materialize(%q) accepted", bad)
		}
	}
}

func TestSummarizeCounts(t *testing.T) {
	rep := &evidence.ValidationReport{Receipts: []evidence.ReceiptVerdict{
		{Status: "checked",
			Spans:      []evidence.SpanVerdict{{Path: "a.go", Verdict: evidence.SpanUnchanged}, {Path: "b.go", Verdict: evidence.SpanChanged}, {Path: "c.go", Verdict: evidence.SpanRemoved}, {Path: "d.go", Verdict: evidence.SpanUnknown}},
			Predicates: []evidence.PredVerdict{{Verdict: evidence.PredHolds}, {Verdict: evidence.PredInvalidated, Anchors: []evidence.Anchor{{Path: "e.go"}}}, {Verdict: evidence.PredUnknown}}},
		{Status: "not_found"},
	}}
	s := Summarize(rep)
	if s.Receipts != 2 || s.Holds != 2 || s.Invalidated != 3 || s.Own != 0 || s.Unknown != 3 || strings.Join(s.Paths, ",") != "b.go,c.go,e.go" {
		t.Fatalf("%+v", s)
	}
	own := SummarizeAgainst(rep, []string{"b.go", "e.go"})
	if own.Invalidated != 1 || own.Own != 2 || strings.Join(own.Paths, ",") != "c.go" || !strings.Contains(own.Text(), "2 changed by the task itself") {
		t.Fatalf("%+v %s", own, own.Text())
	}
}

func TestPrivacyNoninterferenceBeforeCaptureAndMaterialization(t *testing.T) {
	repo, state, _ := fixture(t)
	h := Host{StateDir: state, Repository: repo}
	ctx := context.Background()
	scope := []string{"ledger"}
	before, err := h.Context(ctx, repo, scope, "Balance", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	denied := []string{"ledger/Secrets/caller.go", "ledger/CREDENTIALS.go", "ledger/.ENV", "ledger/Sessions/caller.go", "ledger/TRANSCRIPT.go", "ledger/ROLLOUT-a.go", "ledger/ID_RSA.go", "ledger/key.PEM"}
	for _, p := range denied {
		write(t, repo, p, "package ledger\nfunc Hidden() int { return Balance(nil) }\n")
	}
	after, err := h.Context(ctx, repo, scope, "Balance", "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if before.Receipt.Snapshot != after.Receipt.Snapshot || strings.ReplaceAll(before.Text, before.Receipt.ID, "receipt") != strings.ReplaceAll(after.Text, after.Receipt.ID, "receipt") {
		t.Fatal("denied files changed snapshot, universe or dossier")
	}
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-qm", "denied files")
	pol, err := Policy(scope)
	if err != nil {
		t.Fatal(err)
	}
	dir, cleanup, err := h.Materialize(ctx, gitT(t, repo, "rev-parse", "HEAD"), pol)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	for _, p := range denied {
		if _, err := os.Stat(filepath.Join(dir, p)); !os.IsNotExist(err) {
			t.Fatalf("materialized denied path %s: %v", p, err)
		}
	}
	rep, err := h.ValidateCommit(ctx, gitT(t, repo, "rev-parse", "HEAD"), scope, []string{before.Receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sum := Summarize(rep); sum.Invalidated != 0 || sum.Unknown != 0 {
		t.Fatalf("denied files changed validation: %+v", sum)
	}
	encoded, err := json.Marshal(rep)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range denied {
		if strings.Contains(string(encoded), p) || strings.Contains(after.Text, p) {
			t.Fatalf("denied path in output: %s", p)
		}
	}
}

func TestExpandedLocatorFactsValidateThroughHost(t *testing.T) {
	ctx := context.Background()
	repo, state, _ := fixture(t)
	h := Host{StateDir: state, Repository: repo}
	scope := []string{"ledger"}
	d, err := h.Context(ctx, repo, scope, "Balance", evidence.RecipeNameLocator, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Entries) == 0 {
		t.Fatal("empty locator")
	}
	for _, view := range []string{"source", "callers"} {
		if _, err := h.Expand(ctx, repo, scope, evidence.ExpandRequest{Receipt: d.Receipt.ID, Select: d.Entries[0].Symbol, View: view}); err != nil {
			t.Fatal(err)
		}
	}
	write(t, repo, "ledger/ledger.go", strings.Replace(ledgerV1, "total += e", "total -= e", 1))
	write(t, repo, "ledger/caller.go", "package ledger\nfunc Caller() int { return Balance(nil) }\n")
	gitT(t, repo, "add", "-A")
	gitT(t, repo, "commit", "-qm", "change expanded facts")
	rep, err := h.ValidateCommit(ctx, gitT(t, repo, "rev-parse", "HEAD"), scope, []string{d.Receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	body, callers := false, false
	for _, r := range rep.Receipts {
		for _, s := range r.Spans {
			body = body || s.Verdict == evidence.SpanChanged
		}
		for _, p := range r.Predicates {
			callers = callers || (p.Relation == "callers" && p.Verdict == evidence.PredInvalidated)
		}
	}
	if !body || !callers {
		t.Fatalf("expanded facts not checked: body=%v callers=%v report=%+v", body, callers, rep)
	}
}

func TestValidateCommitRejectsMissingIdentityAndCancellation(t *testing.T) {
	h := Host{}
	if _, err := h.ValidateCommit(context.Background(), "", nil, nil); err == nil {
		t.Fatal("empty commit passed")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := h.ValidateCommit(ctx, strings.Repeat("a", 40), nil, nil); err == nil {
		t.Fatal("canceled validation passed")
	}
}

func TestLocatorWithoutDeliveredFactsReportsUnknown(t *testing.T) {
	repo, state, _ := fixture(t)
	if err := os.Remove(filepath.Join(repo, "ledger/ledger.go")); err != nil {
		t.Fatal(err)
	}
	h := Host{StateDir: state, Repository: repo}
	d, err := h.Context(context.Background(), repo, []string{"ledger"}, "Balance", evidence.RecipeNameLocator, "", 0)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := h.ValidateTree(context.Background(), repo, []string{"ledger"}, []string{d.Receipt.ID})
	if err != nil {
		t.Fatal(err)
	}
	if sum := Summarize(rep); sum.Unknown != 1 || sum.Holds != 0 {
		t.Fatalf("empty locator treated as proof: %+v", sum)
	}
}
