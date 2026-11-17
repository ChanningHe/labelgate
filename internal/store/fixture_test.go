package store

import (
	"database/sql"
	"sort"
	"strings"
	"testing"
	"time"
)

// Timestamps written the way v0.2 wrote them: time.Time bound through the
// modernc driver, which stores time.Time.String() text in the writer's zone.
var (
	fixtureZone = time.FixedZone("CST", 8*3600)
	fxCreated   = time.Date(2026, 9, 1, 10, 0, 0, 123456789, fixtureZone)
	fxOlder     = time.Date(2026, 10, 1, 10, 0, 0, 0, fixtureZone)
	fxNewer     = time.Date(2026, 10, 5, 10, 0, 0, 0, fixtureZone)
)

// insertLegacyRow inserts one managed_resources row; nil values become NULL.
func insertLegacyRow(t *testing.T, db *sql.DB, cols map[string]any) {
	t.Helper()
	names := make([]string, 0, len(cols))
	for c := range cols {
		names = append(names, c)
	}
	sort.Strings(names)
	args := make([]any, len(names))
	for i, c := range names {
		args[i] = cols[c]
	}
	q := "INSERT INTO managed_resources (" + strings.Join(names, ", ") + ") VALUES (" +
		placeholders(len(names)) + ")"
	mustExec(t, db, q, args...)
}

// legacyRowDefaults are the columns every fixture row sets.
func legacyFixtureRow(id, rtype, host, svc string, extra map[string]any) map[string]any {
	m := map[string]any{
		"id": id, "resource_type": rtype, "hostname": host, "service_name": svc,
		"status": "active", "cleanup_enabled": true,
		"created_at": fxCreated, "updated_at": fxOlder,
	}
	for k, v := range extra {
		m[k] = v
	}
	return m
}

// seedLegacyV6 fills a v0.2 (schema 6) database with the representative rows
// used by the migration tests.
func seedLegacyV6(t *testing.T, db *sql.DB) {
	t.Helper()
	rows := []map[string]any{
		// Case + trailing dot duplicates: the newer updated_at must win.
		legacyFixtureRow("dns-1", "dns", "App.Example.com.", "web", map[string]any{
			"cf_id": "cf-a", "zone_id": "zone1", "record_type": "A", "content": "1.1.1.1",
			"proxied": true, "ttl": 1, "container_id": "c1", "container_name": "web", "credential": "default",
		}),
		legacyFixtureRow("dns-2", "dns", "app.example.com", "web", map[string]any{
			"cf_id": "cf-a2", "zone_id": "zone1", "record_type": "A", "content": "2.2.2.2",
			"proxied": true, "ttl": 1, "container_id": "c1", "container_name": "web", "credential": "default",
			"updated_at": fxNewer,
		}),
		legacyFixtureRow("dns-3", "dns", "txt.example.com", "txt", map[string]any{
			"cf_id": "cf-t", "zone_id": "zone1", "record_type": "TXT", "content": "v=spf1 -all",
			"status": "orphaned", "updated_at": fxNewer,
		}),
		legacyFixtureRow("dns-4", "dns", "keep.example.com", "keep", map[string]any{
			"cf_id": "cf-k", "zone_id": "zone1", "record_type": "A", "content": "3.3.3.3",
			"status": "orphaned", "cleanup_enabled": false,
		}),
		legacyFixtureRow("dns-5", "dns", "gone.example.com", "gone", map[string]any{
			"record_type": "A", "status": "deleted", "deleted_at": fxNewer,
		}),
		// Multi-path tunnel rows: record_type NULL so v0.2's UNIQUE allowed several per host.
		legacyFixtureRow("tun-1", "tunnel_ingress", "api.example.com", "svcb", map[string]any{
			"tunnel_id": "tid-b", "path": "/v2", "service": "http://web:80", "zone_id": "zone1",
			"container_name": "api", "agent_id": "edge1", "credential": "main",
		}),
		legacyFixtureRow("tun-2", "tunnel_ingress", "api.example.com", "svca", map[string]any{
			"tunnel_id": "tid-b", "path": "/v1", "service": "http://a:80", "zone_id": "zone1",
			"container_name": "api2", "updated_at": fxNewer,
		}),
		legacyFixtureRow("tun-3", "tunnel_ingress", "web.example.com", "web", map[string]any{
			"tunnel_id": "tid-b", "path": "", "service": "http://web:80",
		}),
		legacyFixtureRow("dns-6", "dns", "web.example.com", "web", map[string]any{
			"record_type": "CNAME", "content": "tid-b.cfargotunnel.com", "cf_id": "cf-c",
		}),
		// Same hostname in two tunnels: only one CNAME may exist.
		legacyFixtureRow("tun-4", "tunnel_ingress", "same.example.com", "s1", map[string]any{
			"tunnel_id": "tid-c", "service": "http://x:80",
		}),
		legacyFixtureRow("tun-5", "tunnel_ingress", "same.example.com", "s2", map[string]any{
			"tunnel_id": "tid-a", "path": "/p", "service": "http://y:80",
		}),
		// Access app: the ID lives in access_app_id, cf_id is NULL.
		legacyFixtureRow("acc-1", "access_app", "secure.example.com", "sec", map[string]any{
			"access_app_id": "app-1", "account_id": "acct1", "access_app_name": "Secure",
			"access_policy_name": "p", "access_decision": "allow",
		}),
		legacyFixtureRow("dns-7", "dns", "err.example.com", "err", map[string]any{
			"record_type": "A", "content": "4.4.4.4", "status": "error", "last_error": "boom",
			"cleanup_enabled": nil,
		}),
		legacyFixtureRow("dns-8", "dns", "badtime.example.com", "bt", map[string]any{
			"record_type": "A", "content": "5.5.5.5", "created_at": "not-a-time", "updated_at": nil,
		}),
		legacyFixtureRow("dns-9", "dns", "mono.example.com", "mono", map[string]any{
			"record_type": "A", "content": "6.6.6.6",
			"created_at": "2026-09-01 10:00:00.5 +0800 CST m=+12.345", "updated_at": "2026-09-02 03:04:05",
		}),
		legacyFixtureRow("dns-10", "dns", "weird.example.com", "weird", map[string]any{
			"record_type": "A", "content": "7.7.7.7", "status": "weird",
		}),
	}
	for _, r := range rows {
		insertLegacyRow(t, db, r)
	}
	mustExec(t, db, `INSERT INTO agents (id, name, connected, last_seen, public_ip, default_tunnel, status, created_at, updated_at)
		VALUES ('edge1', 'Edge One', 1, ?, '9.9.9.9', 'tid-b', 'active', ?, ?)`, fxNewer, fxCreated, fxOlder)
	mustExec(t, db, `INSERT INTO agents (id) VALUES ('edge2')`)
}
