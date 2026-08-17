package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Zen1th53/marshal/internal/model"
)

type registrar interface {
	RegisterArtifact(context.Context, model.Artifact) error
	GetArtifactByDigest(context.Context, string) (model.Artifact, error)
}

type Store struct {
	root      string
	registrar registrar
}

func New(root string, registrar registrar) *Store {
	return &Store{root: root, registrar: registrar}
}

func (s *Store) Put(ctx context.Context, input model.ArtifactInput) (model.Artifact, error) {
	if input.ProjectID == "" || !validKind(input.Kind) || input.SourceCommit == "" || input.Data == nil {
		return model.Artifact{}, fmt.Errorf("%w: incomplete artifact input", model.ErrInvalid)
	}
	if input.ID == "" {
		id, err := model.NewID("ART-")
		if err != nil {
			return model.Artifact{}, err
		}
		input.ID = id
	}
	digestRoot := filepath.Join(s.root, "sha256")
	if err := os.MkdirAll(digestRoot, 0o700); err != nil {
		return model.Artifact{}, fmt.Errorf("create artifact store: %w", err)
	}
	if err := os.Chmod(s.root, 0o700); err != nil {
		return model.Artifact{}, fmt.Errorf("secure artifact root: %w", err)
	}
	if err := os.Chmod(digestRoot, 0o700); err != nil {
		return model.Artifact{}, fmt.Errorf("secure artifact digest root: %w", err)
	}
	temporary, err := os.CreateTemp(digestRoot, ".incoming-*")
	if err != nil {
		return model.Artifact{}, fmt.Errorf("create artifact temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return model.Artifact{}, fmt.Errorf("secure artifact temporary file: %w", err)
	}
	hash := sha256.New()
	size, copyErr := io.Copy(io.MultiWriter(temporary, hash), input.Data)
	if copyErr != nil {
		temporary.Close()
		return model.Artifact{}, fmt.Errorf("write artifact: %w", copyErr)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return model.Artifact{}, fmt.Errorf("sync artifact: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return model.Artifact{}, fmt.Errorf("close artifact: %w", err)
	}
	hexDigest := hex.EncodeToString(hash.Sum(nil))
	digest := "sha256:" + hexDigest
	if input.ClaimedDigest != "" && input.ClaimedDigest != digest {
		return model.Artifact{}, fmt.Errorf("%w: claimed artifact digest does not match bytes", model.ErrConflict)
	}
	target := filepath.Join(digestRoot, hexDigest)
	if err := os.Link(temporaryPath, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return model.Artifact{}, fmt.Errorf("publish artifact: %w", err)
		}
		existingDigest, err := fileDigest(target)
		if err != nil {
			return model.Artifact{}, err
		}
		if existingDigest != digest {
			return model.Artifact{}, fmt.Errorf("%w: content-addressed artifact path is corrupt", model.ErrConflict)
		}
	}
	artifact := model.Artifact{
		ID: input.ID, ProjectID: input.ProjectID, Kind: input.Kind, Digest: digest,
		SourceCommit: input.SourceCommit, TaskIDs: nonNil(input.TaskIDs),
		ProducerSession:  input.ProducerSession,
		VerificationRefs: nonNil(input.VerificationRefs),
		Path:             target, Size: size, CreatedAt: time.Now().UTC(),
	}
	if existing, err := s.registrar.GetArtifactByDigest(ctx, digest); err == nil {
		return existing, nil
	} else if !errors.Is(err, model.ErrNotFound) {
		return model.Artifact{}, err
	}
	if err := s.registrar.RegisterArtifact(ctx, artifact); err != nil {
		return model.Artifact{}, err
	}
	return artifact, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open existing artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash existing artifact: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func validKind(kind string) bool {
	switch kind {
	case "binary", "package", "container", "report", "sbom", "generated_source", "dataset", "archive":
		return true
	default:
		return false
	}
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
