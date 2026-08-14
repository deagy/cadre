//go:build cgo
// +build cgo

package knowledge

import (
	"testing"
	"time"
)

// Model version registry tests (Phase 5.3)

func TestModelVersionRegistryRegister(t *testing.T) {
	registry := NewModelVersionRegistry()

	mv := &EmbeddingModelVersion{
		Name:       "text-embedding",
		Version:    "1.0",
		Dimensions: 1536,
		Provider:   "openai",
		CreatedAt:  time.Now(),
	}

	err := registry.RegisterVersion(mv)
	if err != nil {
		t.Fatalf("Failed to register version: %v", err)
	}

	// Verify it's registered
	version, err := registry.GetCurrentVersion("text-embedding")
	if err != nil {
		t.Fatalf("Failed to get version: %v", err)
	}

	if version.Version != "1.0" {
		t.Errorf("Expected version 1.0, got %s", version.Version)
	}
}

func TestModelVersionRegistryMultipleVersions(t *testing.T) {
	registry := NewModelVersionRegistry()

	// Register multiple versions
	for i := 1; i <= 3; i++ {
		version := string(rune(48 + i))
		mv := &EmbeddingModelVersion{
			Name:       "text-embedding",
			Version:    version,
			Dimensions: 1536,
			Provider:   "openai",
			CreatedAt:  time.Now().Add(time.Duration(i) * time.Hour),
		}
		registry.RegisterVersion(mv)
	}

	// First registered is current (by design - doesn't automatically switch to latest)
	current, _ := registry.GetCurrentVersion("text-embedding")
	if current.Version != "1" {
		t.Errorf("Expected first version 1 as current, got %s", current.Version)
	}

	// Get all versions
	allVersions, _ := registry.GetAllVersions("text-embedding")
	if len(allVersions) != 3 {
		t.Errorf("Expected 3 versions, got %d", len(allVersions))
	}
}

func TestModelVersionRegistrySetCurrent(t *testing.T) {
	registry := NewModelVersionRegistry()

	// Register versions
	for i := 1; i <= 2; i++ {
		version := string(rune(48 + i))
		mv := &EmbeddingModelVersion{
			Name:       "text-embedding",
			Version:    version,
			Dimensions: 1536,
			Provider:   "openai",
			CreatedAt:  time.Now(),
		}
		registry.RegisterVersion(mv)
	}

	// Set version 1 as current
	err := registry.SetCurrentVersion("text-embedding", "1")
	if err != nil {
		t.Fatalf("Failed to set current version: %v", err)
	}

	current, _ := registry.GetCurrentVersion("text-embedding")
	if current.Version != "1" {
		t.Errorf("Expected current version 1, got %s", current.Version)
	}
}

func TestModelVersionRegistryDeprecate(t *testing.T) {
	registry := NewModelVersionRegistry()

	// Register versions
	mv1 := &EmbeddingModelVersion{
		Name:       "text-embedding",
		Version:    "1",
		Dimensions: 1536,
		Provider:   "openai",
		CreatedAt:  time.Now(),
	}

	mv2 := &EmbeddingModelVersion{
		Name:       "text-embedding",
		Version:    "2",
		Dimensions: 1536,
		Provider:   "openai",
		CreatedAt:  time.Now().Add(1 * time.Hour),
	}

	registry.RegisterVersion(mv1)
	registry.RegisterVersion(mv2)

	// Deprecate version 1
	err := registry.DeprecateVersion("text-embedding", "1")
	if err != nil {
		t.Fatalf("Failed to deprecate version: %v", err)
	}

	version, _ := registry.GetVersion("text-embedding", "1")
	if !version.Deprecated {
		t.Error("Version 1 should be deprecated")
	}

	// If version 1 was current, should switch to version 2
	current, _ := registry.GetCurrentVersion("text-embedding")
	if current.Version != "2" {
		t.Errorf("Expected current version switched to 2, got %s", current.Version)
	}
}

func TestModelVersionRegistryErrors(t *testing.T) {
	registry := NewModelVersionRegistry()

	// Test nil version
	err := registry.RegisterVersion(nil)
	if err == nil {
		t.Error("Expected error for nil version")
	}

	// Test missing name
	err = registry.RegisterVersion(&EmbeddingModelVersion{Version: "1.0"})
	if err == nil {
		t.Error("Expected error for missing name")
	}

	// Test missing version
	err = registry.RegisterVersion(&EmbeddingModelVersion{Name: "model"})
	if err == nil {
		t.Error("Expected error for missing version")
	}

	// Test getting non-existent model
	_, err = registry.GetCurrentVersion("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent model")
	}
}

// Message mutation tests (Phase 5.4)

func TestMutationLogRecordMutation(t *testing.T) {
	log := NewMutationLog()

	mutation := &MessageMutation{
		MessageID:    "msg-1",
		MutationType: "edit",
		FieldChanged: "content",
		OldValue:     "old content",
		NewValue:     "new content",
		MutatedBy:    "user-1",
		MutatedAt:    time.Now(),
		Reason:       "typo fix",
	}

	err := log.RecordMutation(mutation)
	if err != nil {
		t.Fatalf("Failed to record mutation: %v", err)
	}

	// Verify it was recorded
	mutations := log.GetMessageMutations("msg-1")
	if len(mutations) != 1 {
		t.Errorf("Expected 1 mutation, got %d", len(mutations))
	}

	if mutations[0].NewValue != "new content" {
		t.Errorf("Expected new value 'new content', got %s", mutations[0].NewValue)
	}
}

func TestMutationLogHistory(t *testing.T) {
	log := NewMutationLog()

	// Record multiple mutations for same message
	for i := 1; i <= 3; i++ {
		mutation := &MessageMutation{
			MessageID:    "msg-1",
			MutationType: "edit",
			FieldChanged: "content",
			NewValue:     "version " + string(rune(48+i)),
			MutatedBy:    "user-1",
			MutatedAt:    time.Now().Add(time.Duration(i) * time.Minute),
			Reason:       "update",
		}
		log.RecordMutation(mutation)
	}

	mutations := log.GetMessageMutations("msg-1")
	if len(mutations) != 3 {
		t.Errorf("Expected 3 mutations, got %d", len(mutations))
	}

	// Verify chronological order
	for i := 0; i < len(mutations)-1; i++ {
		if mutations[i].MutatedAt.After(mutations[i+1].MutatedAt) {
			t.Error("Mutations not in chronological order")
		}
	}
}

func TestMutationLogGetByType(t *testing.T) {
	log := NewMutationLog()

	// Record different mutation types
	types := []string{"edit", "delete", "edit", "redact"}
	for i, mutType := range types {
		mutation := &MessageMutation{
			MessageID:    "msg-" + string(rune(48+i)),
			MutationType: mutType,
			MutatedBy:    "user-1",
			MutatedAt:    time.Now(),
		}
		log.RecordMutation(mutation)
	}

	// Get mutations by type
	editMutations := log.GetMutationsByType("edit")
	if len(editMutations) != 2 {
		t.Errorf("Expected 2 edit mutations, got %d", len(editMutations))
	}

	deleteMutations := log.GetMutationsByType("delete")
	if len(deleteMutations) != 1 {
		t.Errorf("Expected 1 delete mutation, got %d", len(deleteMutations))
	}
}

func TestMutationLogRecovery(t *testing.T) {
	log := NewMutationLog()

	now := time.Now()

	// Record edit mutation
	mutation := &MessageMutation{
		MessageID:    "msg-1",
		MutationType: "edit",
		FieldChanged: "content",
		OldValue:     "original",
		NewValue:     "edited",
		MutatedBy:    "user-1",
		MutatedAt:    now.Add(-10 * time.Minute),
	}
	log.RecordMutation(mutation)

	// Recover to point before edit
	state, err := log.RecoverMessage("msg-1", now.Add(-15*time.Minute))
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}

	// Should have empty state (before mutation)
	if len(state) != 0 {
		t.Errorf("Expected empty state before mutation, got %v", state)
	}

	// Recover after edit
	state, err = log.RecoverMessage("msg-1", now)
	if err != nil {
		t.Fatalf("Failed to recover: %v", err)
	}

	// Should have edited value
	if state["content"] != "edited" {
		t.Errorf("Expected edited value, got %s", state["content"])
	}
}

func TestMutationLogRecoveryCapability(t *testing.T) {
	log := NewMutationLog()

	// Empty mutation log should not be recoverable
	canRecover := log.CanRecoverMessage("msg-1")
	if canRecover {
		t.Error("Should not be able to recover empty message")
	}

	// After recording mutation, should be recoverable
	mutation := &MessageMutation{
		MessageID:    "msg-1",
		MutationType: "edit",
		MutatedBy:    "user-1",
		MutatedAt:    time.Now(),
	}
	log.RecordMutation(mutation)

	canRecover = log.CanRecoverMessage("msg-1")
	if !canRecover {
		t.Error("Should be able to recover message with mutations")
	}
}

func TestMutationLogStats(t *testing.T) {
	log := NewMutationLog()

	// Record mutations with different types and users
	mutations := []*MessageMutation{
		{
			MessageID:    "msg-1",
			MutationType: "edit",
			MutatedBy:    "user-1",
			MutatedAt:    time.Now().Add(-10 * time.Minute),
		},
		{
			MessageID:    "msg-2",
			MutationType: "delete",
			MutatedBy:    "user-2",
			MutatedAt:    time.Now().Add(-5 * time.Minute),
		},
		{
			MessageID:    "msg-3",
			MutationType: "edit",
			MutatedBy:    "user-1",
			MutatedAt:    time.Now(),
		},
	}

	for _, mutation := range mutations {
		log.RecordMutation(mutation)
	}

	stats := log.GetMutationStats()

	if stats.TotalMutations != 3 {
		t.Errorf("Expected 3 total mutations, got %d", stats.TotalMutations)
	}

	if stats.MutationsByType["edit"] != 2 {
		t.Errorf("Expected 2 edit mutations, got %d", stats.MutationsByType["edit"])
	}

	if stats.MutationsByType["delete"] != 1 {
		t.Errorf("Expected 1 delete mutation, got %d", stats.MutationsByType["delete"])
	}

	if stats.MutationsByUser["user-1"] != 2 {
		t.Errorf("Expected 2 mutations by user-1, got %d", stats.MutationsByUser["user-1"])
	}

	if stats.AverageMutationAge == 0 {
		t.Error("Expected non-zero average mutation age")
	}
}

func TestMutationLogErrors(t *testing.T) {
	log := NewMutationLog()

	// Test nil mutation
	err := log.RecordMutation(nil)
	if err == nil {
		t.Error("Expected error for nil mutation")
	}

	// Test missing message ID
	err = log.RecordMutation(&MessageMutation{MutationType: "edit"})
	if err == nil {
		t.Error("Expected error for missing message ID")
	}

	// Test missing mutation type
	err = log.RecordMutation(&MessageMutation{MessageID: "msg-1"})
	if err == nil {
		t.Error("Expected error for missing mutation type")
	}

	// Test getting non-existent mutation
	_, err = log.GetMutation("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent mutation")
	}

	// Test recovering non-existent message
	_, err = log.RecoverMessage("msg-1", time.Now())
	if err == nil {
		t.Error("Expected error for non-existent message")
	}
}

func TestMutationLogComplexRecovery(t *testing.T) {
	log := NewMutationLog()

	base := time.Now()

	// Simulate message lifecycle: create, edit, redact, restore
	mutations := []*MessageMutation{
		{
			MessageID:    "msg-1",
			MutationType: "edit",
			FieldChanged: "content",
			OldValue:     "",
			NewValue:     "original content",
			MutatedBy:    "user-1",
			MutatedAt:    base,
		},
		{
			MessageID:    "msg-1",
			MutationType: "edit",
			FieldChanged: "content",
			OldValue:     "original content",
			NewValue:     "updated content",
			MutatedBy:    "user-1",
			MutatedAt:    base.Add(5 * time.Minute),
		},
		{
			MessageID:    "msg-1",
			MutationType: "redact",
			FieldChanged: "content",
			MutatedBy:    "admin",
			MutatedAt:    base.Add(10 * time.Minute),
		},
	}

	for _, mutation := range mutations {
		log.RecordMutation(mutation)
	}

	// Recover at different points in time
	state1, _ := log.RecoverMessage("msg-1", base.Add(2*time.Minute))
	if state1["content"] != "original content" {
		t.Errorf("Expected original content after first edit, got %s", state1["content"])
	}

	state2, _ := log.RecoverMessage("msg-1", base.Add(7*time.Minute))
	if state2["content"] != "updated content" {
		t.Errorf("Expected updated content after second edit, got %s", state2["content"])
	}

	state3, _ := log.RecoverMessage("msg-1", base.Add(15*time.Minute))
	if state3["content"] != "[REDACTED]" {
		t.Errorf("Expected redacted content, got %s", state3["content"])
	}
}
