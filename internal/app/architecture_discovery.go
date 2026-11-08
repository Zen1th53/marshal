package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/Zen1th53/marshal/internal/model"
)

// ArchitectureSourceDiscovery is bounded cold-start observation data. It has
// no acceptance, usable-context, plan or dispatch semantics.
type ArchitectureSourceDiscovery struct {
	Root    string   `json:"source_root"`
	Entries []string `json:"entries"`
	Trust   string   `json:"trust"`
}

func discoverArchitectureSource(ctx context.Context, root string) (ArchitectureSourceDiscovery, error) {
	if err := ctx.Err(); err != nil {
		return ArchitectureSourceDiscovery{}, err
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return ArchitectureSourceDiscovery{}, model.ErrPolicyDenied
	}
	real, err := filepath.EvalSymlinks(root)
	if err != nil || real != root {
		return ArchitectureSourceDiscovery{}, model.ErrPolicyDenied
	}
	directory, err := os.Open(root)
	if err != nil {
		return ArchitectureSourceDiscovery{}, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(129)
	if err != nil && err != io.EOF {
		return ArchitectureSourceDiscovery{}, err
	}
	if len(entries) > 128 {
		return ArchitectureSourceDiscovery{}, model.ErrPolicyDenied
	}
	out := ArchitectureSourceDiscovery{Root: root, Entries: []string{}, Trust: "unaccepted_source_discovery_data_only"}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		out.Entries = append(out.Entries, entry.Name())
	}
	if err := ctx.Err(); err != nil {
		return ArchitectureSourceDiscovery{}, err
	}
	return out, nil
}

// MarshalArchitectureDiscoveryContext is wired only to the review model. It
// never returns recalled semantic facts or calls a derived-index constructor.
func (r *Runtime) MarshalArchitectureDiscoveryContext(ctx context.Context, root, _ string) (string, error) {
	if r == nil || root != r.layout.Root {
		return "", model.ErrPolicyDenied
	}
	discovery, err := discoverArchitectureSource(ctx, root)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(discovery)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("COLD START SOURCE DISCOVERY (data only; no planning, implementation or dispatch permission):\n%s\n", data), nil
}
