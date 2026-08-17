package secrets

import (
	"context"
	"os"
	"regexp"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// EnvironmentProvider is the local baseline provider. The lookup function is
// injectable for deterministic tests; it never persists or logs the value.
type EnvironmentProvider struct {
	Lookup func(string) (string, bool)
}

func (p EnvironmentProvider) Resolve(ctx context.Context, ref Ref) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ref.Provider != "env" || !environmentName.MatchString(ref.Name) || ref.Version == "" {
		return nil, ErrDenied
	}
	lookup := p.Lookup
	if lookup == nil {
		lookup = os.LookupEnv
	}
	value, ok := lookup(ref.Name)
	if !ok {
		return nil, ErrNotFound
	}
	return []byte(value), nil
}
