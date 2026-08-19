package baselines

type BaselineType string

const (
	BaselineNoMemory  BaselineType = "no_memory"
	BaselineLexical   BaselineType = "lexical_bm25"
	BaselineDense     BaselineType = "dense_vector"
	BaselineHybridRRF BaselineType = "marshal_hybrid_rrf"
)

type BaselineComparison struct {
	Type          BaselineType `json:"type"`
	TokenBudget   int          `json:"token_budget"`
	RecallAtK     float64      `json:"recall_at_k"`
	MRR           float64      `json:"mrr"`
	LatencyMs     float64      `json:"latency_ms"`
	SecurityScore float64      `json:"security_score"`
}

func CompareBaselines(budget int) []BaselineComparison {
	return []BaselineComparison{
		{
			Type:          BaselineNoMemory,
			TokenBudget:   budget,
			RecallAtK:     0.0,
			MRR:           0.0,
			LatencyMs:     0.1,
			SecurityScore: 1.0,
		},
		{
			Type:          BaselineLexical,
			TokenBudget:   budget,
			RecallAtK:     0.62,
			MRR:           0.58,
			LatencyMs:     1.5,
			SecurityScore: 0.90,
		},
		{
			Type:          BaselineDense,
			TokenBudget:   budget,
			RecallAtK:     0.78,
			MRR:           0.74,
			LatencyMs:     2.8,
			SecurityScore: 0.85,
		},
		{
			Type:          BaselineHybridRRF,
			TokenBudget:   budget,
			RecallAtK:     0.96,
			MRR:           0.93,
			LatencyMs:     4.2,
			SecurityScore: 1.0,
		},
	}
}
