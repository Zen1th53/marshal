//go:build !governed

package cli

import "context"

func (c *command) ultra(context.Context, []string) error {
	return c.print(map[string]any{"entitled": true, "authority": "local"}, "Nothing to request: parallel workers and cross-provider review are included in Marshal.")
}
