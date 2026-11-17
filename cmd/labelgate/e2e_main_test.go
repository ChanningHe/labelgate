package main

import (
	"encoding/json"
	"net"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
)

const (
	appHost    = "app.example.com"
	apiHost    = "api.example.com"
	ownedTag   = "labelgate-" + e2eInstanceID
	ownedMark  = "labelgate:" + e2eInstanceID
	tunnelCNAM = e2eTunnelID + ".cfargotunnel.com"
)

// webContainer has a DNS record (with user comment and Access), a tunnel
// route with an origin option and an Access policy definition.
func webContainer() model.Container {
	return model.Container{ID: "c-web", Name: "web", State: "running", Labels: map[string]string{
		"labelgate.dns.web.hostname":                  appHost,
		"labelgate.dns.web.comment":                   "frontend",
		"labelgate.dns.web.access":                    "team",
		"labelgate.tunnel.api.hostname":               apiHost,
		"labelgate.tunnel.api.service":                "http://web:8080",
		"labelgate.tunnel.api.origin.connect_timeout": "30s",
		"labelgate.access.team.policy.include.emails": "alice@example.com",
	}}
}

// tunnelIngress decodes the live ingress rules of the root tunnel.
func tunnelIngress(t *testing.T, cf *cfServer) (rules []map[string]any, extra map[string]json.RawMessage) {
	t.Helper()
	var all map[string]json.RawMessage
	if err := json.Unmarshal(cf.tunnelConfig(e2eTunnelID), &all); err != nil {
		t.Fatalf("tunnel config: %v", err)
	}
	if err := json.Unmarshal(all["ingress"], &rules); err != nil {
		t.Fatalf("tunnel ingress: %v", err)
	}
	delete(all, "ingress")
	return rules, all
}

// assertUnmanagedTunnelParts checks that the human-written rule, catch-all
// and global settings survived verbatim, with the given owned hostnames in front.
func assertUnmanagedTunnelParts(t *testing.T, cf *cfServer, owned ...string) {
	t.Helper()
	rules, extra := tunnelIngress(t, cf)
	var want struct {
		Ingress       []map[string]any `json:"ingress"`
		OriginRequest json.RawMessage  `json:"originRequest"`
		Warp          json.RawMessage  `json:"warp-routing"`
	}
	if err := json.Unmarshal([]byte(e2eUnmanagedTunnelConfig), &want); err != nil {
		t.Fatal(err)
	}
	if len(rules) != len(owned)+2 {
		t.Fatalf("ingress = %v, want %d owned + manual + catch-all", rules, len(owned))
	}
	for i, h := range owned {
		if rules[i]["hostname"] != h {
			t.Errorf("ingress[%d] = %v, want hostname %s", i, rules[i], h)
		}
	}
	if !reflect.DeepEqual(rules[len(owned):], want.Ingress) {
		t.Errorf("unmanaged rules / catch-all changed: %v, want %v", rules[len(owned):], want.Ingress)
	}
	if string(extra["originRequest"]) != string(want.OriginRequest) || string(extra["warp-routing"]) != string(want.Warp) || len(extra) != 2 {
		t.Errorf("global settings changed: %s", mustMarshal(extra))
	}
}

func resources(t *testing.T, p *proc, kind string) map[string]api.ResourceDTO {
	t.Helper()
	var list api.ResourceListDTO
	p.api("/resources/"+kind, &list)
	out := map[string]api.ResourceDTO{}
	for _, r := range list.Resources {
		out[r.Hostname+"|"+r.RecordType+"|"+r.Path] = r
	}
	return out
}

func wantResource(t *testing.T, got map[string]api.ResourceDTO, key, status string) api.ResourceDTO {
	t.Helper()
	r, ok := got[key]
	if !ok {
		t.Fatalf("resource %q missing; have %v", key, got)
	}
	if r.Status != status {
		t.Fatalf("resource %q status = %s (%s), want %s", key, r.Status, r.LastError, status)
	}
	return r
}

func onlyOne(t *testing.T, what string, objs []map[string]any) map[string]any {
	t.Helper()
	if len(objs) != 1 {
		t.Fatalf("%s: got %d objects, want 1: %v", what, len(objs), objs)
	}
	return objs[0]
}

// Scenario 1: DNS + tunnel + Access from a local container; second cycle is a no-op.
func TestE2ELocalContainerCreatesAndConverges(t *testing.T) {
	env := newEnv(t)
	m := env.startMain(env.mainConfig(), newScriptSource(env.clock.Now, webContainer()))

	if st := m.firstCycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("first cycle: %+v", st)
	}

	a := onlyOne(t, "A record", env.cf.find("dns:"+e2eZoneID, "name", appHost))
	if a["type"] != "A" || a["content"] != e2ePublicIP || a["comment"] != ownedMark+" frontend" || a["proxied"] != true {
		t.Errorf("A record = %v", a)
	}
	cname := onlyOne(t, "tunnel CNAME", env.cf.find("dns:"+e2eZoneID, "name", apiHost))
	if cname["type"] != "CNAME" || cname["content"] != tunnelCNAM || cname["comment"] != ownedMark {
		t.Errorf("CNAME = %v", cname)
	}
	assertUnmanagedTunnelParts(t, env.cf, apiHost)
	rules, _ := tunnelIngress(t, env.cf)
	if rules[0]["service"] != "http://web:8080" || !reflect.DeepEqual(rules[0]["originRequest"], map[string]any{"connectTimeout": 30.0}) {
		t.Errorf("owned rule = %v", rules[0])
	}
	app := onlyOne(t, "Access app", env.cf.objects("apps:"+e2eAccount))
	if app["domain"] != appHost || app["name"] != "labelgate-team" || !reflect.DeepEqual(app["tags"], []any{ownedTag}) {
		t.Errorf("app = %v", app)
	}
	pol := onlyOne(t, "Access policy", env.cf.objects("policies:"+e2eAccount))
	if links, _ := app["policies"].([]any); len(links) != 1 || links[0].(map[string]any)["id"] != pol["id"] {
		t.Errorf("app policies = %v, policy = %v", app["policies"], pol)
	}
	onlyOne(t, "ownership tag", env.cf.find("tags:"+e2eAccount, "name", ownedTag))

	m.waitAPI()
	dns := resources(t, m, "dns")
	wantResource(t, dns, appHost+"|A|", "active")
	wantResource(t, dns, apiHost+"|CNAME|", "active")
	wantResource(t, resources(t, m, "tunnels"), apiHost+"||", "active")
	wantResource(t, resources(t, m, "access"), appHost+"||", "active")

	before := len(env.cf.mutations())
	if st := m.cycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("second cycle: %+v", st)
	}
	if after := env.cf.mutations(); len(after) != before {
		t.Fatalf("second cycle made %d mutating requests: %v", len(after)-before, after[before:])
	}
}

// Scenario 2: an unowned record with the same name is a visible conflict and stays untouched.
func TestE2EUnownedRecordIsConflictAndUntouched(t *testing.T) {
	env := newEnv(t)
	env.cf.seed("dns:"+e2eZoneID, map[string]any{
		"id": "human-1", "name": "taken.example.com", "type": "A", "content": "192.0.2.1",
		"proxied": false, "ttl": 300, "comment": "hand-made",
	})
	src := newScriptSource(env.clock.Now, model.Container{ID: "c2", Name: "intruder", Labels: map[string]string{
		"labelgate.dns.web.hostname": "taken.example.com",
		"labelgate.dns.web.target":   "198.51.100.7",
	}})
	m := env.startMain(env.mainConfig(), src)

	if st := m.firstCycle(); st.State != reconcile.SyncError || !strings.Contains(st.Error, model.ConflictNotOwned) {
		t.Fatalf("status = %+v, want conflict error", st)
	}
	m.waitAPI()
	r := wantResource(t, resources(t, m, "dns"), "taken.example.com|A|", "error")
	if !strings.Contains(r.LastError, model.ConflictNotOwned) || r.CFID != "" {
		t.Fatalf("resource = %+v", r)
	}
	var ov api.OverviewDTO
	m.api("/overview", &ov)
	if ov.Sync.Status != "error" || ov.Resources.DNS.Error != 1 {
		t.Fatalf("overview = %+v", ov)
	}
	rec := onlyOne(t, "record", env.cf.find("dns:"+e2eZoneID, "name", "taken.example.com"))
	if rec["id"] != "human-1" || rec["content"] != "192.0.2.1" || rec["comment"] != "hand-made" {
		t.Fatalf("record modified: %v", rec)
	}
	if mut := env.cf.mutations(); len(mut) != 0 {
		t.Fatalf("mutating requests: %v", mut)
	}
}

// Scenario 3: removal waits for remove_delay, then deletes only owned resources.
func TestE2EContainerRemovalHonoursRemoveDelay(t *testing.T) {
	env := newEnv(t)
	env.cf.seed("dns:"+e2eZoneID, map[string]any{"id": "human-2", "name": "other.example.com", "type": "A", "content": "192.0.2.2"})
	env.cf.seed("apps:"+e2eAccount, map[string]any{"id": "human-app", "name": "Foreign", "domain": "foreign.example.com", "tags": []any{}})
	src := newScriptSource(env.clock.Now, webContainer())
	cfg := env.mainConfig()
	m := env.startMain(cfg, src)
	if st := m.firstCycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("first cycle: %+v", st)
	}

	src.set()
	before := len(env.cf.mutations())
	if st := m.cycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("removal cycle: %+v", st)
	}
	if mut := env.cf.mutations(); len(mut) != before {
		t.Fatalf("mutations before remove_delay: %v", mut[before:])
	}
	m.waitAPI()
	wantResource(t, resources(t, m, "dns"), appHost+"|A|", "pending_cleanup")
	wantResource(t, resources(t, m, "tunnels"), apiHost+"||", "pending_cleanup")

	env.clock.Advance(cfg.Sync.RemoveDelay + time.Second)
	if st := m.cycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("cleanup cycle: %+v", st)
	}
	records := env.cf.objects("dns:" + e2eZoneID)
	if len(records) != 1 || records[0]["id"] != "human-2" {
		t.Fatalf("records after cleanup = %v, want only the unmanaged one", records)
	}
	assertUnmanagedTunnelParts(t, env.cf)
	apps := env.cf.objects("apps:" + e2eAccount)
	if len(apps) != 1 || apps[0]["id"] != "human-app" {
		t.Fatalf("apps after cleanup = %v", apps)
	}
	if pols := env.cf.objects("policies:" + e2eAccount); len(pols) != 0 {
		t.Fatalf("owned policies left behind: %v", pols)
	}
	for _, kind := range []string{"dns", "tunnels", "access"} {
		if got := resources(t, m, kind); len(got) != 0 {
			t.Fatalf("%s rows left: %v", kind, got)
		}
	}
	before = len(env.cf.mutations())
	m.cycle()
	if mut := env.cf.mutations(); len(mut) != before {
		t.Fatalf("cycle after cleanup mutated: %v", mut[before:])
	}
}

// Scenario 6a: without api.token the API is never served; the reconciler still runs.
func TestE2EAPIDisabledWithoutToken(t *testing.T) {
	env := newEnv(t)
	cfg := env.mainConfig()
	cfg.API.Token = ""
	m := env.startMain(cfg, newScriptSource(env.clock.Now, webContainer()))
	if st := m.firstCycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("first cycle: %+v", st)
	}
	m.waitLog("api.token is empty")
	if conn, err := net.DialTimeout("tcp", cfg.API.Address, time.Second); err == nil {
		_ = conn.Close()
		t.Fatalf("API address %s accepts connections without a token", cfg.API.Address)
	}
}

// Scenario 6b: bearer auth and /overview reflecting pending -> success -> error.
func TestE2EAPIAuthAndOverview(t *testing.T) {
	env := newEnv(t)
	src := newScriptSource(env.clock.Now, webContainer())
	release := src.hold()
	m := env.startMain(env.mainConfig(), src)
	m.waitAPI()

	for _, tok := range []string{"", "wrong-token-0123456789"} {
		code, body, err := m.apiGet("/overview", tok)
		if err != nil || code != http.StatusUnauthorized {
			t.Fatalf("token %q: code=%d err=%v body=%s", tok, code, err, body)
		}
	}
	var ov api.OverviewDTO
	m.api("/overview", &ov)
	if ov.Sync.Status != "pending" || ov.Sync.Error != "waiting for first sync" {
		t.Fatalf("overview before first sync = %+v", ov.Sync)
	}

	release()
	m.firstCycle()
	m.api("/overview", &ov)
	if ov.Sync.Status != "success" || ov.Sync.Error != "" || ov.Resources.DNS.Active != 2 ||
		ov.Resources.TunnelIngress.Active != 1 || ov.Resources.AccessApp.Active != 1 || !ov.Cloudflare.Reachable {
		t.Fatalf("overview after sync = %+v", ov)
	}

	env.cf.seed("dns:"+e2eZoneID, map[string]any{"id": "human-3", "name": "late.example.com", "type": "A", "content": "192.0.2.3"})
	src.set(webContainer(), model.Container{ID: "c3", Name: "late", Labels: map[string]string{
		"labelgate.dns.x.hostname": "late.example.com", "labelgate.dns.x.target": "198.51.100.8",
	}})
	m.cycle()
	m.api("/overview", &ov)
	if ov.Sync.Status != "error" || !strings.Contains(ov.Sync.Error, model.ConflictNotOwned) || ov.Resources.DNS.Error != 1 {
		t.Fatalf("overview after conflict = %+v", ov)
	}
}
