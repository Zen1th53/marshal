package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/app"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/permission"
)

const permissionUsage = "Usage: marshal permission read <allow|deny|revoke|list> <absolute folder>\n"

func (c command) permission(ctx context.Context, args []string) error {
	if len(args) < 3 || args[0] != "read" || (args[1] != "allow" && args[1] != "deny" && args[1] != "revoke" && args[1] != "list") {
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSuffix(permissionUsage, "\n"))
	}
	folder := strings.Join(args[2:], " ")
	if !filepath.IsAbs(folder) {
		return fmt.Errorf("%w: %s", model.ErrInvalid, strings.TrimSuffix(permissionUsage, "\n"))
	}
	runtime, err := app.Open(ctx, c.root)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if args[1] == "list" {
		grants, err := runtime.OperatorReadGrants(ctx)
		if err != nil {
			return err
		}
		canonical := filepath.Clean(folder)
		if resolved, evalErr := filepath.EvalSymlinks(canonical); evalErr == nil {
			canonical = resolved
		}
		for _, grant := range grants {
			if grant.Folder == canonical {
				decision := "deny"
				if grant.Allowed {
					decision = "allow"
				}
				return c.print(map[string]string{"folder": grant.Folder, "decision": decision}, fmt.Sprintf("read %s %s", decision, grant.Folder))
			}
		}
		return c.print(map[string]string{"folder": canonical, "decision": "unset"}, "Read decision unset: "+canonical)
	}
	control, err := runtime.OpenLocalControl(ctx)
	if err != nil {
		return err
	}
	if args[1] == "revoke" {
		if err := runtime.CommandRevokeReadGrant(control.Context(ctx), folder); err != nil {
			return err
		}
		return c.print(map[string]string{"folder": folder, "decision": "revoked"}, "Read decision revoked: "+folder)
	}
	req := permission.Request{Kind: "read", Object: folder, Scope: "this project, until revoked, read-only", Who: "operator", Reason: "operator read decision"}
	if err := runtime.CommandPermission(control.Context(ctx), req, args[1] == "allow", "operator command"); err != nil {
		return err
	}
	return c.print(map[string]string{"folder": folder, "decision": args[1]}, "Read decision recorded: "+folder)
}
