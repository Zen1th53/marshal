package app

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/Zen1th53/marshal/internal/evidence"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
	"github.com/Zen1th53/marshal/internal/project"
)

type NativeConversation struct {
	ID, SourceID, Provider, Project, Source string
	UpdatedAt                               time.Time
	ProviderListed                          bool
}

type SessionInventory struct {
	Native         []NativeConversation
	Governed       []model.WorkerRun
	Warnings       []string
	GovernedModels map[string]string
}

func sessionProject(root string) string {
	root = canonicalSessionRoot(root)
	sum := sha256.Sum256([]byte(root))
	return hex.EncodeToString(sum[:])
}
func canonicalSessionRoot(root string) string {
	if p, err := filepath.Abs(root); err == nil {
		root = p
	}
	if p, err := filepath.EvalSymlinks(root); err == nil {
		root = p
	}
	return filepath.Clean(root)
}
func NativeConversationID(provider, root, sourceID string) string {
	return "native:" + provider + ":" + sessionProject(root) + ":" + url.PathEscape(sourceID)
}
func sessionProvider(provider string) string {
	provider = strings.ToLower(provider)
	switch {
	case provider == "agy":
		return "antigravity"
	case strings.HasPrefix(provider, "claude"):
		return "claude"
	case strings.HasPrefix(provider, "codex"):
		return "codex"
	}
	return provider
}

// Sessions derives the two inventories from canonical imports and provider
// history. Capture cursor indexes are deliberately not ownership evidence.
func (r *Runtime) Sessions(ctx context.Context, provider string) (SessionInventory, error) {
	var inv SessionInventory
	if r == nil || r.store == nil {
		return inv, errors.New("session inventory runtime unavailable")
	}
	provider = sessionProvider(provider)
	imported, err := r.store.ImportedConversations(ctx, r.ProjectID())
	if err != nil {
		return inv, err
	}
	entries := map[string]NativeConversation{}
	firewall := security.NewFirewall(security.FirewallConfig{})
	add := func(p, id, source string, stamp time.Time, listed bool) {
		if len(id) > 4096 || strings.HasPrefix(id, "-") || len(p) > 256 || strings.ContainsFunc(id, unicode.IsControl) || strings.ContainsFunc(p, unicode.IsControl) || firewall.ScanText(id) != nil || firewall.ScanText(p) != nil {
			inv.Warnings = append(inv.Warnings, "native identity omitted: unsafe metadata")
			return
		}
		p = sessionProvider(p)
		if id == "" || (provider != "" && provider != p) {
			return
		}
		key := NativeConversationID(p, r.ProjectRoot(), id)
		old, ok := entries[key]
		if ok {
			if stamp.After(old.UpdatedAt) {
				old.UpdatedAt = stamp
			}
			old.ProviderListed = old.ProviderListed || listed
			if !strings.Contains(old.Source, source) {
				old.Source += ", " + source
			}
			entries[key] = old
			return
		}
		entries[key] = NativeConversation{ID: key, SourceID: id, Provider: p, Project: sessionProject(r.ProjectRoot()), Source: source, UpdatedAt: stamp, ProviderListed: listed}
	}
	for _, tr := range imported {
		add(tr.Provider, tr.SourceID, "transcript imported", tr.UpdatedAt, false)
	}
	inv.Governed, err = r.store.SessionInventoryRuns(ctx, r.ProjectID(), provider)
	if err != nil {
		return inv, err
	}
	inv.GovernedModels = make(map[string]string, len(inv.Governed))
	for _, run := range inv.Governed {
		name := "UNKNOWN"
		if node, evidenceErr := r.Evidence(ctx, evidence.NodeID("EVIDENCE-RUN-"+run.ID+"-COMMAND")); evidenceErr == nil {
			if effective := strings.TrimSpace(node.Metadata["effective_model"]); effective != "" {
				name = effective
			} else if requested := strings.TrimSpace(node.Metadata["requested_model"]); requested != "" {
				name = requested
			}
		}
		if firewall.ScanText(name) != nil || strings.ContainsFunc(name, unicode.IsControl) {
			name = "UNKNOWN"
		}
		inv.GovernedModels[run.ID] = name
	}
	for _, p := range []string{"codex", "claude", "opencode", "antigravity"} {
		if provider != "" && p != provider {
			continue
		}
		if err := readNativeHistory(ctx, p, r.ProjectRoot(), add); err != nil {
			inv.Warnings = append(inv.Warnings, p+" native history unavailable")
		}
	}
	for _, entry := range entries {
		inv.Native = append(inv.Native, entry)
	}
	sort.Slice(inv.Native, func(i, j int) bool {
		if inv.Native[i].UpdatedAt.Equal(inv.Native[j].UpdatedAt) {
			return inv.Native[i].ID < inv.Native[j].ID
		}
		return inv.Native[i].UpdatedAt.After(inv.Native[j].UpdatedAt)
	})
	sort.Slice(inv.Governed, func(i, j int) bool { return inv.Governed[i].StartedAt.After(inv.Governed[j].StartedAt) })
	return inv, nil
}

func (r *Runtime) ResolveNativeConversation(ctx context.Context, provider, target string) (NativeConversation, error) {
	if r == nil || r.store == nil {
		return NativeConversation{}, errors.New("session inventory runtime unavailable")
	}
	provider = sessionProvider(provider)
	if strings.HasPrefix(target, "native:") {
		parts := strings.SplitN(target, ":", 4)
		if len(parts) != 4 {
			return NativeConversation{}, errors.New("malformed native conversation ID")
		}
		if parts[1] != provider {
			return NativeConversation{}, errors.New("native conversation belongs to another provider")
		}
		if parts[2] != sessionProject(r.ProjectRoot()) {
			return NativeConversation{}, errors.New("native conversation belongs to another project")
		}
	} else if target != "" && target != "--last" {
		governed, err := r.store.IsGovernedSessionID(ctx, target)
		if err != nil {
			return NativeConversation{}, err
		}
		if governed {
			return NativeConversation{}, errors.New("governed run/task/session ID cannot resume or fork a native conversation")
		}
	}
	inv, err := r.Sessions(ctx, provider)
	if err != nil {
		return NativeConversation{}, err
	}
	latest := target == "" || target == "--last"
	for _, c := range inv.Native {
		if latest {
			if c.Provider == provider {
				return c, nil
			}
			continue
		}
		if target == c.ID || target == c.SourceID {
			if c.Provider != provider {
				return NativeConversation{}, errors.New("native conversation belongs to another provider")
			}
			return c, nil
		}
	}
	if !latest {
		imported, err := r.store.ImportedConversations(ctx, r.ProjectID())
		if err != nil {
			return NativeConversation{}, err
		}
		for _, c := range imported {
			if c.SourceID == target && sessionProvider(c.Provider) != provider {
				return NativeConversation{}, errors.New("native conversation belongs to another provider")
			}
		}
	}
	if latest {
		return NativeConversation{}, fmt.Errorf("no native %s conversation recorded for this project", provider)
	}
	return NativeConversation{}, errors.New("unknown native conversation ID for this provider and project; use /sessions")
}

type nativeHistoryEntry struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	CWD       string `json:"cwd"`
	Payload   struct {
		ID  string `json:"id"`
		CWD string `json:"cwd"`
	} `json:"payload"`
}

func readNativeHistory(ctx context.Context, provider, root string, add func(string, string, string, time.Time, bool)) error {
	if provider == "opencode" {
		indexErr := readOpenCodeInventory(ctx, root, add)
		// Only this provider has a qualified public JSON listing here. Do not
		// infer resumability from imported transcripts or private database rows.
		binary, err := project.FindBinary("opencode")
		if err != nil {
			return indexErr
		}
		childCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		cmd := exec.CommandContext(childCtx, binary, "session", "list", "--format", "json", "--max-count", "100")
		cmd.Dir = root
		data, err := cmd.Output()
		if err != nil {
			return errors.Join(indexErr, err)
		}
		var sessions []struct {
			ID, Directory string
			Updated       int64
		}
		if err := json.Unmarshal(data, &sessions); err != nil {
			return err
		}
		for _, s := range sessions {
			if canonicalSessionRoot(s.Directory) == canonicalSessionRoot(root) {
				add(provider, s.ID, "provider listing", time.UnixMilli(s.Updated), true)
			}
		}
		return indexErr
	}
	if provider == "antigravity" {
		return readAntigravityInventory(ctx, root, add)
	}
	env, base, dir := "CODEX_HOME", ".codex", "sessions"
	if provider == "claude" {
		env, base, dir = "CLAUDE_CONFIG_DIR", ".claude", "projects"
	}
	home := os.Getenv(env)
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		home = filepath.Join(userHome, base)
	}
	if !filepath.IsAbs(home) {
		home = filepath.Join(root, home)
	}
	return filepath.WalkDir(filepath.Join(home, dir), func(path string, entry fs.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		scan := bufio.NewScanner(f)
		scan.Buffer(make([]byte, 4096), 1<<20)
		for n := 0; n < 128 && scan.Scan(); n++ {
			var meta nativeHistoryEntry
			if json.Unmarshal(scan.Bytes(), &meta) != nil {
				continue
			}
			id, cwd := meta.Payload.ID, meta.Payload.CWD
			if provider == "codex" && meta.Type != "session_meta" {
				continue
			}
			if provider == "claude" {
				id, cwd = meta.SessionID, meta.CWD
			}
			if id == "" || cwd == "" {
				continue
			}
			if canonicalSessionRoot(cwd) != canonicalSessionRoot(root) {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			add(provider, id, "provider history index", info.ModTime(), false)
			return nil
		}
		return scan.Err()
	})
}
