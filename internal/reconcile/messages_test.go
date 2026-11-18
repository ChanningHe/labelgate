package reconcile

import (
	"errors"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

func mustContain(t *testing.T, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%q\nlacks %q", got, w)
		}
	}
}

func TestDescribeKey(t *testing.T) {
	tests := []struct {
		k    rowKey
		want string
	}{
		{rowKey{model.KindDNS, model.DNSKey("app.example.com", model.DNSTypeA, "")}, "the A record app.example.com"},
		{rowKey{model.KindDNS, model.DNSKey("app.example.com", model.DNSTypeTXT, "v=1")}, "the TXT record app.example.com (v=1)"},
		{rowKey{model.KindTunnel, model.TunnelKey("t1", "app.example.com", "")}, "the tunnel route app.example.com on tunnel t1"},
		{rowKey{model.KindTunnel, model.TunnelKey("t1", "app.example.com", "/api")}, "the tunnel route app.example.com path /api on tunnel t1"},
		{rowKey{model.KindAccess, model.AccessKey("app.example.com")}, "the Access application for app.example.com"},
	}
	for _, tt := range tests {
		if got := describeKey(tt.k); got != tt.want {
			t.Errorf("describeKey(%v) = %q, want %q", tt.k, got, tt.want)
		}
	}
}

func TestDescribeClass(t *testing.T) {
	for class, want := range map[string]string{
		classRecords: "DNS records", classCNAME: "a CNAME record", classTunnelPrefix + "t1": "a route on tunnel t1",
	} {
		if got := describeClass(class); got != want {
			t.Errorf("describeClass(%q) = %q, want %q", class, got, want)
		}
	}
}

func TestNotOwnedMessages(t *testing.T) {
	rec := cloudflare.DNSRecord{ID: "r1", Name: "app.example.com", Type: model.DNSTypeCNAME, Comment: "manual"}
	got := notOwnedRecord(rec, "abcd1234")
	mustContain(t, got, model.ConflictNotOwned, "a CNAME record for app.example.com (id r1)", "DNS > Records", "another hostname")
	if strings.Contains(got, "instance_id") {
		t.Errorf("foreign comment must not suggest instance_id: %q", got)
	}

	rec.Comment = model.DNSComment("old0001", "note")
	mustContain(t, notOwnedRecord(rec, "abcd1234"), `labelgate instance "old0001"`, `instance_id (env LABELGATE_INSTANCE_ID) to "old0001"`)

	mustContain(t, markerRemoved(rec, "abcd1234"), model.ConflictNotOwned, `"labelgate:abcd1234"`, "or delete the record")
	app := cloudflare.AccessApp{ID: "a1", Domain: "app.example.com"}
	mustContain(t, notOwnedApp(app), "Access application for app.example.com (id a1)", "Zero Trust > Access > Applications")
	mustContain(t, appTagRemoved(app, "labelgate-abcd1234"), `"labelgate-abcd1234"`, "add the tag back")
	mustContain(t, notOwnedRoute("t1", "app.example.com"), "tunnel t1 already routes app.example.com", "Zero Trust > Networks > Tunnels")
}

func TestZoneError(t *testing.T) {
	noZone := cloudflare.NoZoneError("app.example.org", "")
	if got := zoneError("app.example.org", noZone); got != noZone.Error() {
		t.Errorf("self-describing error was rewrapped: %q", got)
	}
	mustContain(t, zoneError("app.example.org", errors.New("boom")), "cannot find the Cloudflare zone for app.example.org: boom")
}
