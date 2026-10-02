//go:build linux

package tui

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/adapter/codex"
	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/projectid"
)

func TestSkillInstallCompletionProvenance(t *testing.T) {
	c := NewCompleter(CompletionContext{InstallableSkills: func() []string { return []string{"project-only"} }})
	for _, line := range []string{"/skill install ", "/codex skill install "} {
		_, matches := c.Suggest(line, len(line))
		if len(matches) != 1 || strings.TrimSpace(matches[0]) != "project-only" {
			t.Fatalf("%q matches=%q", line, matches)
		}
	}
	var b strings.Builder
	skills := []codex.SkillInfo{{Name: "same", SourceType: "project-agents", Path: "/project/same/SKILL.md"}, {Name: "same", SourceType: "home-codex", Path: "/home/same/SKILL.md"}}
	for _, skill := range skills {
		b.WriteString(skillSourceLine(skill))
	}
	writeSkillInstallHint(&b, skills)
	if strings.Count(b.String(), "same source=") != 2 || strings.Contains(b.String(), "install <name>") || !strings.Contains(b.String(), "not installable") {
		t.Fatal(b.String())
	}
}

func TestSkillCompletionUsesCanonicalInventory(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	root := initProject(t, bin)
	write := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, ".agents", "skills", "project-only"))
	write(filepath.Join(root, ".agents", "skills", "same"))
	write(filepath.Join(os.Getenv("HOME"), ".codex", "skills", "home-only"))
	write(filepath.Join(os.Getenv("HOME"), ".codex", "skills", "same"))
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ws := NewWorkspace(rt.Store(), rt.ProjectID(), "skill-completion")
	ws.AttachRuntime(rt, projectid.ID(rt.ProjectID()))
	// Even a matching canonical task ID must never escape into install completion.
	ws.completer.ctx.Tasks = []string{"home-only"}
	for _, prefix := range []string{"/skill install ", "/codex skill install "} {
		_, matches := ws.completer.Suggest(prefix, len(prefix))
		if len(matches) != 1 || strings.TrimSpace(matches[0]) != "project-only" {
			t.Fatalf("matches=%q", matches)
		}
		line := prefix + "home"
		_, matches = ws.completer.Suggest(line, len(line))
		if len(matches) != 0 {
			t.Fatalf("HOME/task suggestions=%q", matches)
		}
	}
	// An unsafe project tree is rejected by preview, not guessed installable.
	if err := os.Symlink("SKILL.md", filepath.Join(root, ".agents", "skills", "project-only", "link")); err != nil {
		t.Fatal(err)
	}
	line := "/skill install "
	_, matches := ws.completer.Suggest(line, len(line))
	if len(matches) != 0 {
		t.Fatalf("unsafe suggestions=%q", matches)
	}
}

func TestSkillSourcesPTY(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	sweepAgentCodexDouble(t)
	root := initProject(t, bin)
	write := func(dir string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, ".agents", "skills", "project-only"))
	write(filepath.Join(os.Getenv("HOME"), ".codex", "skills", "home-only"))
	s := startFrozenTUIInProject(t, 50, 240, bin, root, "tui")
	command := func(line string) { s.send(line); s.send("\x1b"); s.send("\r") }
	command("/skills")
	s.mustSee("project-only source=project-agents")
	s.mustSee("home-only source=home-codex")
	s.mustSee("(installable)")
	s.mustSee("(not installable)")
	write(filepath.Join(os.Getenv("HOME"), ".codex", "skills", "project-only"))
	command("/codex skills")
	s.mustSee("project-only source=home-codex")
	command("/skill install project-only")
	s.mustSee("ambiguous skill")
	s.mustSee("sources:")
	if err := os.RemoveAll(filepath.Join(os.Getenv("HOME"), ".codex", "skills")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(os.Getenv("HOME"), ".codex", "skills"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	command("/skills")
	s.mustSee("local skill discovery failed")
	s.mustSee("project-only source=project-agents")
}

func TestSkillSourcesDoNotExposeLocalPaths(t *testing.T) {
	bin := buildMarshalBinary(t)
	sweepWorkEnvironment(t)
	sweepAgentCodexDouble(t)
	// Successful plugin JSON ensures the healthy snapshot actually carries skills.
	if err := os.WriteFile(filepath.Join(os.Getenv("PATH"), "codex"), []byte("#!/bin/sh\ncase \"$1:$2\" in\n --version:*) echo privacy-provider;;\n plugin:list) echo '{\"installed\":[]}';;\n *) echo '[]';;\nesac\n"), 0700); err != nil {
		t.Fatal(err)
	}

	root := initProject(t, bin)
	home := os.Getenv("HOME")
	for _, base := range []string{filepath.Join(root, ".agents", "skills"), filepath.Join(home, ".codex", "skills"), filepath.Join(home, ".gemini", "config", "skills"), filepath.Join(home, ".codex", ".tmp", "marketplaces", "ecc", ".agents", "skills")} {
		dir := filepath.Join(base, "same")
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rt, err := app.Open(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	ws := NewWorkspace(rt.Store(), rt.ProjectID(), "skill-path-privacy")
	ws.AttachRuntime(rt, projectid.ID(rt.ProjectID()))
	check := func(label, value string) {
		t.Helper()
		for _, path := range []string{home, root} {
			if strings.Contains(value, path) {
				t.Fatalf("%s exposed local path: %s", label, value)
			}
		}
	}
	for _, degraded := range []bool{false, true} {
		if degraded {
			catalog := filepath.Join(home, ".codex", "skills")
			if err := os.RemoveAll(catalog); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(catalog, []byte("broken"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for _, args := range [][]string{{"skills"}, {"skill", "install", "same"}, {"skill", "install", "missing"}} {
			output, err := (&CommandHandler{ws: ws}).handleCodex(context.Background(), args, "/codex "+strings.Join(args, " "))
			check("rendered command", output)
			if !degraded && args[0] == "skills" {
				for _, category := range []string{"project-agents", "home-codex", "home-gemini", "home-marketplace"} {
					if !strings.Contains(output, "source="+category) {
						t.Fatalf("missing category %s: %s", category, output)
					}
				}
			}
			if err != nil {
				check("command error", err.Error())
			}
		}
		authority := ws.controlSource().Authority.(*runtimeControlAuthority)
		snap := (&ModelsFeed{Reader: authority, ProjectID: rt.ProjectID()}).ReadModels(context.Background())
		if !degraded && len(snap.CodexSkills) != 4 {
			t.Fatalf("snapshot omitted skills: %+v", snap.CodexSkills)
		}

		raw, err := json.Marshal(snap)
		if err != nil {
			t.Fatal(err)
		}
		check("ModelsSnapshot", string(raw))
		skills, err := rt.ProjectCodexSkills()
		raw, marshalErr := json.Marshal(skills)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		check("inventory", string(raw))
		if err != nil {
			check("discovery error", err.Error())
		}
	}
}
