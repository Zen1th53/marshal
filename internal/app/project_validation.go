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
	"github.com/Zen1th53/marshal/internal/store"
	"go.yaml.in/yaml/v3"
)

// ValidateProject inspects an existing initialization without bootstrapping it.
// Both the launcher and Open use this gate before any runtime writes. Bootstrap
// remains the explicit init path that creates defaults and canonical state.
func ValidateProject(ctx context.Context, root string) (project.Layout, error) {
	layout, err := project.Discover(root)
	if err != nil {
		if errors.Is(err, project.ErrStatePreparation) {
			return project.Layout{}, fmt.Errorf("cannot open Marshal project in %q: %w", root, err)
		}
		return project.Layout{}, fmt.Errorf("cannot open MARSHAL project in %q: no usable Git repository or commit baseline; cd into your project; run `marshal doctor` for details", root)
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
		return project.Layout{}, projectNotReadyError{msg: fmt.Sprintf("MARSHAL project %q is not initialized or is invalid: %s; run `marshal init` here (or cd into your project); run `marshal doctor` for details", layout.Root, strings.Join(problems, "; "))}
	}
	return layout, nil
}

// projectNotReadyError keeps the operator-facing message unchanged while
// remaining an invalid-input error for callers that match model.ErrInvalid.
type projectNotReadyError struct{ msg string }

func (e projectNotReadyError) Error() string { return e.msg }
func (e projectNotReadyError) Unwrap() error { return model.ErrInvalid }
