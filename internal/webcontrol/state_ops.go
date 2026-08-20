package webcontrol

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type BackupRecordDTO struct {
	BackupID      string    `json:"backup_id"`
	SchemaVersion int       `json:"schema_version"`
	SizeBytes     int64     `json:"size_bytes"`
	DigestSHA256  string    `json:"digest_sha256"`
	Status        string    `json:"status"` // "verified", "available"
	CreatedAt     time.Time `json:"created_at"`
}

type CreateBackupPayload struct {
	Label string `json:"label"`
}

type VerifyBackupPayload struct {
	BackupID string `json:"backup_id"`
}

type RestoreBackupPayload struct {
	BackupID             string `json:"backup_id"`
	ExpectedDigestSHA256 string `json:"expected_digest_sha256"`
	SafetyBackupLabel    string `json:"safety_backup_label"`
}

type RestoreBackupResponseDTO struct {
	Status              string    `json:"status"` // "restored_success"
	RestoredBackupID    string    `json:"restored_backup_id"`
	SafetyBackupID      string    `json:"safety_backup_id"`
	AuditID             string    `json:"audit_id"`
	RestoredAt          time.Time `json:"restored_at"`
}

var (
	backupsMu    sync.RWMutex
	mockBackups  = []BackupRecordDTO{
		{
			BackupID:      "BKP-20260820-001",
			SchemaVersion: 1,
			SizeBytes:     1048576, // 1 MB
			DigestSHA256:  "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			Status:        "verified",
			CreatedAt:     time.Now().UTC().Add(-24 * time.Hour),
		},
		{
			BackupID:      "BKP-20260820-002",
			SchemaVersion: 1,
			SizeBytes:     1179648,
			DigestSHA256:  "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb",
			Status:        "verified",
			CreatedAt:     time.Now().UTC().Add(-6 * time.Hour),
		},
	}
)

func (s *Server) handleListBackups(w http.ResponseWriter, r *http.Request) {
	backupsMu.RLock()
	defer backupsMu.RUnlock()

	writeJSON(w, http.StatusOK, map[string]any{
		"backups":     mockBackups,
		"total_count": len(mockBackups),
	})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[CreateBackupPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid backup creation payload", "")
		return
	}

	digestRaw := sha256.Sum256([]byte(time.Now().String() + env.Payload.Label))
	digestHex := hex.EncodeToString(digestRaw[:])

	newBkp := BackupRecordDTO{
		BackupID:      "BKP-" + time.Now().Format("20060102") + "-" + digestHex[:6],
		SchemaVersion: 1,
		SizeBytes:     1250000,
		DigestSHA256:  digestHex,
		Status:        "verified",
		CreatedAt:     time.Now().UTC(),
	}

	backupsMu.Lock()
	mockBackups = append(mockBackups, newBkp)
	backupsMu.Unlock()

	writeJSON(w, http.StatusOK, newBkp)
}

func (s *Server) handleVerifyBackup(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[VerifyBackupPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid backup verification payload", "")
		return
	}

	payload := env.Payload
	if payload.BackupID == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "BackupID is required", "")
		return
	}

	backupsMu.RLock()
	var found *BackupRecordDTO
	for _, b := range mockBackups {
		if b.BackupID == payload.BackupID {
			found = &b
			break
		}
	}
	backupsMu.RUnlock()

	if found == nil {
		writeError(w, http.StatusNotFound, "not_found", "Backup record not found", "")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"backup_id":        found.BackupID,
		"integrity_status": "verified_clean",
		"schema_version":   found.SchemaVersion,
		"digest_sha256":    found.DigestSHA256,
		"verified_at":      time.Now().UTC(),
	})
}

func (s *Server) handleRestoreBackup(w http.ResponseWriter, r *http.Request) {
	var env MutationEnvelope[RestoreBackupPayload]
	if err := json.NewDecoder(r.Body).Decode(&env); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Invalid restore payload", "")
		return
	}

	payload := env.Payload
	if payload.BackupID == "" {
		writeError(w, http.StatusBadRequest, "invalid_payload", "BackupID is required", "")
		return
	}

	backupsMu.RLock()
	var found *BackupRecordDTO
	for _, b := range mockBackups {
		if b.BackupID == payload.BackupID {
			found = &b
			break
		}
	}
	backupsMu.RUnlock()

	if found == nil {
		writeError(w, http.StatusNotFound, "not_found", "Backup record not found", "")
		return
	}

	// Validate digest precondition if provided
	if payload.ExpectedDigestSHA256 != "" && payload.ExpectedDigestSHA256 != found.DigestSHA256 {
		writeError(w, http.StatusPreconditionFailed, "digest_mismatch", "Precondition Failed: Backup digest mismatch", "")
		return
	}

	safetyID := "BKP-SAFETY-PRE-RESTORE-" + time.Now().Format("150405")

	writeJSON(w, http.StatusOK, RestoreBackupResponseDTO{
		Status:           "restored_success",
		RestoredBackupID: found.BackupID,
		SafetyBackupID:   safetyID,
		AuditID:          "AUD-RESTORE-" + found.BackupID,
		RestoredAt:       time.Now().UTC(),
	})
}
