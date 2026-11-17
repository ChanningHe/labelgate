package reconcile

import (
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// legacyOrphan seeds a migrated v0.2 row that was orphaned long ago.
func (h *harness) legacyOrphan(r model.Resource) model.Resource {
	r.Hostname, r.Legacy, r.Cleanup = appHost, true, true
	r.Owner = model.Owner{ContainerName: "web", Service: "web"}
	r.Status, r.OrphanedAt = model.StatusPendingCleanup, h.clk.now().Add(-time.Hour)
	return h.st.seed(r)
}

// Rows migrated with credential "default" while the v0.3 config has no root
// credential are re-bound by zone matching instead of failing forever.
func TestOrphanWithUnknownCredentialIsReboundAndDeleted(t *testing.T) {
	h := newHarness(t)
	rec := h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeA, Content: "192.0.2.1", TTL: 1, Comment: "Managed by labelgate"})
	h.legacyOrphan(model.Resource{Kind: model.KindDNS, Key: appKey, RecordType: model.DNSTypeA, Content: "192.0.2.1",
		Credential: "gone", ZoneID: testZone, AccountID: testAccount, CFID: rec.ID})
	app := h.cf.PutApp(testAccount, cloudflare.AccessApp{Name: "labelgate-team", Domain: appHost, SessionDuration: "24h"})
	h.legacyOrphan(model.Resource{Kind: model.KindAccess, Key: model.AccessKey(appHost), AppName: "labelgate-team",
		Credential: "gone", AccountID: testAccount, CFID: app.ID})
	h.cycle()
	if len(h.recordsNamed(appHost)) != 0 || len(h.cf.Apps(testAccount)) != 0 {
		t.Fatalf("not deleted: records %+v apps %+v", h.records(), h.cf.Apps(testAccount))
	}
	if rows := h.st.list(false); len(rows) != 0 {
		t.Fatalf("rows %+v", rows)
	}
}

// A synthesized v0.2 tunnel CNAME row has no record ID: when it is cleaned
// up, the live CNAME is found by name and content and deleted.
func TestOrphanedLegacyTunnelCNAMEWithoutIDIsDeleted(t *testing.T) {
	for _, zoneID := range []string{testZone, ""} {
		h := newHarness(t)
		h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeCNAME, Content: targetA, Proxied: true, TTL: 1, Comment: "Managed by labelgate"})
		h.legacyOrphan(model.Resource{Kind: model.KindDNS, Key: cnameKey, RecordType: model.DNSTypeCNAME, Content: targetA,
			Proxied: true, TTL: 1, TunnelID: tunnelA, Credential: "default", ZoneID: zoneID, AccountID: testAccount})
		h.cycle()
		if recs := h.recordsNamed(appHost); len(recs) != 0 {
			t.Fatalf("zone %q: CNAME leaked: %+v", zoneID, recs)
		}
		h.noRow(model.KindDNS, cnameKey)
	}
}

// A CNAME with other content is not ours: the row is dropped, the record kept.
func TestOrphanedLegacyCNAMEWithoutIDLeavesForeignRecord(t *testing.T) {
	h := newHarness(t)
	h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeCNAME, Content: "elsewhere.example.net", Proxied: true, TTL: 1})
	h.legacyOrphan(model.Resource{Kind: model.KindDNS, Key: cnameKey, RecordType: model.DNSTypeCNAME, Content: targetA,
		Proxied: true, TTL: 1, TunnelID: tunnelA, Credential: "default", ZoneID: testZone, AccountID: testAccount})
	h.cycle()
	if recs := h.recordsNamed(appHost); len(recs) != 1 {
		t.Fatalf("foreign record touched: %+v", recs)
	}
	h.noRow(model.KindDNS, cnameKey)
}
