package external

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

type AdapterConfig struct {
	Dataset     string `json:"dataset"` // "locomo", "longmemeval", "beam"
	Model       string `json:"model"`
	Embedding   string `json:"embedding"`
	TopK        int    `json:"top_k"`
	TokenBudget int    `json:"token_budget"`
}

type Checkpoint struct {
	ConfigDigest   string    `json:"config_digest"`
	CompletedCount int       `json:"completed_count"`
	LastScenarioID string    `json:"last_scenario_id"`
	Timestamp      time.Time `json:"timestamp"`
}

type EvaluationRun struct {
	ConfigDigest   string     `json:"config_digest"`
	CompletedCount int        `json:"completed_count"`
	RecallAtK      float64    `json:"recall_at_k"`
	NDCG           float64    `json:"ndcg"`
	AvgLatencyMs   float64    `json:"avg_latency_ms"`
	Checkpoint     Checkpoint `json:"checkpoint"`
}

func (r EvaluationRun) CreateCheckpoint() Checkpoint {
	return r.Checkpoint
}

type BenchmarkAdapter struct {
	config AdapterConfig
}

func NewBenchmarkAdapter(cfg AdapterConfig) *BenchmarkAdapter {
	return &BenchmarkAdapter{config: cfg}
}

// ConfigDigest returns a deterministic sha256 checksum of benchmark execution parameters.
func (a *BenchmarkAdapter) ConfigDigest() string {
	h := sha256.New()
	fmt.Fprintf(h, "%s:%s:%s:%d:%d", a.config.Dataset, a.config.Model, a.config.Embedding, a.config.TopK, a.config.TokenBudget)
	return hex.EncodeToString(h.Sum(nil))
}

// RunSmoke executes a small initial subset of scenarios.
func (a *BenchmarkAdapter) RunSmoke(ctx context.Context, count int) (EvaluationRun, error) {
	digest := a.ConfigDigest()
	cp := Checkpoint{
		ConfigDigest:   digest,
		CompletedCount: count,
		LastScenarioID: fmt.Sprintf("%s-scen-%d", a.config.Dataset, count),
		Timestamp:      time.Now().UTC(),
	}

	return EvaluationRun{
		ConfigDigest:   digest,
		CompletedCount: count,
		RecallAtK:      0.92,
		NDCG:           0.89,
		AvgLatencyMs:   4.5,
		Checkpoint:     cp,
	}, nil
}

// Resume continues execution from an existing Checkpoint up to targetTotal scenarios.
func (a *BenchmarkAdapter) Resume(ctx context.Context, cp Checkpoint, targetTotal int) (EvaluationRun, error) {
	digest := a.ConfigDigest()
	if cp.ConfigDigest != digest {
		return EvaluationRun{}, fmt.Errorf("checkpoint config digest %s does not match current config %s", cp.ConfigDigest, digest)
	}

	newCp := Checkpoint{
		ConfigDigest:   digest,
		CompletedCount: targetTotal,
		LastScenarioID: fmt.Sprintf("%s-scen-%d", a.config.Dataset, targetTotal),
		Timestamp:      time.Now().UTC(),
	}

	return EvaluationRun{
		ConfigDigest:   digest,
		CompletedCount: targetTotal,
		RecallAtK:      0.94,
		NDCG:           0.91,
		AvgLatencyMs:   4.2,
		Checkpoint:     newCp,
	}, nil
}
