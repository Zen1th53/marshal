package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/Zen1th53/marshal/internal/model"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/policy"
	"github.com/Zen1th53/marshal/internal/project"
	"github.com/Zen1th53/marshal/internal/statepath"
	"github.com/Zen1th53/marshal/internal/store"
	"go.yaml.in/yaml/v3"
)

// ValidateProject inspects an existing initialization without bootstrapping it.
// Both the launcher and Open use this gate before any runtime writes. Bootstrap
// remains the explicit init path that creates defaults and canonical state.
func ValidateProject(ctx context.Context, root string) (project.Layout, error) {
	layout, err := project.Discover(root)
	if err != nil {
		// A folder whose repository is the home folder or / is told why in
		// plain words; the refused root itself gets the refusal.
		var refused *statepath.RefusedRootError
		if errors.As(err, &refused) {
			if statepath.ValidateRoot(root) != nil {
				return project.Layout{}, refused
			}
			return project.Layout{}, errors.New(refused.InsideReason())
		}
		if errors.Is(err, project.ErrStatePreparation) {
			return project.Layout{}, fmt.Errorf("cannot open Marshal project in %q: %w", root, err)
		}
		return project.Layout{}, errors.New("This folder isn't a Git project with a first commit yet. Run `git init`, make a first commit, then run `marshal init`. Or cd into your project.")
	}
	var problems []string
	for _, name := range append(append([]string{}, defaultProjectFiles...), filepath.Join(filepath.Base(layout.RuntimeDir), "state.db")) {
		info, err := os.Stat(filepath.Join(layout.Root, name))
		if os.IsNotExist(err) {
			problems = append(problems, "missing "+name)
		} else if err != nil || !info.Mode().IsRegular() {
			problems = append(problems, "unreadable or invalid "+name)
		}
	}
	// Report all absent files together, without even opening SQLite.
	if len(problems) == 0 {
		if _, err := loadPackVersion(filepath.Join(layout.Root, "PACK-VERSION.yaml")); err != nil {
			problems = append(problems, "invalid PACK-VERSION.yaml: "+err.Error())
		}
		data, err := os.ReadFile(filepath.Join(layout.Root, "RUNTIME-VERSION.yaml"))
		var version struct {
			Schema int    `yaml:"schema_version"`
			Spec   string `yaml:"runtime_spec_version"`
		}
		if err != nil || yaml.Unmarshal(data, &version) != nil || version.Schema != 1 || version.Spec == "" {
			problems = append(problems, "invalid RUNTIME-VERSION.yaml")
		}
		if _, err := policy.Load(filepath.Join(layout.Root, "CAPABILITIES.yaml")); err != nil {
			problems = append(problems, "invalid CAPABILITIES.yaml: "+err.Error())
		}
		if len(problems) == 0 {
			if err := store.ValidateInitialized(ctx, layout.Database); err != nil {
				problems = append(problems, "invalid "+filepath.Join(filepath.Base(layout.RuntimeDir), "state.db")+": "+err.Error())
			}
		}
	}
	if len(problems) != 0 {
		msg := "Some of Marshal's files in this project are damaged. Run `marshal doctor` to see which."
		allMissing := true
		for _, p := range problems {
			if !strings.HasPrefix(p, "missing ") {
				allMissing = false
				break
			}
		}
		// Without canonical state the project was never set up in this
		// clone: opening Marshal sets it up, as `marshal init` would. A
		// project that has its state but lost a file is set up again only
		// on request, since init would replace the lost file with a default.
		_, stateErr := os.Stat(layout.Database)
		notSetUp := allMissing && os.IsNotExist(stateErr)
		switch {
		case notSetUp:
			msg = "Marshal isn't set up in this project yet. Run `marshal` here and it sets the project up."
		case allMissing:
			msg = "Marshal isn't set up in this project yet. Run `marshal init` here."
		}
		return project.Layout{}, projectNotReadyError{msg: msg, detail: strings.Join(problems, "; "), notSetUp: notSetUp}
	}
	return layout, nil
}

// projectNotReadyError keeps the operator-facing message plain while remaining
// an invalid-input error for callers that match model.ErrInvalid. detail keeps
// the technical problem list for the log; it is never printed.
type projectNotReadyError struct {
	msg    string
	detail string
	// notSetUp: nothing is damaged and the project has no canonical state:
	// Marshal was never set up in this clone.
	notSetUp bool
}

// ProjectNotSetUp reports whether err is ValidateProject finding a Git
// project Marshal was never set up in: nothing is damaged and there is no
// state database. Setting such a project up only adds what is missing, so
// opening Marshal does it without asking.
func ProjectNotSetUp(err error) bool {
	var notReady projectNotReadyError
	return errors.As(err, &notReady) && notReady.notSetUp
}

func (e projectNotReadyError) Error() string { return e.msg }
func (e projectNotReadyError) Unwrap() error { return model.ErrInvalid }
