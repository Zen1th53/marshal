package legal

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

type goModuleModule struct {
	Path     string          `json:"Path"`
	Version  string          `json:"Version"`
	Main     bool            `json:"Main"`
	Indirect bool            `json:"Indirect"`
	Dir      string          `json:"Dir"`
	GoMod    string          `json:"GoMod"`
	Replace  *goModuleModule `json:"Replace"`
}

func CollectDependencyEvidence(ctx context.Context, repoDir string) (*DependencyEvidence, error) {
	cmd := exec.CommandContext(ctx, "go", "list", "-m", "-json", "all")
	cmd.Dir = repoDir
	cmd.Env = append(os.Environ(), "GOPROXY=off", "GONOSUMDB=*")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Fallback: if go list fails (e.g. no go.mod or offline cache issue), return graceful review required status
		return &DependencyEvidence{
			Status: StatusReviewRequired,
		}, nil
	}

	decoder := json.NewDecoder(&stdout)
	var deps []DependencyItem
	overall := StatusPass

	for {
		var m goModuleModule
		if err := decoder.Decode(&m); err != nil {
			if err == io.EOF {
				break
			}
			break
		}
		if m.Main {
			continue
		}

		item := DependencyItem{
			Path:     m.Path,
			Version:  m.Version,
			Indirect: m.Indirect,
			Status:   StatusPass,
		}
		if m.Replace != nil {
			item.Replace = fmt.Sprintf("%s %s", m.Replace.Path, m.Replace.Version)
		}

		// Look for license evidence files in local module cache directory
		if m.Dir != "" {
			licFiles := findModuleLicenses(m.Dir, m.Path, m.Version)
			item.Licenses = licFiles
			if len(licFiles) == 0 {
				item.Status = StatusReviewRequired
				if overall == StatusPass {
					overall = StatusReviewRequired
				}
			}
		} else {
			item.Status = StatusReviewRequired
			if overall == StatusPass {
				overall = StatusReviewRequired
			}
		}

		deps = append(deps, item)
	}

	sort.Slice(deps, func(i, j int) bool {
		return deps[i].Path < deps[j].Path
	})

	return &DependencyEvidence{
		Dependencies: deps,
		Status:       overall,
	}, nil
}

func findModuleLicenses(modDir string, modPath string, modVersion string) []FileEvidence {
	var results []FileEvidence

	entries, err := os.ReadDir(modDir)
	if err != nil {
		return results
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		nameUpper := strings.ToUpper(name)
		if strings.HasPrefix(nameUpper, "LICENSE") ||
			strings.HasPrefix(nameUpper, "COPYING") ||
			strings.HasPrefix(nameUpper, "NOTICE") ||
			strings.HasPrefix(nameUpper, "PATENTS") {

			fullPath := filepath.Join(modDir, name)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				continue
			}

			relName := fmt.Sprintf("third-party/dependencies/%s@%s/%s", sanitizeModPath(modPath), modVersion, name)
			fe := FileEvidence{
				Path:       relName,
				BlobSHA256: HashBytes(data),
				SizeBytes:  int64(len(data)),
				Status:     StatusPass,
			}
			results = append(results, fe)
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Path < results[j].Path
	})

	return results
}

func sanitizeModPath(path string) string {
	return strings.ReplaceAll(path, "/", "_")
}
