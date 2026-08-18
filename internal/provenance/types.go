package provenance

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

type Code string

const (
	CodeChangeNotFound  Code = "PROV_CHANGE_NOT_FOUND"
	CodeAlreadySealed   Code = "PROV_ALREADY_SEALED"
	CodePatchMismatch   Code = "PROV_PATCH_MISMATCH"
	CodeForeignEvidence Code = "PROV_FOREIGN_EVIDENCE"
	CodeInvalidCommit   Code = "PROV_INVALID_COMMIT"
)

type Error struct {
	Code    Code
	Message string
	Err     error
}

func (e *Error) Error() string { return e.Message }
func (e *Error) Unwrap() error { return e.Err }
func (e *Error) Is(target error) bool {
	other, ok := target.(*Error)
	return ok && e.Code == other.Code
}

var (
	ErrChangeNotFound  = &Error{Code: CodeChangeNotFound, Message: "provenance change record not found"}
	ErrAlreadySealed   = &Error{Code: CodeAlreadySealed, Message: "provenance chain is already sealed"}
	ErrPatchMismatch   = &Error{Code: CodePatchMismatch, Message: "patch digest mismatch for change record"}
	ErrForeignEvidence = &Error{Code: CodeForeignEvidence, Message: "evidence does not belong to change context"}
	ErrInvalidCommit   = &Error{Code: CodeInvalidCommit, Message: "commit SHA format is invalid"}
)

type ChangeRecord struct {
	ChangeID      string    
	TaskID        string    
	AgentID       string    
	Provider      string    
	ContextDigest string    
	PatchDigest   string    
	CommitSHA     string    
	ToolCallIDs   []string  
	EvidenceIDs   []string  
	ApprovalIDs   []string  
	Sealed        bool      
	CreatedAt     time.Time 
	SealedAt      time.Time 
}

type ChainCustodyView struct {
	Record    ChangeRecord 
	ChainHash string       
}

func CalculateDigest(input string) string {
	h := sha256.Sum256([]byte(input))
	return hex.EncodeToString(h[:])
}

func ValidateSHA(sha string) bool {
	if len(sha) != 40 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func (r *ChangeRecord) ComputeChainHash() string {
	payload := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%s|%s|%s|%v|%d",
		r.ChangeID, r.TaskID, r.AgentID, r.Provider,
		r.ContextDigest, r.PatchDigest, r.CommitSHA,
		strings.Join(r.ToolCallIDs, ","),
		strings.Join(r.EvidenceIDs, ","),
		r.Sealed, r.CreatedAt.UnixNano(),
	)
	return CalculateDigest(payload)
}
