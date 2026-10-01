package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

func TestProjectCodexSkillProvenance(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", t.TempDir())
	r := &Runtime{layout: project.Layout{Root: root}}
	write := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	projectDir := filepath.Join(root, ".agents", "skills", "project-only")
	write(projectDir)
	write(filepath.Join(home, ".codex", "skills", "home-only"))
	skills, err := r.ProjectCodexSkills()
	if err != nil || len(skills) != 2 {
		t.Fatalf("skills=%+v err=%v", skills, err)
	}
	for _, skill := range skills {
		if skill.Installable != (skill.Name == "project-only") || skill.Path != "" || skill.Root != "" || skill.SourceType == "" {
			t.Fatalf("provenance=%+v", skill)
		}
	}
	digest, err := r.PreviewProjectCodexSkill("project-only")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "SKILL.md"), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	// Open a store only for the mutation test; no providers are launched.
	repo := runtimeRepo(t)
	if _, err := Bootstrap(context.Background(), repo.Path()); err != nil {
		t.Fatal(err)
	}
	opened, err := Open(context.Background(), repo.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	r.store = opened.store
	if _, err := r.InstallProjectCodexSkill(context.Background(), "project-only", digest); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("stale install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("CODEX_HOME"), "skills", "project-only")); !os.IsNotExist(err) {
		t.Fatalf("stale source installed: %v", err)
	}
	write(filepath.Join(home, ".codex", "skills", "project-only"))
	skills, err = r.ProjectCodexSkills()
	if err != nil || len(skills) != 3 {
		t.Fatalf("duplicates=%+v err=%v", skills, err)
	}
	for _, skill := range skills {
		if skill.Installable {
			t.Fatalf("ambiguous installable: %+v", skill)
		}
	}
	for _, preview := range []bool{true, false} {
		if preview {
			_, err = r.PreviewProjectCodexSkill("project-only")
		} else {
			_, err = r.InstallProjectCodexSkill(context.Background(), "project-only", digest)
		}
		if !errors.Is(err, model.ErrConflict) || !strings.Contains(err.Error(), "project-agents") || !strings.Contains(err.Error(), "home-codex") {
			t.Fatalf("ambiguity missing sources: %v", err)
		}
	}
	if _, err := r.PreviewProjectCodexSkill("home-only"); err == nil {
		t.Fatal("HOME-only preview accepted")
	}
}

func TestProjectCodexSkillsAlreadyInstalled(t *testing.T) {
	root, home, destination := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", destination)
	for _, dir := range []string{filepath.Join(root, ".agents", "skills", "installed"), filepath.Join(destination, "skills", "installed")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	r := &Runtime{layout: project.Layout{Root: root}}
	skills, err := r.ProjectCodexSkills()
	if err != nil || len(skills) != 1 || skills[0].Installable {
		t.Fatalf("installed=%+v err=%v", skills, err)
	}
	if _, err := r.PreviewProjectCodexSkill("installed"); !errors.Is(err, model.ErrConflict) {
		t.Fatalf("overwrite preview: %v", err)
	}
}

func TestProjectCodexSkillsDiscoveryFailure(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(root, ".agents", "skills", "visible")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".codex", "skills"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	r := &Runtime{layout: project.Layout{Root: root}}
	skills, err := r.ProjectCodexSkills()
	if err == nil || len(skills) != 1 || skills[0].Installable {
		t.Fatalf("partial discovery=%+v err=%v", skills, err)
	}
	if _, err := r.PreviewProjectCodexSkill("visible"); err == nil {
		t.Fatal("incomplete discovery allowed preview")
	}
}

func TestProjectCodexSkillFilesystemErrorsArePrivate(t *testing.T) {
	root, home := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(root, ".agents", "skills", "visible")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
		t.Fatal(err)
	}
	blocked := filepath.Join(home, "blocked")
	if err := os.WriteFile(blocked, []byte("file"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", blocked)
	r := &Runtime{layout: project.Layout{Root: root}}
	_, err := r.PreviewProjectCodexSkill("visible")
	if err == nil {
		t.Fatal("invalid destination accepted")
	}
	for _, path := range []string{home, root, blocked} {
		if strings.Contains(err.Error(), path) {
			t.Fatalf("filesystem path escaped: %v", err)
		}
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		t.Fatalf("filesystem path retained in error: %+v", pathErr)
	}
	skills, discoveryErr := r.ProjectCodexSkills()
	if discoveryErr != nil || len(skills) != 1 || skills[0].Installable {
		t.Fatalf("inventory=%+v err=%v", skills, discoveryErr)
	}
}
