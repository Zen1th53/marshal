//go:build !governed

package statepath

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestNewProjectsUseMarshal(t *testing.T) {
	root := t.TempDir()
	if err := Prepare(root); err != nil {
		t.Fatal(err)
	}
	if got := Path(root, "state.db"); got != filepath.Join(root, ".marshal", "state.db") {
		t.Fatal(got)
	}
	if _, err := os.Stat(filepath.Join(root, ".marshal-extreme")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestFormerNamespaceMigration(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, ".marshal-extreme")
	if err := os.MkdirAll(old, 0700); err != nil {
		t.Fatal(err)
	}
	original := `{"artifact":"` + old + `/artifacts/report"}`
	if err := os.WriteFile(filepath.Join(old, "session.json"), []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(old, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`CREATE TABLE sessions (path TEXT, body TEXT, number INTEGER); INSERT INTO sessions VALUES (?, ?, 7)`, old+"/worktrees/task", original); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := Prepare(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old namespace remains", err)
	}
	body, err := os.ReadFile(Path(root, "session.json"))
	if err != nil || strings.Contains(string(body), old) || !strings.Contains(string(body), Path(root, "artifacts")) {
		t.Fatal(string(body), err)
	}
	backup, err := os.ReadFile(Path(root, "session.json.namespace-backup"))
	if err != nil || string(backup) != original {
		t.Fatal("original not retained", err)
	}
	db, err = sql.Open("sqlite", Path(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var path, blob string
	var n int
	if err := db.QueryRow("SELECT path,body,number FROM sessions").Scan(&path, &blob, &n); err != nil || path != Path(root, "worktrees/task") || strings.Contains(blob, old) || n != 7 {
		t.Fatal(path, blob, n, err)
	}
	if err := Prepare(root); err != nil {
		t.Fatal("repeat", err)
	}
}

func TestBothNamespacesPreserved(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".marshal", ".marshal-extreme"} {
		if err := os.Mkdir(filepath.Join(root, name), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name, "keep"), []byte(name), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prepare(root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".marshal", ".marshal-extreme"} {
		b, err := os.ReadFile(filepath.Join(root, name, "keep"))
		if err != nil || string(b) != name {
			t.Fatal(name, err)
		}
	}
}

func TestNamespaceMigrationRefusesLiveOwner(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, ".marshal-extreme")
	os.Mkdir(old, 0700)
	lock, err := os.OpenFile(filepath.Join(old, "runtime-owner.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	if err := Prepare(root); err == nil || !strings.Contains(err.Error(), "running Marshal") {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationFailureResumesWithoutDataLoss(t *testing.T) {
	root := t.TempDir()
	old := filepath.Join(root, ".marshal-extreme")
	os.Mkdir(old, 0700)
	path := filepath.Join(old, "binary")
	os.WriteFile(path, append([]byte(old), 0), 0600)
	if err := Prepare(root); err == nil {
		t.Fatal("accepted binary path")
	}
	if err := Validate(root); err == nil {
		t.Fatal("accepted incomplete migration")
	}
	if _, err := os.Stat(Path(root, "binary")); err != nil {
		t.Fatal("data lost", err)
	}
	os.WriteFile(Path(root, "binary"), []byte("manually repaired"), 0600)
	if err := Prepare(root); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseHostAndHomeProjectRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, root := range []string{"/", home, filepath.Join(home, ".")} {
		if err := ValidateRoot(root); err == nil || !strings.Contains(err.Error(), "without a sandbox") {
			t.Fatal(root, err)
		}
	}
	alias := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(home, alias); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRoot(alias); err == nil {
		t.Fatal("accepted home alias")
	}
	if err := ValidateRoot(filepath.Join(home, "project")); err != nil {
		t.Fatal(err)
	}
}

func TestRelocationPreservesSiblingPaths(t *testing.T) {
	old := "/project/.marshal-extreme"
	source := []byte(old + "/state.db " + old + "-backup/state.db")
	want := "/project/.marshal/state.db " + old + "-backup/state.db"
	if got := string(relocatePaths(source, old, "/project/.marshal")); got != want {
		t.Fatal(got)
	}
	root := t.TempDir()
	path := filepath.Join(root, "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE paths (body TEXT); INSERT INTO paths VALUES (?)", string(source)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if err := relocateSQLite(path, old, "/project/.marshal"); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow("SELECT body FROM paths").Scan(&got); err != nil || got != want {
		t.Fatal(got, err)
	}
}
