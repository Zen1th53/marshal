package webcontrol

import (
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type ArtifactDigestDTO struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	SHA256      string `json:"sha256"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
}

type ChangedFileSummaryDTO struct {
	Path       string `json:"path"`
	Status     string `json:"status"` // "added", "modified", "deleted"
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

type RunResultComprehensiveDTO struct {
	RunID          string                  `json:"run_id"`
	BaseCommit     string                  `json:"base_commit"`
	HeadCommit     string                  `json:"head_commit"`
	FilesSummary   []ChangedFileSummaryDTO `json:"files_summary"`
	Artifacts      []ArtifactDigestDTO     `json:"artifacts"`
	WorktreeStatus string                  `json:"worktree_status"` // "clean", "dirty", "retained"
	CheckpointID   string                  `json:"checkpoint_id,omitempty"`
	CanRecover     bool                    `json:"can_recover"`
	CreatedAt      time.Time               `json:"created_at"`
}

var mockArtifactStore = map[string]struct {
	name   string
	data   []byte
	mime   string
	sha256 string
}{
	"art-001": {
		name:   "benchmark_results.json",
		data:   []byte(`{"status":"PASS","latency_p95_ms":0.48}`),
		mime:   "application/json",
		sha256: "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
	},
	"art-002": {
		name:   "merkle_evidence.bin",
		data:   []byte("MARSHAL-MERKLE-EVIDENCE-ATTESTATION-SIG-01"),
		mime:   "application/octet-stream",
		sha256: "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
	},
}

var safeFilenameRegex = regexp.MustCompile(`^[a-zA-Z0-9_\-\.]+$`)

func (s *Server) handleGetRunResult(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Run ID is required", "")
		return
	}

	res := RunResultComprehensiveDTO{
		RunID:      id,
		BaseCommit: "4431cce789",
		HeadCommit: "e174534abc",
		FilesSummary: []ChangedFileSummaryDTO{
			{Path: "internal/webcontrol/runs.go", Status: "added", Insertions: 98, Deletions: 0},
			{Path: "web/src/routes/Runs.tsx", Status: "added", Insertions: 175, Deletions: 0},
			{Path: "distribution/PACK-MANIFEST.json", Status: "modified", Insertions: 4, Deletions: 4},
		},
		Artifacts: []ArtifactDigestDTO{
			{
				ID:          "art-001",
				Name:        "benchmark_results.json",
				SHA256:      "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
				SizeBytes:   45,
				ContentType: "application/json",
			},
			{
				ID:          "art-002",
				Name:        "merkle_evidence.bin",
				SHA256:      "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
				SizeBytes:   43,
				ContentType: "application/octet-stream",
			},
		},
		WorktreeStatus: "retained",
		CheckpointID:   "chk-" + id,
		CanRecover:     true,
		CreatedAt:      time.Now().UTC().Add(-10 * time.Minute),
	}

	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleDownloadArtifact(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Artifact ID is required", "")
		return
	}

	// Security invariant: reject path traversal attempt
	if strings.Contains(id, "/") || strings.Contains(id, "..") || strings.Contains(id, "\\") {
		writeError(w, http.StatusBadRequest, "path_traversal_blocked", "Path traversal attempt detected", "")
		return
	}

	art, ok := mockArtifactStore[id]
	if !ok {
		writeError(w, http.StatusNotFound, "artifact_not_found", "Artifact not found: "+id, "")
		return
	}

	cleanFilename := filepath.Base(art.name)
	if !safeFilenameRegex.MatchString(cleanFilename) {
		cleanFilename = "artifact.bin"
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", cleanFilename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(art.data)
}

func (s *Server) handleRecoverRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeError(w, http.StatusBadRequest, "invalid_id", "Run ID is required", "")
		return
	}

	user := s.getAuthenticatedUser(r)
	if user == nil {
		writeError(w, http.StatusUnauthorized, "unauthorized", "Authentication required", "")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"run_id":        id,
		"checkpoint_id": "chk-" + id,
		"recovered_at":  time.Now().UTC(),
		"status":        "restored",
	})
}
