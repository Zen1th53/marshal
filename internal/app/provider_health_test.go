package app

import (
	"testing"
	"time"
)

func TestProviderHealthOneRegistryAndResetText(t *testing.T) {
	now := time.Date(2026, 10, 29, 4, 0, 0, 0, time.UTC)
	cases := map[string]time.Duration{
		"agy: Resets in 103h":                          103 * time.Hour,
		"Retry-After: 120":                             2 * time.Minute,
		"usage limit; reset at 5:03 AM":                63 * time.Minute,
		"You've used today's free messages":            20 * time.Hour,
		"resets at 2026-10-29T06:00:00Z quota reached": 2 * time.Hour,
	}
	for text, want := range cases {
		if got, ok := parseCooldownFound(text, now); !ok || got != want {
			t.Errorf("parseCooldownFound(%q) = %v %v, want %v", text, got, ok, want)
		}
	}
	if _, ok := parseCooldownFound("RESOURCE_EXHAUSTED (code 429)", now); ok {
		t.Error("a bare 429 names no reset time")
	}
	h := &ProviderHealth{entries: map[string]providerHealthEntry{}}
	h.Mark("antigravity", "quota", now.Add(time.Hour))
	h.Mark("agy", "quota", now.Add(time.Minute))
	if until, _, ok := h.CooldownUntil("agy"); !ok || !until.Equal(now.Add(time.Hour)) {
		t.Fatalf("later reset must win across names: %v %v", until, ok)
	}
	if _, cooling := h.Cooling("agy", now.Add(2*time.Hour)); cooling {
		t.Fatal("cooldown must expire")
	}
	h.Clear("agy")
	if _, _, ok := h.CooldownUntil("antigravity"); ok {
		t.Fatal("clear must forget the provider")
	}
	if workerFallbackOrder[0] != "codex" || len(providerFallbackOrder) != 4 || providerFallbackOrder[1] != "claude" {
		t.Fatalf("one fallback order: %v", providerFallbackOrder)
	}
}
