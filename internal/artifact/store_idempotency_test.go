package artifact

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/model"
)

func TestPutReusesExistingContentAddressedArtifact(t *testing.T) {
	artifacts, st := newArtifactStore(t)
	input := func(id string) model.ArtifactInput {
		return model.ArtifactInput{
			ID: id, ProjectID: "PROJECT-local", Kind: "report",
			SourceCommit: "abc123", Data: bytes.NewReader([]byte("same output")),
		}
	}
	first, err := artifacts.Put(context.Background(), input("ART-001"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := artifacts.Put(context.Background(), input("ART-002"))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID {
		t.Fatalf("repeated content created a new artifact: first=%s second=%s", first.ID, second.ID)
	}
	if _, err := st.GetArtifact(context.Background(), "ART-002"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("second artifact ID was registered: %v", err)
	}
}
