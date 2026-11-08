package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/egm"
	"github.com/Zen1th53/marshal/internal/model"
)

func (c command) egm(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: egm status | install --provider X | record --stage X --provider X --egm-version X --file PATH", model.ErrInvalid)
	}
	flags := flag.NewFlagSet("egm "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	provider := flags.String("provider", "", "provider")
	var stage, version, file *string
	if args[0] == "record" {
		stage = flags.String("stage", "", "artifact stage")
		version = flags.String("egm-version", "", "producer version (builtin for fallback)")
		file = flags.String("file", "", "project-relative artifact file")
	}
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %v", model.ErrInvalid, err)
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("%w: unexpected egm arguments", model.ErrInvalid)
	}
	if *provider != "" && !egm.ValidProvider(*provider) {
		return fmt.Errorf("%w: unknown provider %q", model.ErrInvalid, *provider)
	}
	switch args[0] {
	case "status":
		statuses := egm.All()
		if *provider != "" {
			statuses = []egm.Status{egm.Check(*provider)}
		}
		var lines []string
		for _, s := range statuses {
			lines = append(lines, s.String())
		}
		return c.print(statuses, strings.Join(lines, "\n"))
	case "install":
		recipe, err := egm.InstallRecipe(*provider)
		if err != nil {
			return fmt.Errorf("%w: %v", model.ErrInvalid, err)
		}
		return c.print(map[string]any{"provider": *provider, "executed": false, "commands": recipe, "lock": egm.Lock()}, recipe)
	case "record":
		if *file == "" || *stage == "" || *provider == "" || *version == "" {
			return fmt.Errorf("%w: record requires --file, --stage, --provider and --egm-version", model.ErrInvalid)
		}
		runtime, err := app.Open(ctx, c.root)
		if err != nil {
			return err
		}
		defer runtime.Close()
		root, err := os.OpenRoot(runtime.ProjectRoot())
		if err != nil {
			return err
		}
		defer root.Close()
		f, err := root.Open(*file)
		if err != nil {
			return err
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%w: artifact must be a regular file", model.ErrInvalid)
		}
		content, err := io.ReadAll(io.LimitReader(f, app.MaxDesignArtifactBytes+1))
		if err != nil {
			return err
		}
		operator := authz.Principal{ID: "cli-operator", Role: authz.Role{Name: "operator", Authorities: []authz.Authority{authz.AuthorityPolicyAdmin}}}
		rec, err := runtime.Memory().RecordDesign(ctx, operator, runtime.ProjectID(), app.DesignRecordRequest{Stage: *stage, Provider: *provider, Version: *version, Path: filepath.ToSlash(*file), Content: string(content)})
		if err != nil {
			return err
		}
		return c.print(rec, fmt.Sprintf("design memory_id=%s lifecycle=%s", rec.ID, rec.Lifecycle))
	default:
		return fmt.Errorf("%w: unknown egm subcommand %q", model.ErrInvalid, args[0])
	}
}
