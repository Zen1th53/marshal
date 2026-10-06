package model

import (
	"strings"
	"testing"
)

func TestSystemRecordLabelUsesRuntimeProvenance(t *testing.T) {
	old := MemoryRecordV2{Title: "Old run outcome", Source: MemorySource{Kind: "runtime_outcome"}}
	if !old.IsSystemRecord() || old.DisplayTitle() != "System record · Old run outcome" {
		t.Fatal("old runtime record unlabeled")
	}
	old.Title = old.DisplayTitle()
	if strings.Count(old.DisplayTitle(), "System record") != 1 {
		t.Fatal("duplicate label")
	}
	forged := MemoryRecordV2{Title: "Model proposal", Source: MemorySource{Kind: "external"}, ExtMeta: map[string]any{"record_class": "system_record"}}
	if forged.IsSystemRecord() || forged.DisplayTitle() != forged.Title {
		t.Fatal("untrusted metadata forged system classification")
	}
}
