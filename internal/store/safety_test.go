package store

import (
	"testing"
	"time"
)

func TestSetGetRemoveBlock(t *testing.T) {
	s := tempStore(t)

	if err := s.SetBlock("scope-a", "type-a", time.Now().Add(time.Minute), "temporary hold"); err != nil {
		t.Fatalf("SetBlock: %v", err)
	}

	block, err := s.GetBlock("scope-a", "type-a")
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if block == nil {
		t.Fatal("expected block, got nil")
	}
	if block.Scope != "scope-a" || block.BlockType != "type-a" || block.Reason != "temporary hold" {
		t.Fatalf("unexpected block: %+v", block)
	}

	if err := s.RemoveBlock("scope-a", "type-a"); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}
	block, _ = s.GetBlock("scope-a", "type-a")
	if block != nil {
		t.Fatal("expected nil after remove")
	}
}

func TestSetBlockWithMetadataRoundTrip(t *testing.T) {
	s := tempStore(t)

	metadata := map[string]interface{}{"attempts": float64(3), "tag": "circuit"}
	if err := s.SetBlockWithMetadata("system", "gateway", time.Now().Add(10*time.Minute), "gateway circuit", metadata); err != nil {
		t.Fatalf("SetBlockWithMetadata: %v", err)
	}

	block, _ := s.GetBlock("system", "gateway")
	if block == nil {
		t.Fatal("expected block")
	}
	if block.Metadata["tag"] != "circuit" {
		t.Errorf("metadata tag = %v, want circuit", block.Metadata["tag"])
	}
	if _, ok := block.Metadata["attempts"]; !ok {
		t.Error("expected attempts metadata")
	}
}

func TestGetMissingBlockReturnsNil(t *testing.T) {
	s := tempStore(t)
	block, err := s.GetBlock("missing", "type")
	if err != nil {
		t.Fatalf("GetBlock: %v", err)
	}
	if block != nil {
		t.Fatal("expected nil for missing block")
	}
}

func TestGetBlockEmptyInputsReturnsNil(t *testing.T) {
	s := tempStore(t)
	block, err := s.GetBlock("", "type")
	if err != nil {
		t.Fatalf("GetBlock empty scope: %v", err)
	}
	if block != nil {
		t.Fatal("expected nil for empty scope")
	}

	block, err = s.GetBlock("scope", "")
	if err != nil {
		t.Fatalf("GetBlock empty type: %v", err)
	}
	if block != nil {
		t.Fatal("expected nil for empty type")
	}
}

func TestActiveBlocksAndCounts(t *testing.T) {
	s := tempStore(t)

	// No blocks yet.
	counts, _ := s.GetBlockCountsByType()
	if len(counts) != 0 {
		t.Fatalf("expected empty counts, got %v", counts)
	}
	active, _ := s.GetActiveBlocks()
	if len(active) != 0 {
		t.Fatalf("expected no active blocks, got %d", len(active))
	}

	// Create active blocks.
	s.SetBlock("proj-a", "churn_block", time.Now().Add(5*time.Minute), "high failure rate")
	s.SetBlock("proj-b", "churn_block", time.Now().Add(5*time.Minute), "high failure rate b")
	s.SetBlock("task-xyz", "quarantine", time.Now().Add(10*time.Minute), "consecutive failures")
	s.SetBlockWithMetadata("system", "circuit_breaker", time.Now().Add(15*time.Minute), "tripped", map[string]interface{}{"failures": float64(5)})

	// Expired block should not appear.
	s.SetBlock("old-scope", "expired_type", time.Now().Add(-time.Minute), "expired")

	counts, _ = s.GetBlockCountsByType()
	if counts["churn_block"] != 2 {
		t.Fatalf("churn_block count = %d, want 2", counts["churn_block"])
	}
	if counts["quarantine"] != 1 {
		t.Fatalf("quarantine count = %d, want 1", counts["quarantine"])
	}
	if counts["circuit_breaker"] != 1 {
		t.Fatalf("circuit_breaker count = %d, want 1", counts["circuit_breaker"])
	}
	if _, exists := counts["expired_type"]; exists {
		t.Fatal("expired block should not appear in counts")
	}

	active, _ = s.GetActiveBlocks()
	if len(active) != 4 {
		t.Fatalf("expected 4 active blocks, got %d", len(active))
	}

	// Verify circuit breaker metadata round-trips.
	var foundCB bool
	for _, b := range active {
		if b.BlockType == "circuit_breaker" {
			foundCB = true
			if b.Metadata["failures"] == nil {
				t.Fatal("expected circuit_breaker metadata failures")
			}
		}
	}
	if !foundCB {
		t.Fatal("circuit_breaker not found in active blocks")
	}

	// Remove and verify counts decrease.
	s.RemoveBlock("proj-a", "churn_block")
	counts, _ = s.GetBlockCountsByType()
	if counts["churn_block"] != 1 {
		t.Fatalf("churn_block after removal = %d, want 1", counts["churn_block"])
	}
}

func TestTaskValidatingRoundTrip(t *testing.T) {
	s := tempStore(t)

	s.SetTaskValidating("task-v", time.Now().Add(2*time.Minute))

	validating, _ := s.IsTaskValidating("task-v")
	if !validating {
		t.Fatal("expected task to be validating")
	}

	s.ClearTaskValidating("task-v")
	validating, _ = s.IsTaskValidating("task-v")
	if validating {
		t.Fatal("expected task to not be validating after clear")
	}
}

func TestSetBlockRequiresFields(t *testing.T) {
	s := tempStore(t)
	if err := s.SetBlock("", "type", time.Now().Add(time.Minute), "reason"); err == nil {
		t.Fatal("expected error for empty scope")
	}
	if err := s.SetBlock("scope", "", time.Now().Add(time.Minute), "reason"); err == nil {
		t.Fatal("expected error for empty type")
	}
}

func TestSetBlockUpsertOverwrites(t *testing.T) {
	s := tempStore(t)

	s.SetBlock("scope", "type", time.Now().Add(time.Minute), "first")
	s.SetBlock("scope", "type", time.Now().Add(5*time.Minute), "second")

	block, _ := s.GetBlock("scope", "type")
	if block.Reason != "second" {
		t.Fatalf("expected upsert to overwrite, got reason=%q", block.Reason)
	}
}
