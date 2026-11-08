package statepath

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNamespaceAliasesRefusedBeforeConsumption(t *testing.T) {
	for _, kind := range []string{"directory", "child", "file", "hardlink", "renamed-hardlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			other := filepath.Join(root, OtherDirName)
			if err := os.MkdirAll(filepath.Join(other, "inbox"), 0700); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(other, "state.db")
			if err := os.WriteFile(sentinel, []byte("foreign-state-unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			if kind == "directory" {
				err = os.Symlink(other, Path(root))
			} else {
				if err = os.MkdirAll(Path(root), 0700); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "child":
					err = os.Symlink(filepath.Join(other, "inbox"), Path(root, "inbox"))
				case "file":
					err = os.Symlink(sentinel, Path(root, "state.db"))
				case "renamed-hardlink":
					renamed := filepath.Join(other, "backup.db")
					if err = os.Rename(sentinel, renamed); err != nil {
						t.Fatal(err)
					}
					sentinel = renamed
					err = os.Link(sentinel, Path(root, "state.db"))
				case "hardlink":
					err = os.Link(sentinel, Path(root, "state.db"))
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if Validate(root) == nil {
				t.Fatal("state alias accepted")
			}
			target := Path(root, "state.db")
			if kind == "child" {
				target = Path(root, "inbox", "codex.md")
			}
			if CheckPath(root, target) == nil {
				t.Fatal("native callback alias accepted")
			}
			data, err := os.ReadFile(sentinel)
			if err != nil || string(data) != "foreign-state-unchanged" {
				t.Fatal("foreign state was changed")
			}
		})
	}
}

func TestNamespaceValidationKeepsTaskSymlinksAndRejectsForeignPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Path(root, "worktrees", "task"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/synthetic-missing-user-target", Path(root, "worktrees", "task", "link")); err != nil {
		t.Fatal(err)
	}
	if err := Validate(root); err != nil {
		t.Fatal(err)
	}
	if CheckPath(root, filepath.Join(root, OtherDirName, "state.db")) == nil {
		t.Fatal("foreign namespace path accepted")
	}
	for _, dir := range []string{".marshal", ".marshal-extreme"} {
		if !Reserved(dir) || !Reserved(dir+"/state.db") || Reserved(dir+"-source/file.go") {
			t.Fatal("reserved path boundary incorrect")
		}
	}
}

func TestMachineConfigAliasRefused(t *testing.T) {
	base := t.TempDir()
	other := filepath.Join(base, "marshal")
	if Name == "marshal" {
		other = filepath.Join(base, "marshal-extreme")
	}
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, filepath.Join(base, Name)); err != nil {
		t.Fatal(err)
	}
	if ValidateConfig(base) == nil {
		t.Fatal("installation identity alias accepted")
	}
}

// Concurrent writers create and remove journals and atomic-rename temporaries
// inside the namespace. An entry that vanishes during validation is not an
// alias and must not make the namespace read as invalid.
func TestValidateToleratesEntriesRemovedDuringWalk(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(Path(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			for _, dir := range []string{Path(root), Path(root, "nested")} {
				name := filepath.Join(dir, "churn-"+string(rune('a'+i%26))+".tmp")
				_ = os.WriteFile(name, nil, 0600)
				_ = os.Remove(name)
			}
		}
	}()
	defer func() { close(stop); <-done }()
	for i := 0; i < 2000; i++ {
		if err := Validate(root); err != nil {
			t.Fatalf("validation failed on a concurrently removed entry: %v", err)
		}
	}
}
