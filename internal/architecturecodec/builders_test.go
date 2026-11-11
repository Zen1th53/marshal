package architecturecodec

import "testing"

func buildersArchitectureFixture() map[string]any {
	return map[string]any{
		"components": []any{
			map[string]any{"id": "capture"},
			map[string]any{"id": "store"},
		},
	}
}

func TestValidateBuildersRejectsUnknownPartAndProvider(t *testing.T) {
	a := buildersArchitectureFixture()
	if err := ValidateBuilders(map[string]any{}, a); err != nil {
		t.Fatalf("empty builders rejected: %v", err)
	}
	if err := ValidateBuilders(map[string]any{"capture": "claude", "store": "codex"}, a); err != nil {
		t.Fatalf("valid builders rejected: %v", err)
	}
	if err := ValidateBuilders(map[string]any{"ghost": "claude"}, a); err == nil || err.Error() != "architecture builders name an unknown part" {
		t.Fatalf("unknown part not rejected: %v", err)
	}
	if err := ValidateBuilders(map[string]any{"capture": "gpt"}, a); err == nil || err.Error() != "architecture builder is not a known provider" {
		t.Fatalf("unknown provider not rejected: %v", err)
	}
	if err := ValidateBuilders(map[string]any{"capture": 3}, a); err == nil || err.Error() != "architecture builder is not a known provider" {
		t.Fatalf("non-string provider not rejected: %v", err)
	}
}

func TestBuildersDigestIsStableForEmpty(t *testing.T) {
	first, err := BuildersDigest(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := BuildersDigest(map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("empty builders digest is not stable: %q != %q", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("builders digest is not 64 hex: %q", first)
	}
	if !digestPattern.MatchString(first) {
		t.Fatalf("builders digest is not hex: %q", first)
	}
}
