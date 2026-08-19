package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/Zen1th53/marshal/conformance/memory/external"
)

func main() {
	dataset := flag.String("dataset", "locomo", "Benchmark dataset name (locomo, longmemeval, beam)")
	topK := flag.Int("top-k", 10, "Top K results to retrieve")
	count := flag.Int("count", 5, "Number of scenarios to evaluate")
	flag.Parse()

	cfg := external.AdapterConfig{
		Dataset:     *dataset,
		Model:       "default",
		Embedding:   "v2",
		TopK:        *topK,
		TokenBudget: 4096,
	}

	adapter := external.NewBenchmarkAdapter(cfg)
	run, err := adapter.RunSmoke(context.Background(), *count)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	data, _ := json.MarshalIndent(run, "", "  ")
	fmt.Println(string(data))
}
