package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/Zen1th53/marshal/internal/store"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/memory/importer"
	"github.com/Zen1th53/marshal/internal/memory/portable"
	"github.com/Zen1th53/marshal/internal/memory/security"
	"github.com/Zen1th53/marshal/internal/model"
)

const memoryActionMaxBytes = 8 << 20

type MemoryFileBinding struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type MemoryImportRequest struct {
	File   MemoryFileBinding
	Format string
	DryRun bool
}
type MemoryExportRequest struct {
	Path           string
	Format         string
	ExpectedDigest string
}
type MemoryImportReadback struct {
	File             MemoryFileBinding
	Records          []model.MemorySnapshotRecord
	Skipped          int
	DryRun           bool
	ProjectionStatus string
}

// ReviewMemoryImport reads only a selected regular file beneath the project
// root. os.Root confines resolution, and symlink components are refused.
func (r *Runtime) ReviewMemoryImport(ctx context.Context, path string) (MemoryFileBinding, error) {
	_, _, _, err := r.memoryCommand(ctx, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "review", TargetID: "file", IdempotencyKey: "read-review"}, "memory.import", path)
	if err != nil {
		return MemoryFileBinding{}, err
	}
	_, binding, err := r.readMemoryActionFile(path)
	return binding, err
}

func (r *Runtime) readMemoryActionFile(path string) ([]byte, MemoryFileBinding, error) {
	binding := MemoryFileBinding{Path: path}
	root, rel, err := r.memoryFileRoot(path)
	if err != nil {
		return nil, binding, err
	}
	defer root.Close()
	info, err := root.Lstat(rel)
	if err != nil {
		return nil, binding, err
	}
	if !info.Mode().IsRegular() || info.Size() > memoryActionMaxBytes {
		return nil, binding, model.ErrInvalid
	}
	f, err := root.Open(rel)
	if err != nil {
		return nil, binding, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, binding, authz.ErrDenied
	}
	data, err := io.ReadAll(io.LimitReader(f, memoryActionMaxBytes+1))
	if err != nil {
		return nil, binding, err
	}
	if len(data) > memoryActionMaxBytes {
		return nil, binding, model.ErrInvalid
	}
	sum := sha256.Sum256(data)
	binding.Digest = hex.EncodeToString(sum[:])
	binding.Size = int64(len(data))
	return data, binding, nil
}

func (r *Runtime) memoryFileRoot(path string) (*os.Root, string, error) {
	if r == nil || strings.TrimSpace(path) == "" || filepath.IsAbs(path) || strings.Contains(path, "\\") {
		return nil, "", model.ErrInvalid
	}
	rel := filepath.Clean(path)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".git" || strings.HasPrefix(rel, ".git/") {
		return nil, "", authz.ErrDenied
	}
	// Internal key/state files are never import or export destinations.
	if strings.HasPrefix(rel, ".marshal/") && !strings.HasPrefix(rel, ".marshal/exports/") && !strings.HasPrefix(rel, ".marshal/imports/") {
		return nil, "", authz.ErrDenied
	}
	root, err := os.OpenRoot(r.layout.Root)
	if err != nil {
		return nil, "", err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	prefix := ""
	for _, part := range parts {
		prefix = filepath.Join(prefix, part)
		info, err := root.Lstat(prefix)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			root.Close()
			return nil, "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			root.Close()
			return nil, "", authz.ErrDenied
		}
	}
	return root, rel, nil
}

func (r *Runtime) CommandImportMemory(ctx context.Context, e CommandEnvelope, req MemoryImportRequest) (MemoryImportReadback, error) {
	result := MemoryImportReadback{File: req.File, DryRun: req.DryRun}
	ctx, _, command, err := r.memoryCommand(ctx, e, "memory.import", req)
	if err != nil {
		return result, err
	}
	if req.File.Digest == "" || e.TargetID != req.File.Digest || e.ExpectedVersion != 0 {
		return result, model.ErrInvalid
	}
	data, bound, err := r.readMemoryActionFile(req.File.Path)
	if err != nil {
		return result, err
	}
	if bound != req.File {
		return result, fmt.Errorf("%w: import file changed since review", model.ErrConflict)
	}
	parsed, err := r.parseMemoryImport(ctx, r.ProjectID(), req.Format, data)
	if err != nil {
		return result, err
	}
	if req.DryRun {
		for _, rec := range parsed.ImportedRecords {
			result.Records = append(result.Records, model.MemorySnapshotRecord{ID: rec.ID, Revision: rec.Revision, Digest: rec.ContentDigest})
		}
		result.ProjectionStatus = "NOT_RUN: dry-run; no canonical write"
		return result, nil
	}
	if _, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return result, err
	} else if found {
		for _, rec := range parsed.ImportedRecords {
			got, err := r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID)
			if err != nil || got.Lifecycle == model.MemoryTombstoned || got.Lifecycle == model.MemorySuperseded {
				if err == nil {
					err = model.ErrConflict
				}
				return result, err
			}
			result.Records = append(result.Records, model.MemorySnapshotRecord{ID: got.ID, Revision: got.Revision, Digest: got.ContentDigest})
		}
		result.Skipped = len(parsed.ImportedRecords)
		result.ProjectionStatus = "UNKNOWN: replay reread canonical records; projection not probed"
		return result, nil
	}
	inserted, skipped, err := r.store.CommitMemoryCandidates(ctx, parsed.ImportedRecords, nil)
	if err != nil {
		return result, err
	}
	result.Skipped = skipped
	for _, rec := range inserted {
		got, err := r.store.GetMemoryV2(ctx, r.ProjectID(), rec.ID)
		if err != nil {
			return result, err
		}
		if got.Lifecycle != model.MemoryCandidate || got.Authority != model.AuthorityAgent {
			return result, model.ErrConflict
		}
		result.Records = append(result.Records, model.MemorySnapshotRecord{ID: got.ID, Revision: got.Revision, Digest: got.ContentDigest})
	}
	result.ProjectionStatus = "REBUILT: canonical candidates indexed"
	for _, rec := range inserted {
		if err := r.memoryService.IndexRecord(ctx, rec); err != nil {
			result.ProjectionStatus = "PENDING: canonical candidates committed; durable outbox requires recovery"
			break
		}
	}
	return result, nil
}

func (r *Runtime) parseMemoryImport(ctx context.Context, project, format string, data []byte) (importer.ImportResult, error) {
	imp := importer.NewSessionImporter(importer.Config{})
	switch format {
	case "marshal-json":
		return imp.ImportRawJSON(ctx, project, data, true)
	case string(importer.FormatCodexJSONL), string(importer.FormatClaudeJSONL):
		return imp.ImportProviderHistory(ctx, project, importer.ProviderFormat(format), data, true)
	case "portable":
		var manifest portable.ExportManifest
		if err := json.Unmarshal(data, &manifest); err != nil {
			return importer.ImportResult{}, err
		}
		records, err := portable.NewManager().Import(ctx, manifest, portable.ImportOptions{DryRun: true})
		if err != nil {
			return importer.ImportResult{}, err
		}
		for i := range records {
			rec := &records[i]
			if rec.ProjectID != project || rec.Scope != "project" || rec.ScopeID != project || rec.ACLScope != "" || rec.Lifecycle == model.MemoryTombstoned || rec.Lifecycle == model.MemorySuperseded {
				return importer.ImportResult{}, authz.ErrDenied
			}
			rec.ID = deterministicMemoryID("MEM-PACK-", project, manifest.IntegrityDigest, rec.ID)
			rec.Revision = 0
			rec.Lifecycle = model.MemoryCandidate
			rec.Authority = model.AuthorityAgent
			rec.Confidence = model.ConfidenceUnverified
			rec.EvidenceIDs = nil
			rec.LastVerifiedAt = nil
			rec.SupersededBy = nil
			rec.SupersedesID = nil
			rec.ConflictIDs = nil
			rec.Source = model.MemorySource{Kind: "external_pack", Reference: manifest.IntegrityDigest}
			rec.ExtMeta = map[string]any{"source_pack": manifest.IntegrityDigest}
			if *rec, _, err = security.NewFirewall(security.FirewallConfig{}).AdmitRecord(ctx, *rec); err != nil {
				return importer.ImportResult{}, err
			}
			rec.ContentDigest = rec.CanonicalDigest()
		}
		return importer.ImportResult{ImportedRecords: records}, nil
	default:
		return importer.ImportResult{}, model.ErrInvalid
	}
}

func (r *Runtime) ReviewMemoryExport(ctx context.Context, path, format string) (MemoryFileBinding, error) {
	_, _, _, err := r.memoryCommand(ctx, CommandEnvelope{ProjectID: r.ProjectIdentity(), SessionID: "review", TargetID: path, IdempotencyKey: "read-review"}, "memory.export", format)
	if err != nil {
		return MemoryFileBinding{}, err
	}
	data, err := r.memoryExportPayload(ctx, format)
	if err != nil {
		return MemoryFileBinding{}, err
	}
	root, rel, err := r.memoryFileRoot(path)
	if err != nil {
		return MemoryFileBinding{}, err
	}
	defer root.Close()
	if _, err = root.Lstat(rel); err == nil {
		return MemoryFileBinding{}, fmt.Errorf("%w: destination exists; choose a new path", model.ErrConflict)
	} else if !os.IsNotExist(err) {
		return MemoryFileBinding{}, err
	}
	sum := sha256.Sum256(data)
	return MemoryFileBinding{Path: path, Digest: hex.EncodeToString(sum[:]), Size: int64(len(data))}, nil
}
func (r *Runtime) memoryExportPayload(ctx context.Context, format string) ([]byte, error) {
	switch format {
	case "learning":
		service := r.Learning()
		service.now = func() time.Time { return time.Unix(0, 0).UTC() }
		bundle, err := service.Export(ctx, r.ProjectIdentity(), false)
		if err != nil {
			return nil, err
		}
		return json.Marshal(bundle)
	case "portable":
		records, err := r.store.ListMemoryV2(ctx, store.MemoryQueryFilter{ProjectID: r.ProjectID(), Scope: model.ScopeProject, ScopeID: r.ProjectID(), Limit: 129})
		if err != nil {
			return nil, err
		}
		if len(records) > 128 {
			return nil, fmt.Errorf("%w: export exceeds 128-record bound", model.ErrInvalid)
		}
		safe := records[:0]
		for _, rec := range records {
			if validateMemorySelection(rec, r.ProjectID()) == nil && r.memoryService.revalidateMemoryRecord(ctx, rec, MemoryReconcileRequest{}) == nil {
				safe = append(safe, rec)
			}
		}
		manifest, err := portable.NewManager().ExportAt(ctx, safe, nil, time.Unix(0, 0).UTC())
		if err != nil {
			return nil, err
		}
		return json.Marshal(manifest)
	default:
		return nil, model.ErrInvalid
	}
}

func (r *Runtime) CommandExportMemory(ctx context.Context, e CommandEnvelope, req MemoryExportRequest) (MemoryFileBinding, error) {
	var result MemoryFileBinding
	ctx, _, command, err := r.memoryCommand(ctx, e, "memory.export", req)
	if err != nil {
		return result, err
	}
	data, err := r.memoryExportPayload(ctx, req.Format)
	if err != nil {
		return result, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != req.ExpectedDigest || e.TargetID != req.Path {
		return result, model.ErrConflict
	}
	if err = security.NewFirewall(security.FirewallConfig{}).ScanText(string(data)); err != nil {
		return result, err
	}
	if _, found, err := r.store.MemoryCommandResult(ctx, command); err != nil {
		return result, err
	} else if found {
		_, result, err = r.readMemoryActionFile(req.Path)
		if err == nil && result.Digest != req.ExpectedDigest {
			err = model.ErrConflict
		}
		return result, err
	}
	result, err = r.writeMemoryActionFile(req.Path, data)
	if err != nil {
		return result, err
	}
	if err = r.store.RecordCommandReceipt(ctx, command, e.ExpectedVersion+1); err != nil {
		return result, fmt.Errorf("export exists but receipt failed: %w", err)
	}
	return result, nil
}
func (r *Runtime) writeMemoryActionFile(path string, data []byte) (MemoryFileBinding, error) {
	var result MemoryFileBinding
	root, rel, err := r.memoryFileRoot(path)
	if err != nil {
		return result, err
	}
	defer root.Close()
	f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return result, err
	}
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return result, err
	}
	if closeErr != nil {
		return result, closeErr
	}
	_, result, err = r.readMemoryActionFile(path)
	return result, err
}
