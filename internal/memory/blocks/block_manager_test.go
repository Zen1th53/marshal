package blocks_test

import (
	"context"
	"errors"
	"testing"

	"github.com/Zen1th53/marshal/internal/memory/blocks"
	"github.com/Zen1th53/marshal/internal/model"
)

func TestT97CoreMemoryBlockAttachmentAndRevoke(t *testing.T) {
	mgr := blocks.NewManager(blocks.Config{
		MaxBlockCharacters: 2000,
	})
	ctx := context.Background()

	// 1. Create a core block
	blk, err := mgr.CreateBlock(ctx, blocks.BlockInput{
		ProjectID: "PROJ-1",
		Name:      "Coding Standard",
		Content:   "Always check errors fail-closed. Never swallow errors.",
		Authority: model.AuthorityOperator,
	})
	if err != nil {
		t.Fatalf("CreateBlock: %v", err)
	}

	// 2. Attach to agent-1 and agent-2
	if err := mgr.Attach(ctx, blk.ID, string(model.ScopeAgent), "agent-1"); err != nil {
		t.Fatalf("Attach to agent-1: %v", err)
	}
	if err := mgr.Attach(ctx, blk.ID, string(model.ScopeAgent), "agent-2"); err != nil {
		t.Fatalf("Attach to agent-2: %v", err)
	}

	// 3. List attached blocks for agent-1 -> contains block
	blks1, err := mgr.GetAttachedBlocks(ctx, "PROJ-1", string(model.ScopeAgent), "agent-1")
	if err != nil {
		t.Fatalf("GetAttachedBlocks: %v", err)
	}
	if len(blks1) != 1 || blks1[0].ID != blk.ID {
		t.Fatalf("expected 1 block for agent-1, got: %+v", blks1)
	}

	// 4. Detach from agent-1 -> agent-1 has 0, agent-2 still has 1
	if err := mgr.Detach(ctx, blk.ID, string(model.ScopeAgent), "agent-1"); err != nil {
		t.Fatalf("Detach from agent-1: %v", err)
	}

	blks1After, _ := mgr.GetAttachedBlocks(ctx, "PROJ-1", string(model.ScopeAgent), "agent-1")
	if len(blks1After) != 0 {
		t.Fatalf("expected 0 blocks after detach for agent-1, got %d", len(blks1After))
	}

	blks2, _ := mgr.GetAttachedBlocks(ctx, "PROJ-1", string(model.ScopeAgent), "agent-2")
	if len(blks2) != 1 {
		t.Fatalf("expected agent-2 still attached to block, got %d", len(blks2))
	}
}

func TestT97OversizedBlockRejected(t *testing.T) {
	mgr := blocks.NewManager(blocks.Config{
		MaxBlockCharacters: 50, // Small limit
	})
	ctx := context.Background()

	longContent := "This content exceeds the configured maximum character size of fifty characters."
	_, err := mgr.CreateBlock(ctx, blocks.BlockInput{
		ProjectID: "PROJ-1",
		Name:      "Oversized",
		Content:   longContent,
		Authority: model.AuthorityOperator,
	})
	if !errors.Is(err, blocks.ErrBlockTooLarge) {
		t.Fatalf("expected ErrBlockTooLarge, got: %v", err)
	}
}
