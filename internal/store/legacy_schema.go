package store

// legacyMigration is one frozen labelgate v0.1/v0.2 schema step. The SQL is
// copied verbatim from internal/storage/sqlite.go at v0.2.1 and MUST NOT be
// edited: it exists so that databases left at any legacy version can be
// brought to the final v0.2 shape before the v0.2 -> 7 conversion runs.
type legacyMigration struct {
	version int
	sql     string
}

// legacyFinalVersion is the last schema version written by v0.2.
const legacyFinalVersion = 6

var legacyMigrations = []legacyMigration{
	{
		version: 1,
		sql: `
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
		version: 2,
		sql: `
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
		version: 3,
		sql: `
			-- Add access app fields to managed_resources
			ALTER TABLE managed_resources ADD COLUMN access_app_id TEXT;
			ALTER TABLE managed_resources ADD COLUMN account_id TEXT;
		`,
	},
	{
		version: 4,
		sql: `
			-- Add last_error field for per-resource error tracking
			ALTER TABLE managed_resources ADD COLUMN last_error TEXT;
		`,
	},
	{
		version: 5,
		sql: `
			-- Add access display fields for dashboard
			ALTER TABLE managed_resources ADD COLUMN access_app_name TEXT;
			ALTER TABLE managed_resources ADD COLUMN access_policy_name TEXT;
			ALTER TABLE managed_resources ADD COLUMN access_decision TEXT;
		`,
	},
	{
		version: 6,
		sql: `
			-- Persist the credential name so update/delete operations use the
			-- same Cloudflare credential the resource was created with
			ALTER TABLE managed_resources ADD COLUMN credential TEXT;
		`,
	},
}

// legacySyncStateDDL is the table the v0.2 getSchemaVersion created on demand
// before running migrations; it holds the legacy schema_version row.
const legacySyncStateDDL = `CREATE TABLE IF NOT EXISTS sync_state (
		key TEXT PRIMARY KEY,
		value TEXT,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`
