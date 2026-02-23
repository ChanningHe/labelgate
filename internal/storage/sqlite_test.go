package storage

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestSQLiteStorage_Initialize(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "labelgate-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Create storage
	storage, err := NewSQLiteStorage(tmpFile.Name())
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}
	defer storage.Close()

	// Initialize
	ctx := context.Background()
	if err := storage.Initialize(ctx); err != nil {
		t.Fatalf("failed to initialize: %v", err)
	}

	// Verify schema version
	version, err := storage.GetSyncState(ctx, "schema_version")
	if err != nil {
		t.Fatalf("failed to get schema version: %v", err)
	}
	if version == "" || version == "0" {
		t.Error("schema version should be set after initialization")
	}
}

func TestSQLiteStorage_Resource_CRUD(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create resource
	resource := &ManagedResource{
		ResourceType:   ResourceTypeDNS,
		Hostname:       "test.example.com",
		RecordType:     "A",
		Content:        "1.2.3.4",
		Proxied:        true,
		TTL:            300,
		ContainerID:    "container123",
		ContainerName:  "test-container",
		ServiceName:    "web",
		Status:         StatusActive,
		CleanupEnabled: false,
	}

	// Save
	if err := storage.SaveResource(ctx, resource); err != nil {
		t.Fatalf("failed to save resource: %v", err)
	}

	if resource.ID == "" {
		t.Error("resource ID should be set after save")
	}

	// Get by ID
	got, err := storage.GetResource(ctx, resource.ID)
	if err != nil {
		t.Fatalf("failed to get resource: %v", err)
	}

	if got.Hostname != resource.Hostname {
		t.Errorf("hostname mismatch: got %s, want %s", got.Hostname, resource.Hostname)
	}

	// Get by hostname
	got, err = storage.GetResourceByHostname(ctx, "test.example.com", ResourceTypeDNS)
	if err != nil {
		t.Fatalf("failed to get resource by hostname: %v", err)
	}
	if got.ID != resource.ID {
		t.Error("should return same resource")
	}

	// Update status
	if err := storage.UpdateResourceStatus(ctx, resource.ID, StatusOrphaned); err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	got, _ = storage.GetResource(ctx, resource.ID)
	if got.Status != StatusOrphaned {
		t.Errorf("status should be orphaned, got %s", got.Status)
	}

	// List with filter
	resources, err := storage.ListResources(ctx, ResourceFilter{Status: StatusOrphaned})
	if err != nil {
		t.Fatalf("failed to list resources: %v", err)
	}
	if len(resources) != 1 {
		t.Errorf("should return 1 resource, got %d", len(resources))
	}

	// Delete
	if err := storage.DeleteResource(ctx, resource.ID); err != nil {
		t.Fatalf("failed to delete resource: %v", err)
	}

	_, err = storage.GetResource(ctx, resource.ID)
	if !IsNotFound(err) {
		t.Error("should return not found after delete")
	}
}

func TestSQLiteStorage_Agent_CRUD(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create agent
	agent := &Agent{
		ID:            "agent-1",
		Name:          "Test Agent",
		Connected:     true,
		DefaultTunnel: "default",
		Status:        AgentStatusActive,
	}

	// Save
	if err := storage.SaveAgent(ctx, agent); err != nil {
		t.Fatalf("failed to save agent: %v", err)
	}

	// Get
	got, err := storage.GetAgent(ctx, "agent-1")
	if err != nil {
		t.Fatalf("failed to get agent: %v", err)
	}
	if got.Name != agent.Name {
		t.Errorf("name mismatch: got %s, want %s", got.Name, agent.Name)
	}

	// Update status
	if err := storage.UpdateAgentStatus(ctx, "agent-1", false, AgentStatusDisconnected); err != nil {
		t.Fatalf("failed to update agent status: %v", err)
	}

	got, _ = storage.GetAgent(ctx, "agent-1")
	if got.Connected != false || got.Status != AgentStatusDisconnected {
		t.Error("agent status should be updated")
	}

	// List
	agents, err := storage.ListAgents(ctx)
	if err != nil {
		t.Fatalf("failed to list agents: %v", err)
	}
	if len(agents) != 1 {
		t.Errorf("should return 1 agent, got %d", len(agents))
	}

	// Delete
	if err := storage.DeleteAgent(ctx, "agent-1"); err != nil {
		t.Fatalf("failed to delete agent: %v", err)
	}

	_, err = storage.GetAgent(ctx, "agent-1")
	if !IsNotFound(err) {
		t.Error("should return not found after delete")
	}
}

func TestSQLiteStorage_SyncState(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Set value
	if err := storage.SetSyncState(ctx, "test_key", "test_value"); err != nil {
		t.Fatalf("failed to set sync state: %v", err)
	}

	// Get value
	value, err := storage.GetSyncState(ctx, "test_key")
	if err != nil {
		t.Fatalf("failed to get sync state: %v", err)
	}
	if value != "test_value" {
		t.Errorf("value mismatch: got %s, want test_value", value)
	}

	// Update value
	if err := storage.SetSyncState(ctx, "test_key", "new_value"); err != nil {
		t.Fatalf("failed to update sync state: %v", err)
	}

	value, _ = storage.GetSyncState(ctx, "test_key")
	if value != "new_value" {
		t.Errorf("value should be updated, got %s", value)
	}

	// Get non-existent key
	value, err = storage.GetSyncState(ctx, "non_existent")
	if err != nil {
		t.Fatalf("should not error for non-existent key: %v", err)
	}
	if value != "" {
		t.Error("should return empty string for non-existent key")
	}
}

func TestSQLiteStorage_CleanupDeletedResources(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create resources with different status
	now := time.Now()
	oldTime := now.Add(-24 * time.Hour)

	resources := []*ManagedResource{
		{
			ResourceType: ResourceTypeDNS,
			Hostname:     "active.example.com",
			ServiceName:  "active",
			Status:       StatusActive,
		},
		{
			ResourceType: ResourceTypeDNS,
			Hostname:     "deleted1.example.com",
			ServiceName:  "deleted1",
			Status:       StatusDeleted,
			DeletedAt:    &oldTime,
		},
		{
			ResourceType: ResourceTypeDNS,
			Hostname:     "deleted2.example.com",
			ServiceName:  "deleted2",
			Status:       StatusDeleted,
			DeletedAt:    &now,
		},
	}

	for _, r := range resources {
		if err := storage.SaveResource(ctx, r); err != nil {
			t.Fatalf("failed to save resource: %v", err)
		}
	}

	// Cleanup old deleted resources
	cutoff := now.Add(-12 * time.Hour)
	count, err := storage.CleanupDeletedResources(ctx, cutoff)
	if err != nil {
		t.Fatalf("failed to cleanup: %v", err)
	}

	if count != 1 {
		t.Errorf("should cleanup 1 resource, got %d", count)
	}

	// Verify remaining resources
	all, _ := storage.ListResources(ctx, ResourceFilter{})
	if len(all) != 2 {
		t.Errorf("should have 2 resources remaining, got %d", len(all))
	}
}

func TestSQLiteStorage_HostnameUniqueness(t *testing.T) {
	storage, cleanup := setupTestStorage(t)
	defer cleanup()

	ctx := context.Background()

	// Create first resource
	resource1 := &ManagedResource{
		ResourceType: ResourceTypeDNS,
		Hostname:     "unique.example.com",
		RecordType:   "A",
		ServiceName:  "web1",
		Status:       StatusActive,
	}
	if err := storage.SaveResource(ctx, resource1); err != nil {
		t.Fatalf("failed to save first resource: %v", err)
	}

	// Save second resource with same (resource_type, hostname, record_type).
	// SaveResource uses ON CONFLICT DO UPDATE (upsert), so this should succeed
	// and overwrite the first resource's mutable fields.
	resource2 := &ManagedResource{
		ResourceType: ResourceTypeDNS,
		Hostname:     "unique.example.com",
		RecordType:   "A",
		ServiceName:  "web2",
		Status:       StatusActive,
	}
	if err := storage.SaveResource(ctx, resource2); err != nil {
		t.Fatalf("upsert with same key should succeed: %v", err)
	}

	// Verify the upsert replaced service_name
	got, err := storage.GetResourceByHostname(ctx, "unique.example.com", ResourceTypeDNS)
	if err != nil {
		t.Fatalf("failed to get resource after upsert: %v", err)
	}
	if got.ServiceName != "web2" {
		t.Errorf("expected service_name to be updated to web2, got %s", got.ServiceName)
	}

	// Different resource_type with same hostname should NOT conflict
	tunnelResource := &ManagedResource{
		ResourceType: ResourceTypeTunnelIngress,
		Hostname:     "unique.example.com",
		RecordType:   "",
		ServiceName:  "tunnel1",
		Status:       StatusActive,
	}
	if err := storage.SaveResource(ctx, tunnelResource); err != nil {
		t.Fatalf("different resource_type with same hostname should not conflict: %v", err)
	}

	// Different record_type with same hostname should NOT conflict
	aaaaResource := &ManagedResource{
		ResourceType: ResourceTypeDNS,
		Hostname:     "unique.example.com",
		RecordType:   "AAAA",
		ServiceName:  "web3",
		Status:       StatusActive,
	}
	if err := storage.SaveResource(ctx, aaaaResource); err != nil {
		t.Fatalf("different record_type with same hostname should not conflict: %v", err)
	}
}

// Helper function to setup test storage
func setupTestStorage(t *testing.T) (*SQLiteStorage, func()) {
	tmpFile, err := os.CreateTemp("", "labelgate-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	storage, err := NewSQLiteStorage(tmpFile.Name())
	if err != nil {
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to create storage: %v", err)
	}

	if err := storage.Initialize(context.Background()); err != nil {
		storage.Close()
		os.Remove(tmpFile.Name())
		t.Fatalf("failed to initialize storage: %v", err)
	}

	cleanup := func() {
		storage.Close()
		os.Remove(tmpFile.Name())
	}

	return storage, cleanup
}
