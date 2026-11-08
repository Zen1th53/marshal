package app

import (
	"context"
	"testing"
)

func TestProviderObservationsUseAgyWithoutGeminiCLI(t *testing.T) {
	rows, err := (*Runtime)(nil).ProviderObservations(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, row := range rows {
		found[row.Name] = true
	}
	if found["gemini"] {
		t.Fatal("retired Gemini CLI is still advertised as a provider")
	}
	if !found["agy"] {
		t.Fatal("Antigravity CLI is missing from provider discovery")
	}
}
