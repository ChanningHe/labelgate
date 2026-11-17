package store

// schemaV7 is the labelgate v0.3 schema (see dev/docs/v0.3-design.md §5.1).
// All timestamps are INTEGER unix seconds UTC; zero time is stored as NULL
// where the column is nullable.
const schemaV7 = `
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
) STRICT;

CREATE TABLE resources (
  id             INTEGER PRIMARY KEY,
  kind           TEXT    NOT NULL CHECK (kind IN ('dns','tunnel_ingress','access_app')),
  key            TEXT    NOT NULL,
  rejected       INTEGER NOT NULL DEFAULT 0 CHECK (rejected IN (0,1)),
  hostname       TEXT    NOT NULL,
  agent_id       TEXT    NOT NULL DEFAULT '',
  container_name TEXT    NOT NULL DEFAULT '',
  container_id   TEXT    NOT NULL DEFAULT '',
  service_name   TEXT    NOT NULL,
  credential     TEXT    NOT NULL DEFAULT '',
  account_id     TEXT    NOT NULL DEFAULT '',
  zone_id        TEXT    NOT NULL DEFAULT '',
  cf_id          TEXT    NOT NULL DEFAULT '',
  record_type    TEXT    NOT NULL DEFAULT '',
  content        TEXT    NOT NULL DEFAULT '',
  proxied        INTEGER NOT NULL DEFAULT 0,
  ttl            INTEGER NOT NULL DEFAULT 0,
  priority       INTEGER NOT NULL DEFAULT 0,
  weight         INTEGER NOT NULL DEFAULT 0,
  port           INTEGER NOT NULL DEFAULT 0,
  flags          INTEGER NOT NULL DEFAULT 0,
  tag            TEXT    NOT NULL DEFAULT '',
  comment        TEXT    NOT NULL DEFAULT '',
  tunnel_id      TEXT    NOT NULL DEFAULT '',
  path           TEXT    NOT NULL DEFAULT '',
  service        TEXT    NOT NULL DEFAULT '',
  origin_json    TEXT    NOT NULL DEFAULT '{}',
  app_name       TEXT    NOT NULL DEFAULT '',
  policy_name    TEXT    NOT NULL DEFAULT '',
  decision       TEXT    NOT NULL DEFAULT '',
  policy_ids     TEXT    NOT NULL DEFAULT '[]',
  status         TEXT    NOT NULL CHECK (status IN ('pending','active','error','orphaned','pending_cleanup')),
  cleanup        INTEGER NOT NULL DEFAULT 1,
  last_error     TEXT    NOT NULL DEFAULT '',
  legacy         INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  orphaned_at    INTEGER,
  synced_at      INTEGER
) STRICT;
CREATE UNIQUE INDEX ux_resources_owned    ON resources(kind, key) WHERE rejected = 0;
CREATE UNIQUE INDEX ux_resources_rejected ON resources(kind, key, agent_id, container_name, service_name) WHERE rejected = 1;
CREATE INDEX ix_resources_hostname    ON resources(hostname);
CREATE INDEX ix_resources_agent       ON resources(agent_id);
CREATE INDEX ix_resources_kind_status ON resources(kind, status);

CREATE TABLE agents (
  id             TEXT PRIMARY KEY,
  name           TEXT NOT NULL DEFAULT '',
  default_tunnel TEXT NOT NULL DEFAULT '',
  public_ip      TEXT NOT NULL DEFAULT '',
  public_ipv6    TEXT NOT NULL DEFAULT '',
  last_seen      INTEGER,
  snapshot_json  TEXT,
  snapshot_hash  TEXT NOT NULL DEFAULT '',
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
) STRICT;
`
