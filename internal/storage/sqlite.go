package storage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

// SQLiteStorage implements Storage interface using SQLite.
type SQLiteStorage struct {
	db   *sql.DB
	path string
}

// resourceColumns is the standard column list for resource queries.
const resourceColumns = `id, resource_type, cf_id, zone_id, hostname, record_type, content, proxied, ttl,
	tunnel_id, service, path, access_app_id, account_id, container_id, container_name, service_name, agent_id,
	status, cleanup_enabled, last_error, created_at, updated_at, deleted_at`

// NewSQLiteStorage creates a new SQLite storage instance.
func NewSQLiteStorage(path string) (*SQLiteStorage, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("failed to create storage directory: %w", err)
		}
	}

	// Open database with pure Go driver
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	return &SQLiteStorage{
		db:   db,
		path: path,
	}, nil
}

// Initialize creates tables and runs migrations.
func (s *SQLiteStorage) Initialize(ctx context.Context) error {
	// Get current schema version
	currentVersion := s.getSchemaVersion(ctx)

	// Run migrations
	for _, m := range migrations {
		if m.Version > currentVersion {
			if _, err := s.db.ExecContext(ctx, m.SQL); err != nil {
				return fmt.Errorf("migration %d failed: %w", m.Version, err)
			}
			if err := s.setSchemaVersion(ctx, m.Version); err != nil {
				return fmt.Errorf("failed to update schema version: %w", err)
			}
		}
	}

	return nil
}

// Close closes the database connection.
func (s *SQLiteStorage) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

// GetResource retrieves a resource by ID.
func (s *SQLiteStorage) GetResource(ctx context.Context, id string) (*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE id = ?
	`

	row := s.db.QueryRowContext(ctx, query, id)
	return s.scanResource(row)
}

// GetResourceByHostname retrieves a resource by hostname and type.
func (s *SQLiteStorage) GetResourceByHostname(ctx context.Context, hostname string, resourceType ResourceType) (*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE hostname = ? AND resource_type = ? AND status != 'deleted'
	`

	row := s.db.QueryRowContext(ctx, query, hostname, resourceType)
	return s.scanResource(row)
}

// GetResourceByContainerService retrieves a resource by container and service name.
func (s *SQLiteStorage) GetResourceByContainerService(ctx context.Context, containerID, serviceName string) (*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE container_id = ? AND service_name = ? AND status != 'deleted'
	`

	row := s.db.QueryRowContext(ctx, query, containerID, serviceName)
	return s.scanResource(row)
}

// ListResources lists resources with optional filtering.
func (s *SQLiteStorage) ListResources(ctx context.Context, filter ResourceFilter) ([]*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE 1=1
	`
	args := []interface{}{}

	if filter.ResourceType != "" {
		query += " AND resource_type = ?"
		args = append(args, filter.ResourceType)
	}
	if filter.Hostname != "" {
		query += " AND hostname = ?"
		args = append(args, filter.Hostname)
	}
	if filter.ContainerID != "" {
		query += " AND container_id = ?"
		args = append(args, filter.ContainerID)
	}
	if filter.ServiceName != "" {
		query += " AND service_name = ?"
		args = append(args, filter.ServiceName)
	}
	if filter.AgentID != "" {
		query += " AND agent_id = ?"
		args = append(args, filter.AgentID)
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, len(filter.Statuses))
		for i, s := range filter.Statuses {
			placeholders[i] = "?"
			args = append(args, s)
		}
		query += " AND status IN (" + strings.Join(placeholders, ",") + ")"
	} else if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	query += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		query += " LIMIT ?"
		args = append(args, filter.Limit)
	}
	if filter.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, filter.Offset)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []*ManagedResource
	for rows.Next() {
		r, err := s.scanResourceRows(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}

	return resources, rows.Err()
}

// SaveResource creates or updates a resource.
// Uses upsert based on (resource_type, hostname, record_type) unique constraint.
func (s *SQLiteStorage) SaveResource(ctx context.Context, resource *ManagedResource) error {
	if resource.ID == "" {
		resource.ID = uuid.New().String()
	}
	if resource.CreatedAt.IsZero() {
		resource.CreatedAt = time.Now()
	}
	resource.UpdatedAt = time.Now()

	// Use ON CONFLICT for the unique constraint (resource_type, hostname, record_type)
	// This handles both:
	// - id conflict (updating existing resource by ID)
	// - unique constraint conflict (resource already exists with same type/hostname/record_type)
	query := `
		INSERT INTO managed_resources (
			id, resource_type, cf_id, zone_id, hostname, record_type, content, proxied, ttl,
			tunnel_id, service, path, access_app_id, account_id, container_id, container_name, service_name, agent_id,
			status, cleanup_enabled, last_error, created_at, updated_at, deleted_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(resource_type, hostname, record_type) DO UPDATE SET
			cf_id = excluded.cf_id,
			zone_id = excluded.zone_id,
			content = excluded.content,
			proxied = excluded.proxied,
			ttl = excluded.ttl,
			tunnel_id = excluded.tunnel_id,
			service = excluded.service,
			path = excluded.path,
			access_app_id = excluded.access_app_id,
			account_id = excluded.account_id,
			container_id = excluded.container_id,
			container_name = excluded.container_name,
			service_name = excluded.service_name,
			agent_id = excluded.agent_id,
			status = excluded.status,
			cleanup_enabled = excluded.cleanup_enabled,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at,
			deleted_at = excluded.deleted_at
	`

	_, err := s.db.ExecContext(ctx, query,
		resource.ID, resource.ResourceType, resource.CFID, resource.ZoneID,
		resource.Hostname, resource.RecordType, resource.Content, resource.Proxied, resource.TTL,
		resource.TunnelID, resource.Service, resource.Path,
		resource.AccessAppID, resource.AccountID,
		resource.ContainerID, resource.ContainerName, resource.ServiceName, resource.AgentID,
		resource.Status, resource.CleanupEnabled, resource.LastError, resource.CreatedAt, resource.UpdatedAt, resource.DeletedAt,
	)
	return err
}

// UpdateResourceStatus updates the status of a resource.
func (s *SQLiteStorage) UpdateResourceStatus(ctx context.Context, id string, status ResourceStatus) error {
	query := `UPDATE managed_resources SET status = ?, updated_at = ? WHERE id = ?`

	now := time.Now()
	var deletedAt *time.Time
	if status == StatusDeleted {
		deletedAt = &now
	}

	if deletedAt != nil {
		query = `UPDATE managed_resources SET status = ?, updated_at = ?, deleted_at = ? WHERE id = ?`
		_, err := s.db.ExecContext(ctx, query, status, now, deletedAt, id)
		return err
	}

	_, err := s.db.ExecContext(ctx, query, status, now, id)
	return err
}

// UpdateResourceError updates the status and last_error of a resource.
// Pass an empty lastError string to clear the error.
func (s *SQLiteStorage) UpdateResourceError(ctx context.Context, id string, status ResourceStatus, lastError string) error {
	query := `UPDATE managed_resources SET status = ?, last_error = ?, updated_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, status, lastError, time.Now(), id)
	return err
}

// DeleteResource deletes a resource by ID.
func (s *SQLiteStorage) DeleteResource(ctx context.Context, id string) error {
	query := `DELETE FROM managed_resources WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// GetAgent retrieves an agent by ID.
func (s *SQLiteStorage) GetAgent(ctx context.Context, id string) (*Agent, error) {
	query := `
		SELECT id, name, connected, last_seen, public_ip, default_tunnel, status, created_at, updated_at
		FROM agents
		WHERE id = ?
	`

	row := s.db.QueryRowContext(ctx, query, id)
	return s.scanAgent(row)
}

// ListAgents lists all agents.
func (s *SQLiteStorage) ListAgents(ctx context.Context) ([]*Agent, error) {
	query := `
		SELECT id, name, connected, last_seen, public_ip, default_tunnel, status, created_at, updated_at
		FROM agents
		ORDER BY created_at DESC
	`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var agents []*Agent
	for rows.Next() {
		a, err := s.scanAgentRows(rows)
		if err != nil {
			return nil, err
		}
		agents = append(agents, a)
	}

	return agents, rows.Err()
}

// SaveAgent creates or updates an agent.
func (s *SQLiteStorage) SaveAgent(ctx context.Context, agent *Agent) error {
	if agent.CreatedAt.IsZero() {
		agent.CreatedAt = time.Now()
	}
	agent.UpdatedAt = time.Now()

	query := `
		INSERT INTO agents (id, name, connected, last_seen, public_ip, default_tunnel, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			connected = excluded.connected,
			last_seen = excluded.last_seen,
			public_ip = excluded.public_ip,
			default_tunnel = excluded.default_tunnel,
			status = excluded.status,
			updated_at = excluded.updated_at
	`

	_, err := s.db.ExecContext(ctx, query,
		agent.ID, agent.Name, agent.Connected, agent.LastSeen, agent.PublicIP,
		agent.DefaultTunnel, agent.Status, agent.CreatedAt, agent.UpdatedAt,
	)
	return err
}

// UpdateAgentStatus updates agent connection status.
func (s *SQLiteStorage) UpdateAgentStatus(ctx context.Context, id string, connected bool, status AgentStatus) error {
	query := `UPDATE agents SET connected = ?, status = ?, last_seen = ?, updated_at = ? WHERE id = ?`
	now := time.Now()
	_, err := s.db.ExecContext(ctx, query, connected, status, now, now, id)
	return err
}

// DeleteAgent deletes an agent by ID.
func (s *SQLiteStorage) DeleteAgent(ctx context.Context, id string) error {
	query := `DELETE FROM agents WHERE id = ?`
	_, err := s.db.ExecContext(ctx, query, id)
	return err
}

// GetSyncState retrieves a sync state value.
func (s *SQLiteStorage) GetSyncState(ctx context.Context, key string) (string, error) {
	query := `SELECT value FROM sync_state WHERE key = ?`
	var value string
	err := s.db.QueryRowContext(ctx, query, key).Scan(&value)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSyncState sets a sync state value.
func (s *SQLiteStorage) SetSyncState(ctx context.Context, key, value string) error {
	query := `
		INSERT INTO sync_state (key, value, updated_at)
		VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET
			value = excluded.value,
			updated_at = excluded.updated_at
	`
	_, err := s.db.ExecContext(ctx, query, key, value, time.Now())
	return err
}

// CleanupDeletedResources removes deleted resources older than the given time.
func (s *SQLiteStorage) CleanupDeletedResources(ctx context.Context, before time.Time) (int64, error) {
	query := `DELETE FROM managed_resources WHERE status = 'deleted' AND deleted_at < ?`
	result, err := s.db.ExecContext(ctx, query, before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// ListExpiredOrphans returns orphaned resources with cleanup_enabled=false
// whose updated_at is older than the given time (for orphan_ttl DB-only cleanup).
func (s *SQLiteStorage) ListExpiredOrphans(ctx context.Context, olderThan time.Time) ([]*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE status = 'orphaned' AND cleanup_enabled = FALSE AND updated_at < ?
		ORDER BY updated_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []*ManagedResource
	for rows.Next() {
		r, err := s.scanResourceRows(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}
	return resources, rows.Err()
}

// ListOrphanedForCleanup returns orphaned resources with cleanup_enabled=true
// whose updated_at is older than the given time (for remove_delay CF cleanup).
func (s *SQLiteStorage) ListOrphanedForCleanup(ctx context.Context, olderThan time.Time) ([]*ManagedResource, error) {
	query := `
		SELECT ` + resourceColumns + `
		FROM managed_resources
		WHERE status = 'orphaned' AND cleanup_enabled = TRUE AND updated_at < ?
		ORDER BY updated_at ASC
	`
	rows, err := s.db.QueryContext(ctx, query, olderThan)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var resources []*ManagedResource
	for rows.Next() {
		r, err := s.scanResourceRows(rows)
		if err != nil {
			return nil, err
		}
		resources = append(resources, r)
	}
	return resources, rows.Err()
}

// Vacuum performs database vacuum.
func (s *SQLiteStorage) Vacuum(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, "VACUUM")
	return err
}

// Helper methods

func (s *SQLiteStorage) getSchemaVersion(ctx context.Context) int {
	// Create sync_state table if it doesn't exist (for initial setup)
	s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`)

	value, err := s.GetSyncState(ctx, "schema_version")
	if err != nil || value == "" {
		return 0
	}
	var version int
	fmt.Sscanf(value, "%d", &version)
	return version
}

func (s *SQLiteStorage) setSchemaVersion(ctx context.Context, version int) error {
	return s.SetSyncState(ctx, "schema_version", fmt.Sprintf("%d", version))
}

func (s *SQLiteStorage) scanResource(row *sql.Row) (*ManagedResource, error) {
	r := &ManagedResource{}
	var cfID, zoneID, recordType, content, tunnelID, service, path, accessAppID, accountID, containerID, containerName, agentID, lastError sql.NullString
	var proxied sql.NullBool
	var ttl sql.NullInt64
	var deletedAt sql.NullTime

	err := row.Scan(
		&r.ID, &r.ResourceType, &cfID, &zoneID, &r.Hostname, &recordType, &content, &proxied, &ttl,
		&tunnelID, &service, &path, &accessAppID, &accountID, &containerID, &containerName, &r.ServiceName, &agentID,
		&r.Status, &r.CleanupEnabled, &lastError, &r.CreatedAt, &r.UpdatedAt, &deletedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	r.CFID = cfID.String
	r.ZoneID = zoneID.String
	r.RecordType = recordType.String
	r.Content = content.String
	r.Proxied = proxied.Bool
	r.TTL = int(ttl.Int64)
	r.TunnelID = tunnelID.String
	r.Service = service.String
	r.Path = path.String
	r.AccessAppID = accessAppID.String
	r.AccountID = accountID.String
	r.ContainerID = containerID.String
	r.ContainerName = containerName.String
	r.AgentID = agentID.String
	r.LastError = lastError.String
	if deletedAt.Valid {
		r.DeletedAt = &deletedAt.Time
	}

	return r, nil
}

func (s *SQLiteStorage) scanResourceRows(rows *sql.Rows) (*ManagedResource, error) {
	r := &ManagedResource{}
	var cfID, zoneID, recordType, content, tunnelID, service, path, accessAppID, accountID, containerID, containerName, agentID, lastError sql.NullString
	var proxied sql.NullBool
	var ttl sql.NullInt64
	var deletedAt sql.NullTime

	err := rows.Scan(
		&r.ID, &r.ResourceType, &cfID, &zoneID, &r.Hostname, &recordType, &content, &proxied, &ttl,
		&tunnelID, &service, &path, &accessAppID, &accountID, &containerID, &containerName, &r.ServiceName, &agentID,
		&r.Status, &r.CleanupEnabled, &lastError, &r.CreatedAt, &r.UpdatedAt, &deletedAt,
	)
	if err != nil {
		return nil, err
	}

	r.CFID = cfID.String
	r.ZoneID = zoneID.String
	r.RecordType = recordType.String
	r.Content = content.String
	r.Proxied = proxied.Bool
	r.TTL = int(ttl.Int64)
	r.TunnelID = tunnelID.String
	r.Service = service.String
	r.Path = path.String
	r.AccessAppID = accessAppID.String
	r.AccountID = accountID.String
	r.ContainerID = containerID.String
	r.ContainerName = containerName.String
	r.AgentID = agentID.String
	r.LastError = lastError.String
	if deletedAt.Valid {
		r.DeletedAt = &deletedAt.Time
	}

	return r, nil
}

func (s *SQLiteStorage) scanAgent(row *sql.Row) (*Agent, error) {
	a := &Agent{}
	var name, publicIP, defaultTunnel sql.NullString
	var lastSeen sql.NullTime

	err := row.Scan(
		&a.ID, &name, &a.Connected, &lastSeen, &publicIP, &defaultTunnel, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	a.Name = name.String
	a.PublicIP = publicIP.String
	a.DefaultTunnel = defaultTunnel.String
	if lastSeen.Valid {
		a.LastSeen = &lastSeen.Time
	}

	return a, nil
}

func (s *SQLiteStorage) scanAgentRows(rows *sql.Rows) (*Agent, error) {
	a := &Agent{}
	var name, publicIP, defaultTunnel sql.NullString
	var lastSeen sql.NullTime

	err := rows.Scan(
		&a.ID, &name, &a.Connected, &lastSeen, &publicIP, &defaultTunnel, &a.Status, &a.CreatedAt, &a.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	a.Name = name.String
	a.PublicIP = publicIP.String
	a.DefaultTunnel = defaultTunnel.String
	if lastSeen.Valid {
		a.LastSeen = &lastSeen.Time
	}

	return a, nil
}

// Migration represents a database migration.
type Migration struct {
	Version int
	SQL     string
}

// migrations is the list of database migrations.
var migrations = []Migration{
	{
		Version: 1,
		SQL: `
			CREATE TABLE IF NOT EXISTS managed_resources (
				id TEXT PRIMARY KEY,
				
				-- Resource identification
				resource_type TEXT NOT NULL,
				cf_id TEXT,
				
				-- DNS record fields
				zone_id TEXT,
				hostname TEXT NOT NULL,
				record_type TEXT,
				content TEXT,
				proxied BOOLEAN,
				ttl INTEGER,
				
				-- Tunnel fields
				tunnel_id TEXT,
				service TEXT,
				path TEXT,
				
				-- Source information
				container_id TEXT,
				container_name TEXT,
				service_name TEXT NOT NULL,
				agent_id TEXT,
				
				-- Status
				status TEXT DEFAULT 'active',
				cleanup_enabled BOOLEAN DEFAULT TRUE,
				
				-- Timestamps
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				deleted_at TIMESTAMP,
				
				-- Indexes
				UNIQUE(resource_type, hostname, record_type)
			);
			
			CREATE INDEX IF NOT EXISTS idx_resources_container ON managed_resources(container_id);
			CREATE INDEX IF NOT EXISTS idx_resources_agent ON managed_resources(agent_id);
			CREATE INDEX IF NOT EXISTS idx_resources_status ON managed_resources(status);
			CREATE INDEX IF NOT EXISTS idx_resources_hostname ON managed_resources(hostname);
			CREATE INDEX IF NOT EXISTS idx_resources_service ON managed_resources(container_id, service_name);
		`,
	},
	{
		Version: 2,
		SQL: `
			CREATE TABLE IF NOT EXISTS agents (
				id TEXT PRIMARY KEY,
				name TEXT,
				
				-- Connection info
				connected BOOLEAN DEFAULT FALSE,
				last_seen TIMESTAMP,
				public_ip TEXT,
				
				-- Configuration
				default_tunnel TEXT,
				
				-- Status
				status TEXT DEFAULT 'active',
				
				-- Timestamps
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
		`,
	},
	{
		Version: 3,
		SQL: `
			-- Add access app fields to managed_resources
			ALTER TABLE managed_resources ADD COLUMN access_app_id TEXT;
			ALTER TABLE managed_resources ADD COLUMN account_id TEXT;
		`,
	},
	{
		Version: 4,
		SQL: `
			-- Add last_error field for per-resource error tracking
			ALTER TABLE managed_resources ADD COLUMN last_error TEXT;
		`,
	},
}
