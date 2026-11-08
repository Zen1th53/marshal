package security

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen1th53/marshal/internal/redaction"
)

// Secrets used across the edition tests. Each is recognized by a detector.
var redactionSamples = []struct{ text, secret string }{
	{"deploy with token=ghp_" + "abcdefghijklmnopqrstuvwxyz0123 now", "ghp_" + "abcdefghijklmnopqrstuvwxyz0123"},
	{"key sk-ant-" + "abcdefghijklmnopqrstuvwx in config", "sk-ant-" + "abcdefghijklmnopqrstuvwx"},
	{"password=hunter2 for the dev db", "hunter2"},
	{"api_key: Zx9QvB7mLp2Rt5Wk", "Zx9QvB7mLp2Rt5Wk"},
	{"db at postgres://app:s3cretpw@localhost:5432/app", "s3cretpw"},
	{"Authorization: Bearer abcdefghij0123456789", "abcdefghij0123456789"},
	{"AKIA" + "ABCDEFGHIJKLMNOP is the key id", "AKIA" + "ABCDEFGHIJKLMNOP"},
	{"-----BEGIN RSA " + "PRIVATE KEY-----\nMIIEow\n-----END RSA PRIVATE KEY-----", "BEGIN RSA PRIVATE KEY"},
	{"jwt eyJhbGciOiJIUzI1NiJ9" + ".eyJzdWIiOiIxMjM0NTYifQ.abcdefghijklmnop", "eyJhbGciOiJIUzI1NiJ9" + ""},
}

func TestRedactRemovesEverySecretAndPassesTheScan(t *testing.T) {
	fw := NewFirewall(FirewallConfig{})
	for _, sample := range redactionSamples {
		if fw.ScanText(sample.text) == nil {
			t.Fatalf("sample not detected: %q", sample.text)
		}
		out, r := fw.Redact(sample.text)
		if !r.Applied() || strings.Contains(out, sample.secret) || fw.ScanText(out) != nil {
			t.Fatalf("redaction incomplete: %q -> %q (%v)", sample.text, out, r.Kinds)
		}
		if !strings.Contains(out, redaction.Marker) {
			t.Fatalf("no marker: %q", out)
		}
		for _, kind := range r.Kinds {
			if strings.Contains(kind, sample.secret) {
				t.Fatal("redaction kind echoes the secret")
			}
		}
	}
	clean := "an ordinary sentence about tokens and keys"
	if out, r := fw.Redact(clean); out != clean || r.Applied() {
		t.Fatal("clean text changed")
	}
}

func TestRedactJSONKeepsValidJSON(t *testing.T) {
	fw := NewFirewall(FirewallConfig{})
	raw, _ := json.Marshal(map[string]any{"note": "password=hunter2", "list": []any{"x", "ghp_" + "abcdefghijklmnopqrstuvwxyz0123"}, "n": 3})
	out, r, err := fw.RedactJSON(raw)
	if err != nil || !r.Applied() || !json.Valid(out) || fw.ScanText(string(out)) != nil || strings.Contains(string(out), "hunter2") {
		t.Fatalf("%s %v %v", out, r, err)
	}
	same, r, err := fw.RedactJSON([]byte(`{"a":"b"}`))
	if err != nil || r.Applied() || string(same) != `{"a":"b"}` {
		t.Fatal("clean JSON changed")
	}
}
