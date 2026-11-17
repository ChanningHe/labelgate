//go:build integration

// End-to-end label -> Cloudflare tests using the real labels.Parse. They need
// the WP1 parser and are enabled with `go test -tags integration` once merged.
package reconcile

import (
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/labels"
	"github.com/channinghe/labelgate/internal/model"
)

func TestLabelsEndToEnd(t *testing.T) {
	h := newHarness(t)
	h.r.parse = labels.Parse
	h.src.set(model.Container{ID: "c1", Name: "web", Labels: map[string]string{
		"labelgate.dns.web.hostname":                  "App.Example.com.",
		"labelgate.dns.web.access":                    "team",
		"labelgate.tunnel.api.hostname":               "api.example.com",
		"labelgate.tunnel.api.service":                "http://web:8080",
		"labelgate.tunnel.api.path":                   "/v1",
		"labelgate.access.team.policy.include.emails": "a@example.com, b@example.com",
		"labelgate.access.team.session_duration":      "12h",
		"labelgate.tunnel.api.origin.connect_timeout": "30s",
	}})
	if st := h.cycle(); st.State != SyncSuccess {
		if strings.Contains(st.Error, "not implemented") {
			t.Skip("labels.Parse not implemented on this branch")
		}
		t.Fatalf("status %+v", st)
	}
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
	wantStatus(t, h.row(model.KindTunnel, model.TunnelKey(tunnelA, "api.example.com", "/v1")), model.StatusActive)
	wantStatus(t, h.row(model.KindDNS, model.DNSKey("api.example.com", model.DNSTypeCNAME, "")), model.StatusActive)
	if app := h.onlyApp(); app.SessionDuration != "12h" || len(app.PolicyIDs) != 1 {
		t.Fatalf("app %+v", app)
	}
	h.assertIdempotent()

	h.src.set(model.Container{ID: "c1", Name: "web", Labels: map[string]string{
		"labelgate.dns.web.hostname": "app.example.com",
		"labelgate.dns.web.ttl":      "not-a-number",
	}})
	h.cycle()
	if row := h.row(model.KindDNS, appKey); row.Status != model.StatusError || !strings.Contains(row.LastError, "label error") {
		t.Fatalf("invalid label must hold the record: %+v", row)
	}
}
