package main

import (
	"context"
	"database/sql"
	"os"
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/reconcile"
	"github.com/channinghe/labelgate/internal/store"
)

// legacyV6DDL is the final labelgate v0.2 schema (legacy version 6): the
// v0.2.1 migrations 1..6 collapsed into their resulting tables.
const legacyV6DDL = `
CREATE TABLE sync_state (key TEXT PRIMARY KEY, value TEXT, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE managed_resources (
	id TEXT PRIMARY KEY, resource_type TEXT NOT NULL, cf_id TEXT,
	zone_id TEXT, hostname TEXT NOT NULL, record_type TEXT, content TEXT, proxied BOOLEAN, ttl INTEGER,
	tunnel_id TEXT, service TEXT, path TEXT,
	container_id TEXT, container_name TEXT, service_name TEXT NOT NULL, agent_id TEXT,
	status TEXT DEFAULT 'active', cleanup_enabled BOOLEAN DEFAULT TRUE,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, deleted_at TIMESTAMP,
	access_app_id TEXT, account_id TEXT, last_error TEXT,
	access_app_name TEXT, access_policy_name TEXT, access_decision TEXT, credential TEXT,
	UNIQUE(resource_type, hostname, record_type));
CREATE TABLE agents (
	id TEXT PRIMARY KEY, name TEXT, connected BOOLEAN DEFAULT FALSE, last_seen TIMESTAMP, public_ip TEXT,
	default_tunnel TEXT, status TEXT DEFAULT 'active',
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP);
INSERT INTO sync_state (key, value) VALUES ('schema_version', '6');
`

// legacyV6Rows is what v0.2 tracked for webContainer(): the A record, the
// tunnel rule (its CNAME was created but never tracked) and the Access app.
var legacyV6Rows = []string{
	`INSERT INTO managed_resources (id, resource_type, cf_id, zone_id, hostname, record_type, content, proxied, ttl,
		container_id, container_name, service_name, credential, created_at, updated_at)
	VALUES ('r1', 'dns', 'rec-app', 'zone1', 'app.example.com', 'A', '203.0.113.10', 1, 1,
		'c-web-old', 'web', 'web', 'default', '2026-09-01 10:00:00', '2026-09-02 10:00:00')`,
	`INSERT INTO managed_resources (id, resource_type, zone_id, hostname, tunnel_id, service, path,
		container_id, container_name, service_name, credential, created_at, updated_at)
	VALUES ('r2', 'tunnel_ingress', 'zone1', 'api.example.com', '` + e2eTunnelID + `', 'http://web:8080', '',
		'c-web-old', 'web', 'api', 'default', '2026-09-01 10:00:00', '2026-09-02 10:00:00')`,
	`INSERT INTO managed_resources (id, resource_type, hostname, access_app_id, account_id, access_app_name,
		access_policy_name, access_decision, container_id, container_name, service_name, created_at, updated_at)
	VALUES ('r3', 'access_app', 'app.example.com', 'app-legacy', 'acct1', 'labelgate-team', 'team', 'allow',
		'c-web-old', 'web', 'web', '2026-09-01 10:00:00', '2026-09-02 10:00:00')`,
}

const legacyComment = "Managed by labelgate [container:web service:web]"

func writeLegacyDB(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range append([]string{legacyV6DDL}, legacyV6Rows...) {
		if _, err := db.Exec(q); err != nil {
			t.Fatalf("legacy fixture: %v\n%s", err, q)
		}
	}
}

// seedLegacyCloudflare is the live state v0.2 left behind for legacyV6Rows.
func seedLegacyCloudflare(env *e2eEnv) {
	env.cf.seed("dns:"+e2eZoneID, map[string]any{"id": "rec-app", "name": appHost, "type": "A",
		"content": e2ePublicIP, "proxied": true, "ttl": 1, "comment": legacyComment})
	env.cf.seed("dns:"+e2eZoneID, map[string]any{"id": "rec-cname", "name": apiHost, "type": "CNAME",
		"content": tunnelCNAM, "proxied": true, "ttl": 1, "comment": "Managed by labelgate [container:web service:api]"})
	env.cf.setTunnelConfig(e2eTunnelID, `{"ingress":[`+
		`{"hostname":"api.example.com","service":"http://web:8080"},`+
		`{"hostname":"manual.example.com","service":"http://manual:80","originRequest":{"noTLSVerify":true}},`+
		`{"service":"http_status:418"}],`+
		`"originRequest":{"connectTimeout":10},"warp-routing":{"enabled":true}}`)
	env.cf.seed("policies:"+e2eAccount, map[string]any{"id": "pol-legacy", "name": "team", "decision": "allow",
		"include": []any{map[string]any{"email": map[string]any{"email": "alice@example.com"}}}})
	env.cf.seed("apps:"+e2eAccount, map[string]any{"id": "app-legacy", "name": "labelgate-team", "domain": appHost,
		"type": "self_hosted", "session_duration": "24h", "tags": []any{},
		"policies": []any{map[string]any{"id": "pol-legacy", "precedence": 1}}})
}

func persistedInstanceID(t *testing.T, path string) string {
	t.Helper()
	st, err := store.Open(context.Background(), path, nil)
	if err != nil {
		t.Fatalf("reopen store: %v", err)
	}
	defer st.Close()
	id, err := st.InstanceID(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// Scenario 7: a v0.2 database and its Cloudflare state are migrated and
// adopted in place: backup, markers stamped, no duplicates, no deletions.
func TestE2EUpgradeFromV02AdoptsInPlace(t *testing.T) {
	env := newEnv(t)
	seedLegacyCloudflare(env)
	cfg := env.mainConfig()
	cfg.InstanceID = "" // a v0.2 install has none; the migration generates it
	writeLegacyDB(t, cfg.DB.Path)

	m := env.startMain(cfg, newScriptSource(env.clock.Now, webContainer()))
	if st := m.firstCycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("first cycle after upgrade: %+v", st)
	}
	before := len(env.cf.mutations())
	if st := m.cycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("second cycle: %+v", st)
	}
	if mut := env.cf.mutations(); len(mut) != before {
		t.Fatalf("second cycle after upgrade mutated: %v", mut[before:])
	}
	if err := m.stop(); err != nil {
		t.Fatalf("main: %v", err)
	}

	if _, err := os.Stat(cfg.DB.Path + ".v0.2.bak"); err != nil {
		t.Fatalf("legacy backup: %v", err)
	}
	id := persistedInstanceID(t, cfg.DB.Path)
	mark := "labelgate:" + id
	if n := env.cf.count("DELETE", "/"); n != 0 {
		t.Fatalf("upgrade deleted %d objects: %v", n, env.cf.mutations())
	}
	if n := env.cf.count("POST", "/dns_records"); n != 0 {
		t.Fatalf("upgrade created %d DNS records instead of adopting", n)
	}
	a := onlyOne(t, "A record", env.cf.find("dns:"+e2eZoneID, "name", appHost))
	if a["id"] != "rec-app" || a["comment"] != mark+" frontend" {
		t.Errorf("A record not stamped in place: %v", a)
	}
	c := onlyOne(t, "tunnel CNAME", env.cf.find("dns:"+e2eZoneID, "name", apiHost))
	if c["id"] != "rec-cname" || c["comment"] != mark {
		t.Errorf("tunnel CNAME not adopted in place: %v", c)
	}
	assertUnmanagedTunnelParts(t, env.cf, apiHost)
	app := onlyOne(t, "Access app", env.cf.objects("apps:"+e2eAccount))
	if app["id"] != "app-legacy" || !reflect.DeepEqual(app["tags"], []any{"labelgate-" + id}) {
		t.Errorf("Access app not tagged in place: %v", app)
	}
	if len(env.cf.find("policies:"+e2eAccount, "id", "pol-legacy")) != 1 {
		t.Errorf("v0.2 policy must never be deleted: %v", env.cf.objects("policies:"+e2eAccount))
	}
}
