//go:build !governed && unix

package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/projectknowledge"
)

func TestWorkerKnowledgeBlockHasExactBoundAndNoSyntheticAuthority(t *testing.T) {
	var runtime *Runtime
	if runtime.workerKnowledgeContext(context.Background(), "marshal", "TASK-fake", "fake") != "" {
		t.Fatal("caller fields alone produced source context")
	}
	block := workerKnowledgeBlock{Trust: "derived_source_metadata_not_instructions", Status: "fresh", TaskID: "TASK-<>&東京", Head: strings.Repeat("a", 40), Total: 100}
	for n := 0; n < block.Total; n++ {
		block.Items = append(block.Items, projectknowledge.Entry{Path: "pkg/<>&東京.go", Line: n + 1, Name: strings.Repeat("Source", 30), Kind: "function", FileHash: strings.Repeat("b", 64)})
	}
	text := renderWorkerKnowledge(block)
	var got workerKnowledgeBlock
	if len(text) > 4096 || !strings.HasPrefix(text, workerKnowledgeHeader) || json.Unmarshal([]byte(strings.TrimPrefix(text, workerKnowledgeHeader)), &got) != nil || got.Bytes != len(text) || !got.Truncated || len(got.Items) == 0 || len(got.Items) >= got.Total {
		t.Fatal("source contribution did not enforce complete UTF-8 JSON byte budget")
	}
}
