package security

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func secretRecord() model.MemoryRecordV2 {
	return model.MemoryRecordV2{ID: "MEM-1", ProjectID: "p", Title: "deploy notes", Body: "use password=hunter2 here", Source: model.MemorySource{Kind: "live_mcp", Reference: "marshal"}, ExtMeta: map[string]any{"claim": "token=ghp_" + "abcdefghijklmnopqrstuvwxyz0123"}}
}

// Community refuses content holding a secret; Extreme removes the secret,
// continues, and reports what it removed. Neither edition keeps the value.
func TestAdmitFollowsTheEdition(t *testing.T) {
	fw := NewFirewall(FirewallConfig{})
	text := "use password=hunter2 here"
	out, r, err := fw.AdmitText(text)
	jsonOut, jr, jerr := fw.AdmitJSON([]byte(`{"note":"password=hunter2"}`))
	rec, rr, rerr := fw.AdmitRecord(context.Background(), secretRecord())
	if !RedactsSecrets {
		if !errors.Is(err, ErrSecretDetected) || out != text || r.Applied() {
			t.Fatalf("Community must refuse text: %q %v", out, err)
		}
		if !errors.Is(jerr, ErrSecretDetected) || jr.Applied() {
			t.Fatal("Community must refuse JSON")
		}
		if !errors.Is(rerr, ErrSecretDetected) || rr.Applied() {
			t.Fatal("Community must refuse the record")
		}
		return
	}
	if err != nil || !r.Applied() || strings.Contains(out, "hunter2") {
		t.Fatalf("Marshal must redact text: %q %v", out, err)
	}
	if jerr != nil || !jr.Applied() || strings.Contains(string(jsonOut), "hunter2") {
		t.Fatalf("Marshal must redact JSON: %s %v", jsonOut, jerr)
	}
	if rerr != nil || !rr.Applied() || strings.Contains(rec.Body, "hunter2") || strings.Contains(rec.ExtMeta["claim"].(string), "ghp_") {
		t.Fatalf("Marshal must redact the record: %+v %v", rec, rerr)
	}
	if kinds, ok := rec.ExtMeta[RedactionMetaKey].([]any); !ok || len(kinds) == 0 {
		t.Fatalf("redaction evidence missing: %+v", rec.ExtMeta)
	}
	if fw.ScanRecord(context.Background(), rec) != nil {
		t.Fatal("redacted record still fails the scan")
	}
}
