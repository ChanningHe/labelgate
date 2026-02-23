package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/storage"
)

// mockStorage implements storage.Storage for testing.
type mockStorage struct {
	resources []*storage.ManagedResource
	agents    []*storage.Agent
}

func (m *mockStorage) Initialize(ctx context.Context) error                    { return nil }
func (m *mockStorage) Close() error                                            { return nil }
func (m *mockStorage) GetResource(ctx context.Context, id string) (*storage.ManagedResource, error) {
	return nil, storage.ErrNotFound
}
func (m *mockStorage) GetResourceByHostname(ctx context.Context, hostname string, resourceType storage.ResourceType) (*storage.ManagedResource, error) {
	return nil, storage.ErrNotFound
}
func (m *mockStorage) GetResourceByContainerService(ctx context.Context, containerID, serviceName string) (*storage.ManagedResource, error) {
	return nil, storage.ErrNotFound
}
func (m *mockStorage) ListResources(ctx context.Context, filter storage.ResourceFilter) ([]*storage.ManagedResource, error) {
	var result []*storage.ManagedResource
	for _, r := range m.resources {
		if filter.ResourceType != "" && r.ResourceType != filter.ResourceType {
			continue
		}
		if filter.Status != "" && r.Status != filter.Status {
			continue
		}
		if filter.AgentID != "" && r.AgentID != filter.AgentID {
			continue
		}
		result = append(result, r)
	}
	return result, nil
}
func (m *mockStorage) SaveResource(ctx context.Context, resource *storage.ManagedResource) error {
	return nil
}
func (m *mockStorage) UpdateResourceStatus(ctx context.Context, id string, status storage.ResourceStatus) error {
	return nil
}
func (m *mockStorage) UpdateResourceError(ctx context.Context, id string, status storage.ResourceStatus, lastError string) error {
	return nil
}
func (m *mockStorage) DeleteResource(ctx context.Context, id string) error { return nil }

func (m *mockStorage) GetAgent(ctx context.Context, id string) (*storage.Agent, error) {
	return nil, storage.ErrNotFound
}
func (m *mockStorage) ListAgents(ctx context.Context) ([]*storage.Agent, error) {
	return m.agents, nil
}
func (m *mockStorage) SaveAgent(ctx context.Context, agent *storage.Agent) error { return nil }
func (m *mockStorage) UpdateAgentStatus(ctx context.Context, id string, connected bool, status storage.AgentStatus) error {
	return nil
}
func (m *mockStorage) DeleteAgent(ctx context.Context, id string) error { return nil }

func (m *mockStorage) GetSyncState(ctx context.Context, key string) (string, error) {
	return "", nil
}
func (m *mockStorage) SetSyncState(ctx context.Context, key, value string) error { return nil }

func (m *mockStorage) CleanupDeletedResources(ctx context.Context, before time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStorage) ListExpiredOrphans(ctx context.Context, olderThan time.Time) ([]*storage.ManagedResource, error) {
	return nil, nil
}
func (m *mockStorage) ListOrphanedForCleanup(ctx context.Context, olderThan time.Time) ([]*storage.ManagedResource, error) {
	return nil, nil
}
func (m *mockStorage) Vacuum(ctx context.Context) error { return nil }

func newTestServer(store storage.Storage) *Server {
	return NewServer(&Config{
		Address:  ":0",
		BasePath: "/api",
		Token:    "",
		Storage:  store,
		Version:  "0.1.0-test",
	})
}

func TestHealthEndpoint(t *testing.T) {
	s := newTestServer(&mockStorage{})
	req := httptest.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()

	s.handleHealth(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Fatalf("expected status ok, got %s", body["status"])
	}
}

func TestVersionEndpoint(t *testing.T) {
	s := newTestServer(&mockStorage{})
	req := httptest.NewRequest("GET", "/api/version", nil)
	w := httptest.NewRecorder()

	s.handleVersion(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]string
	json.NewDecoder(w.Body).Decode(&body)
	if body["version"] != "0.1.0-test" {
		t.Fatalf("expected version 0.1.0-test, got %s", body["version"])
	}
}

func TestDNSEndpoint(t *testing.T) {
	store := &mockStorage{
		resources: []*storage.ManagedResource{
			{ID: "1", ResourceType: storage.ResourceTypeDNS, Hostname: "a.example.com", Status: storage.StatusActive},
			{ID: "2", ResourceType: storage.ResourceTypeDNS, Hostname: "b.example.com", Status: storage.StatusOrphaned},
			{ID: "3", ResourceType: storage.ResourceTypeTunnelIngress, Hostname: "c.example.com", Status: storage.StatusActive},
		},
	}
	s := newTestServer(store)
	req := httptest.NewRequest("GET", "/api/resources/dns", nil)
	w := httptest.NewRecorder()

	s.handleDNS(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 2 {
		t.Fatalf("expected 2 DNS records, got %d", total)
	}
}

func TestDNSEndpointWithStatusFilter(t *testing.T) {
	store := &mockStorage{
		resources: []*storage.ManagedResource{
			{ID: "1", ResourceType: storage.ResourceTypeDNS, Hostname: "a.example.com", Status: storage.StatusActive},
			{ID: "2", ResourceType: storage.ResourceTypeDNS, Hostname: "b.example.com", Status: storage.StatusOrphaned},
		},
	}
	s := newTestServer(store)
	req := httptest.NewRequest("GET", "/api/resources/dns?status=active", nil)
	w := httptest.NewRecorder()

	s.handleDNS(w, req)

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 1 {
		t.Fatalf("expected 1 active DNS record, got %d", total)
	}
}

func TestTokenAuthMiddleware(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	// Test with no token configured (should pass through)
	t.Run("no token required", func(t *testing.T) {
		h := tokenAuth("", handler)
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	// Test with valid token
	t.Run("valid token", func(t *testing.T) {
		h := tokenAuth("secret", handler)
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer secret")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("expected 200, got %d", w.Code)
		}
	})

	// Test with invalid token
	t.Run("invalid token", func(t *testing.T) {
		h := tokenAuth("secret", handler)
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set("Authorization", "Bearer wrong")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})

	// Test with missing token
	t.Run("missing token", func(t *testing.T) {
		h := tokenAuth("secret", handler)
		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("expected 401, got %d", w.Code)
		}
	})
}

func TestAgentsEndpoint(t *testing.T) {
	now := time.Now()
	store := &mockStorage{
		agents: []*storage.Agent{
			{ID: "agent-1", Name: "Host 1", Connected: true, LastSeen: &now, Status: storage.AgentStatusActive},
			{ID: "agent-2", Name: "Host 2", Connected: false, Status: storage.AgentStatusDisconnected},
		},
	}
	s := newTestServer(store)
	req := httptest.NewRequest("GET", "/api/agents", nil)
	w := httptest.NewRecorder()

	s.handleAgents(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body map[string]any
	json.NewDecoder(w.Body).Decode(&body)
	total := int(body["total"].(float64))
	if total != 2 {
		t.Fatalf("expected 2 agents, got %d", total)
	}
}

func TestOverviewEndpoint(t *testing.T) {
	store := &mockStorage{
		resources: []*storage.ManagedResource{
			{ID: "1", ResourceType: storage.ResourceTypeDNS, Status: storage.StatusActive},
			{ID: "2", ResourceType: storage.ResourceTypeDNS, Status: storage.StatusOrphaned},
			{ID: "3", ResourceType: storage.ResourceTypeTunnelIngress, Status: storage.StatusActive},
		},
		agents: []*storage.Agent{},
	}
	s := newTestServer(store)
	req := httptest.NewRequest("GET", "/api/overview", nil)
	w := httptest.NewRecorder()

	s.handleOverview(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var body overviewResponse
	json.NewDecoder(w.Body).Decode(&body)

	if body.Resources.DNS.Total != 2 {
		t.Fatalf("expected 2 DNS total, got %d", body.Resources.DNS.Total)
	}
	if body.Resources.DNS.Active != 1 {
		t.Fatalf("expected 1 DNS active, got %d", body.Resources.DNS.Active)
	}
	if body.Resources.DNS.Orphaned != 1 {
		t.Fatalf("expected 1 DNS orphaned, got %d", body.Resources.DNS.Orphaned)
	}
	if body.Resources.TunnelIngress.Total != 1 {
		t.Fatalf("expected 1 tunnel total, got %d", body.Resources.TunnelIngress.Total)
	}
	if body.Version != "0.1.0-test" {
		t.Fatalf("expected version 0.1.0-test, got %s", body.Version)
	}
}
