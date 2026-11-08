package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/Zen1th53/marshal/internal/authz"
	"github.com/Zen1th53/marshal/internal/model"
)

const MaxDesignArtifactBytes = 1 << 20

// DesignRecordRequest contains client-supplied artifact bytes. MCP never reads
// a caller-named file on the server. CLI reads the file within the project root.
// Version/provider identify the producer, not an attestation of compatibility.
type DesignRecordRequest struct {
	Stage    string `json:"stage"`
	Provider string `json:"provider"`
	Version  string `json:"egm_version"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Digest   string `json:"digest,omitempty"`
}

type DesignProvenance struct {
	Stage    string `json:"stage"`
	Provider string `json:"provider"`
	Version  string `json:"egm_version"`
	Path     string `json:"path"`
	Digest   string `json:"digest"`
}

// RecordDesign always proposes an agent-authority candidate through the same
// authorized, secret-filtered store path as Remember. Design approval does not
// promote a memory, and no design content is treated as instructions.
func (s *MemoryService) RecordDesign(ctx context.Context, principal authz.Principal, projectID string, req DesignRecordRequest) (model.MemoryRecordV2, error) {
	kind := model.MemoryKindDecision
	switch req.Stage {
	case "concepts_brief", "design_draft", "approved_design", "resolution":
	case "review_findings":
		kind = model.MemoryKindFinding
	default:
		return model.MemoryRecordV2{}, fmt.Errorf("%w: unknown design stage %q", model.ErrInvalid, req.Stage)
	}
	for name, value := range map[string]string{"provider": req.Provider, "egm_version": req.Version, "path": req.Path} {
		if strings.TrimSpace(value) == "" || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return model.MemoryRecordV2{}, fmt.Errorf("%w: invalid %s", model.ErrInvalid, name)
		}
	}
	// Relative slash-separated provenance paths travel across provider platforms.
	if path.IsAbs(req.Path) || path.Clean(req.Path) == "." || path.Clean(req.Path) == ".." || strings.HasPrefix(path.Clean(req.Path), "../") || strings.ContainsAny(req.Path, "\\:") {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: artifact path must be project-relative", model.ErrInvalid)
	}
	if strings.TrimSpace(req.Content) == "" || len(req.Content) > MaxDesignArtifactBytes {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: artifact must contain 1 to %d bytes", model.ErrInvalid, MaxDesignArtifactBytes)
	}
	sum := sha256.Sum256([]byte(req.Content))
	digest := "sha256:" + hex.EncodeToString(sum[:])
	if req.Digest != "" && req.Digest != digest {
		return model.MemoryRecordV2{}, fmt.Errorf("%w: artifact digest mismatch", model.ErrInvalid)
	}
	provenance := DesignProvenance{Stage: req.Stage, Provider: req.Provider, Version: req.Version, Path: path.Clean(req.Path), Digest: digest}
	sourceJSON, err := json.Marshal(provenance)
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	// Keep provenance in the canonical source and body, both covered by the
	// record digest, rather than only ExtMeta (which is not digest protected).
	bodyJSON, err := json.Marshal(struct {
		Provenance DesignProvenance `json:"provenance"`
		Content    string           `json:"content"`
	}{provenance, req.Content})
	if err != nil {
		return model.MemoryRecordV2{}, err
	}
	return s.remember(ctx, principal, RememberRequest{ProjectID: projectID, ScopeID: projectID, Kind: kind, Title: "EGM " + req.Stage + ": " + provenance.Path, Body: string(bodyJSON)}, &model.MemorySource{Kind: "egm", Reference: string(sourceJSON), AgentID: principal.ID})
}
