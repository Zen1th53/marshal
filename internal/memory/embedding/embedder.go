package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"

	"github.com/Zen1th53/marshal/internal/model"
)

var (
	ErrRemoteEmbeddingForbidden = errors.New("remote embedding is forbidden for protected memory without explicit policy authorization")
)

type Embedder interface {
	ModelID() string
	Dimensions() int
	IsRemote() bool
	Embed(ctx context.Context, text string) ([]float32, error)
	EmbedBatch(ctx context.Context, texts []string) ([][]float32, error)
}

type DeterministicEmbedder struct {
	modelID string
	dims    int
}

func NewDeterministicEmbedder(modelID string, dims int) *DeterministicEmbedder {
	if dims <= 0 {
		dims = 128
	}
	return &DeterministicEmbedder{
		modelID: modelID,
		dims:    dims,
	}
}

func (d *DeterministicEmbedder) ModelID() string { return d.modelID }
func (d *DeterministicEmbedder) Dimensions() int { return d.dims }
func (d *DeterministicEmbedder) IsRemote() bool  { return false }

func (d *DeterministicEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, d.dims)
	h := sha256.New()
	h.Write([]byte(d.modelID))
	h.Write([]byte("\x00"))
	h.Write([]byte(text))
	seed := h.Sum(nil)

	for i := 0; i < d.dims; i++ {
		offset := (i * 4) % (len(seed) - 4)
		val := binary.LittleEndian.Uint32(seed[offset : offset+4])
		// Normalized float between -1.0 and 1.0
		vec[i] = float32((float64(val)/float64(math.MaxUint32))*2.0 - 1.0)
	}

	return vec, nil
}

func (d *DeterministicEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	var batch [][]float32
	for _, txt := range texts {
		v, err := d.Embed(ctx, txt)
		if err != nil {
			return nil, err
		}
		batch = append(batch, v)
	}
	return batch, nil
}

type RemoteEmbedder struct {
	modelID string
	dims    int
	allowed bool
}

func NewRemoteEmbedder(modelID string, dims int, allowed bool) *RemoteEmbedder {
	return &RemoteEmbedder{
		modelID: modelID,
		dims:    dims,
		allowed: allowed,
	}
}

func (r *RemoteEmbedder) ModelID() string { return r.modelID }
func (r *RemoteEmbedder) Dimensions() int { return r.dims }
func (r *RemoteEmbedder) IsRemote() bool  { return true }
func (r *RemoteEmbedder) Allowed() bool   { return r.allowed }

func (r *RemoteEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, r.dims)
	return vec, nil
}

func (r *RemoteEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	var batch [][]float32
	for _, txt := range texts {
		v, err := r.Embed(ctx, txt)
		if err != nil {
			return nil, err
		}
		batch = append(batch, v)
	}
	return batch, nil
}

type Manager struct {
	mu sync.RWMutex
}

func NewManager() *Manager {
	return &Manager{}
}

// EmbedRecord checks remote embedding policy before generating embeddings for a record.
func (m *Manager) EmbedRecord(ctx context.Context, emb Embedder, rec model.MemoryRecordV2) ([]float32, error) {
	if emb.IsRemote() {
		if rem, ok := emb.(*RemoteEmbedder); ok && !rem.Allowed() {
			return nil, fmt.Errorf("%w: record %s (kind %s)", ErrRemoteEmbeddingForbidden, rec.ID, rec.Kind)
		}
	}

	fullText := rec.Title + "\n" + rec.Body
	return emb.Embed(ctx, fullText)
}
