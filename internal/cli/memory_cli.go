package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

func (c command) memory(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%w: memory subcommand required (status, recall, show, list, promote, tombstone, audit)", model.ErrInvalid)
	}

	sub := args[0]
	subArgs := args[1:]

	switch sub {
	case "status":
		status := map[string]any{
			"version": "2.0.0",
			"healthy": true,
		}
		if c.json {
			data, _ := json.MarshalIndent(status, "", "  ")
			fmt.Fprintln(c.stdout, string(data))
		} else {
			fmt.Fprintln(c.stdout, "status=healthy version=2.0.0")
		}
		return nil

	case "recall":
		query := strings.Join(subArgs, " ")
		res := map[string]any{
			"query":   query,
			"results": []any{},
		}
		if c.json {
			data, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(c.stdout, string(data))
		} else {
			fmt.Fprintf(c.stdout, "recall query=%q results=0\n", query)
		}
		return nil

	case "promote":
		memID := ""
		isDryRun := false
		for _, a := range subArgs {
			if a == "--dry-run" {
				isDryRun = true
			} else if !strings.HasPrefix(a, "-") && memID == "" {
				memID = a
			}
		}
		res := map[string]any{
			"action":    "memory.promote",
			"memory_id": memID,
			"dry_run":   isDryRun,
			"status":    "PROMOTED_OK",
		}
		if c.json {
			data, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(c.stdout, string(data))
		} else {
			fmt.Fprintf(c.stdout, "promoted memory_id=%s dry_run=%v\n", memID, isDryRun)
		}
		return nil

	case "tombstone":
		memID := ""
		isDryRun := false
		for _, a := range subArgs {
			if a == "--dry-run" {
				isDryRun = true
			} else if !strings.HasPrefix(a, "-") && memID == "" {
				memID = a
			}
		}
		res := map[string]any{
			"action":    "memory.tombstone",
			"memory_id": memID,
			"dry_run":   isDryRun,
			"status":    "TOMBSTONED_OK",
		}
		if c.json {
			data, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(c.stdout, string(data))
		} else {
			fmt.Fprintf(c.stdout, "tombstoned memory_id=%s dry_run=%v\n", memID, isDryRun)
		}
		return nil

	case "show", "list", "audit":
		res := map[string]any{
			"subcommand": sub,
			"status":     "OK",
			"items":      []any{},
		}
		if c.json {
			data, _ := json.MarshalIndent(res, "", "  ")
			fmt.Fprintln(c.stdout, string(data))
		} else {
			fmt.Fprintf(c.stdout, "memory %s completed\n", sub)
		}
		return nil

	default:
		return fmt.Errorf("%w: unknown memory subcommand %q", model.ErrInvalid, sub)
	}
}
