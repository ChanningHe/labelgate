package reconcile

import (
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/cloudflare/cffake"
	"github.com/channinghe/labelgate/internal/model"
)

const appHost = "app.example.com"

var appKey = model.DNSKey(appHost, model.DNSTypeA, "")

func TestStatusPendingBeforeFirstCycle(t *testing.T) {
	h := newHarness(t)
	if st := h.r.Status(); st.State != SyncPending || !st.LastSync.IsZero() {
		t.Fatalf("got %+v", st)
	}
	if st := h.cycle(); st.State != SyncSuccess || st.LastSync.IsZero() || st.Error != "" {
		t.Fatalf("got %+v", st)
	}
}

func TestDNSCreateUpdateIdempotent(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))

	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	recs := h.recordsNamed(appHost)
	if len(recs) != 1 || recs[0].Content != "203.0.113.10" || recs[0].Comment != ownMarker || !recs[0].Proxied || recs[0].TTL != 1 {
		t.Fatalf("records %+v", recs)
	}
	row := h.row(model.KindDNS, appKey)
	wantStatus(t, row, model.StatusActive)
	if row.CFID != recs[0].ID || row.ZoneID != testZone || row.Owner.ContainerName != "web" || row.SyncedAt.IsZero() {
		t.Fatalf("row %+v", row)
	}
	h.assertIdempotent()

	h.ip = netip.MustParseAddr("203.0.113.99")
	h.cf.ResetCalls()
	h.cycle()
	if got := h.cf.CountCalls(cffake.OpUpdateDNSRecord); got != 1 {
		t.Fatalf("updates = %d", got)
	}
	if recs := h.recordsNamed(appHost); recs[0].Content != "203.0.113.99" || recs[0].ID != row.CFID {
		t.Fatalf("records %+v", recs)
	}
	h.assertIdempotent()
}

func TestDNSLiteralRecordTypes(t *testing.T) {
	h := newHarness(t)
	txt := model.DNSSpec{Service: "txt", Hostname: appHost, Type: model.DNSTypeTXT, Target: "v=spf1 -all", Cleanup: true, Comment: "hello"}
	mx := model.DNSSpec{Service: "mx", Hostname: "example.com", Type: model.DNSTypeMX, Target: "Mail.Example.com.", Priority: 10, Cleanup: true}
	srv := model.DNSSpec{Service: "srv", Hostname: "_sip._tcp.example.com", Type: model.DNSTypeSRV, Target: "sip.example.com", Priority: 1, Weight: 2, Port: 5060, Cleanup: true}
	caa := model.DNSSpec{Service: "caa", Hostname: "example.com", Type: model.DNSTypeCAA, Target: "letsencrypt.org", Tag: "issue", Cleanup: true}
	v6 := model.DNSSpec{Service: "v6", Hostname: "v6.example.com", Type: model.DNSTypeAAAA, Target: model.TargetAuto, Cleanup: true}
	cip := model.DNSSpec{Service: "cip", Hostname: "c.example.com", Type: model.DNSTypeA, Target: model.TargetContainer, TTL: 300, Cleanup: true}
	h.src.set(ctr("web", dnsOnly(txt, mx, srv, caa, v6, cip)))

	if st := h.cycle(); st.State != SyncSuccess {
		t.Fatalf("status %+v", st)
	}
	want := map[string]string{
		"TXT " + appHost:            "v=spf1 -all",
		"MX example.com":            "mail.example.com",
		"SRV _sip._tcp.example.com": "sip.example.com",
		"CAA example.com":           "letsencrypt.org",
		"AAAA v6.example.com":       "2001:db8::10",
		"A c.example.com":           "172.17.0.5",
	}
	for _, rec := range h.records() {
		if want[string(rec.Type)+" "+rec.Name] != rec.Content {
			t.Errorf("unexpected record %+v", rec)
		}
		if rec.Type == model.DNSTypeTXT && rec.Comment != ownMarker+" hello" {
			t.Errorf("comment %q", rec.Comment)
		}
	}
	if len(h.records()) != len(want) {
		t.Fatalf("records %+v", h.records())
	}
	h.row(model.KindDNS, model.DNSKey("example.com", model.DNSTypeMX, "10 mail.example.com"))
	h.assertIdempotent()
}

func TestDNSCommentTruncated(t *testing.T) {
	h := newHarness(t)
	s := dnsSpec("web", appHost)
	s.Comment = strings.Repeat("x", 200)
	h.src.set(ctr("web", dnsOnly(s)))
	h.cycle()
	c := h.recordsNamed(appHost)[0].Comment
	if len(c) != maxCommentLen || !model.IsOwnedComment(testInstance, c) {
		t.Fatalf("comment %q (%d)", c, len(c))
	}
	h.assertIdempotent()
}

func TestDNSUnownedRecordIsConflictNeverTouched(t *testing.T) {
	tests := []struct {
		name string
		seed cloudflare.DNSRecord
	}{
		{"same identity", cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeA, Content: "1.1.1.1", Comment: "mine"}},
		{"foreign marker", cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeA, Content: "1.1.1.1", Comment: "labelgate:other1"}},
		{"cname exclusivity", cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeCNAME, Content: "x.example.net"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			seeded := h.cf.PutRecord(testZone, tc.seed)
			h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))

			st := h.cycle()
			if st.State != SyncError || !strings.Contains(st.Error, model.ConflictNotOwned) {
				t.Fatalf("status %+v", st)
			}
			if h.mutations() != 0 {
				t.Fatalf("mutations: %+v", h.cf.Calls())
			}
			row := h.row(model.KindDNS, appKey)
			if row.Status != model.StatusError || row.LastError != model.ConflictNotOwned || row.CFID != "" {
				t.Fatalf("row %+v", row)
			}
			h.src.set()
			h.cycle()
			h.clk.advance(31 * time.Minute)
			h.cycle()
			h.noRow(model.KindDNS, appKey)
			if recs := h.records(); len(recs) != 1 || recs[0] != seeded {
				t.Fatalf("foreign record changed: %+v", recs)
			}
		})
	}
}

func TestDNSCreateAlreadyExistsIsConflict(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cf.FailNext(cffake.OpCreateDNSRecord, cloudflare.ErrAlreadyExists)
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.LastError != model.ConflictNotOwned || row.CFID != "" {
		t.Fatalf("row %+v", row)
	}
}

func TestDNSAdoptsOwnMarkedRecord(t *testing.T) {
	h := newHarness(t)
	seeded := h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeA, Content: "203.0.113.10", Proxied: true, TTL: 1, Comment: ownMarker})
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	if h.mutations() != 0 {
		t.Fatalf("adoption must not mutate: %+v", h.cf.Calls())
	}
	row := h.row(model.KindDNS, appKey)
	if row.CFID != seeded.ID || row.Status != model.StatusActive {
		t.Fatalf("row %+v", row)
	}
}

func TestDNSMarkerRemovedIsNeverTouched(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	rec := h.recordsNamed(appHost)[0]
	rec.Comment, rec.Content = "taken over by a human", "9.9.9.9"
	h.cf.PutRecord(testZone, rec)

	h.cf.ResetCalls()
	h.cycle()
	if h.mutations() != 0 {
		t.Fatalf("mutations %+v", h.cf.Calls())
	}
	if row := h.row(model.KindDNS, appKey); !strings.HasPrefix(row.LastError, model.ConflictNotOwned) {
		t.Fatalf("row %+v", row)
	}
	h.src.set()
	h.cycle()
	h.clk.advance(time.Hour)
	h.cycle()
	h.noRow(model.KindDNS, appKey)
	if recs := h.recordsNamed(appHost); len(recs) != 1 || recs[0] != rec {
		t.Fatalf("record %+v", recs)
	}
}

func TestDNSCleanupLifecycle(t *testing.T) {
	tests := []struct {
		name       string
		cleanup    bool
		orphanTTL  time.Duration
		wantStatus model.Status
		wantGone   bool // CF record deleted after remove_delay
		wantPurged bool // DB row purged after orphan_ttl
	}{
		{"cleanup true", true, 0, model.StatusPendingCleanup, true, false},
		{"cleanup false keeps forever", false, 0, model.StatusOrphaned, false, false},
		{"cleanup false with orphan ttl", false, 2 * time.Hour, model.StatusOrphaned, false, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, func(s *Settings) { s.OrphanTTL = tc.orphanTTL })
			s := dnsSpec("web", appHost)
			s.Cleanup = tc.cleanup
			h.src.set(ctr("web", dnsOnly(s)))
			h.cycle()
			h.src.set()
			h.cycle()
			row := h.row(model.KindDNS, appKey)
			wantStatus(t, row, tc.wantStatus)
			if !row.OrphanedAt.Equal(h.clk.now()) || len(h.recordsNamed(appHost)) != 1 {
				t.Fatalf("row %+v", row)
			}
			h.clk.advance(29 * time.Minute)
			h.cycle()
			if len(h.recordsNamed(appHost)) != 1 {
				t.Fatal("deleted before remove_delay")
			}
			h.clk.advance(2 * time.Minute)
			h.cycle()
			if gone := len(h.recordsNamed(appHost)) == 0; gone != tc.wantGone {
				t.Fatalf("gone=%v", gone)
			}
			h.clk.advance(2 * time.Hour)
			h.cycle()
			_, hasRow := h.st.get(model.KindDNS, appKey)
			if wantRow := !tc.wantGone && !tc.wantPurged; hasRow != wantRow {
				t.Fatalf("hasRow=%v want %v", hasRow, wantRow)
			}
			if !tc.wantGone && len(h.recordsNamed(appHost)) != 1 {
				t.Fatal("record must stay")
			}
		})
	}
}

func TestDNSRevivalCancelsCleanup(t *testing.T) {
	h := newHarness(t)
	c := ctr("web", dnsOnly(dnsSpec("web", appHost)))
	h.src.set(c)
	h.cycle()
	h.src.set()
	h.cycle()
	h.clk.advance(20 * time.Minute)
	h.src.set(c)
	h.cycle()
	row := h.row(model.KindDNS, appKey)
	if row.Status != model.StatusActive || !row.OrphanedAt.IsZero() {
		t.Fatalf("row %+v", row)
	}
	h.clk.advance(time.Hour)
	h.cycle()
	if len(h.recordsNamed(appHost)) != 1 {
		t.Fatal("revived record deleted")
	}
}

func TestDNSDeleteFailureRetried(t *testing.T) {
	h := newHarness(t, func(s *Settings) { s.RemoveDelay = 0 })
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	h.src.set()
	h.cf.FailNext(cffake.OpDeleteDNSRecord, errBoom)
	if st := h.cycle(); st.State != SyncError {
		t.Fatalf("status %+v", st)
	}
	wantStatus(t, h.row(model.KindDNS, appKey), model.StatusError)
	h.cycle()
	h.noRow(model.KindDNS, appKey)
	if len(h.records()) != 0 {
		t.Fatal("record not deleted on retry")
	}
}

func TestDNSTransientErrorsRetryNextCycle(t *testing.T) {
	tests := []struct {
		op  cffake.Op
		msg string
	}{
		{cffake.OpCreateDNSRecord, "create dns record"},
		{cffake.OpListDNSRecords, "list dns records"},
		{cffake.OpResolveZone, "zone for"},
	}
	for _, tc := range tests {
		t.Run(string(tc.op), func(t *testing.T) {
			h := newHarness(t)
			h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
			h.cf.FailNext(tc.op, &cloudflare.APIError{Op: "x", Status: 503, Messages: []string{"unavailable"}})
			st := h.cycle()
			if st.State != SyncError || !strings.Contains(st.Error, tc.msg) {
				t.Fatalf("status %+v", st)
			}
			row := h.row(model.KindDNS, appKey)
			if row.Status != model.StatusError || !strings.Contains(row.LastError, tc.msg) {
				t.Fatalf("row %+v", row)
			}
			if st := h.cycle(); st.State != SyncSuccess {
				t.Fatalf("retry status %+v", st)
			}
			wantStatus(t, h.row(model.KindDNS, appKey), model.StatusActive)
			if len(h.records()) != 1 {
				t.Fatal("not created on retry")
			}
		})
	}
}

func TestDNSUpdateFailureKeepsRecord(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	h.ip = netip.MustParseAddr("203.0.113.77")
	h.cf.FailNext(cffake.OpUpdateDNSRecord, errBoom)
	h.cycle()
	if row := h.row(model.KindDNS, appKey); row.Status != model.StatusError || row.CFID == "" {
		t.Fatalf("row %+v", row)
	}
	if h.cf.CountCalls(cffake.OpCreateDNSRecord) != 1 {
		t.Fatal("update failure must not create a second record")
	}
}

func TestDNSPublicIPFailure(t *testing.T) {
	h := newHarness(t)
	h.ip = netip.MustParseAddr("2001:db8::1") // wrong family for an A record
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	if row := h.row(model.KindDNS, appKey); !strings.Contains(row.LastError, "public IPv4") {
		t.Fatalf("row %+v", row)
	}
	if len(h.records()) != 0 {
		t.Fatal("record created without an address")
	}
}

func TestDNSContainerTargetWithoutAddress(t *testing.T) {
	h := newHarness(t)
	s := dnsSpec("web", appHost)
	s.Target = model.TargetContainer
	c := ctr("web", dnsOnly(s))
	c.Networks = nil
	h.src.set(c)
	h.cycle()
	if row := h.row(model.KindDNS, appKey); !strings.Contains(row.LastError, "no IPv4 address") {
		t.Fatalf("row %+v", row)
	}
}

func TestLocalSourceErrorHoldsResources(t *testing.T) {
	h := newHarness(t)
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	before := h.row(model.KindDNS, appKey)
	h.src.setErr(errBoom)
	h.cf.ResetCalls()
	for range 3 {
		h.clk.advance(time.Hour)
		if st := h.cycle(); st.State != SyncError || !strings.Contains(st.Error, "local source") {
			t.Fatalf("status %+v", st)
		}
	}
	if h.mutations() != 0 {
		t.Fatalf("held source mutated: %+v", h.cf.Calls())
	}
	if after := h.row(model.KindDNS, appKey); !sameRow(before, after) {
		t.Fatalf("held row changed: %+v", after)
	}
}

func TestLegacyDNSRowIsStamped(t *testing.T) {
	h := newHarness(t)
	rec := h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: appHost, Type: model.DNSTypeA, Content: "203.0.113.10", Proxied: true, TTL: 1, Comment: "v0.2"})
	h.st.seed(model.Resource{
		Kind: model.KindDNS, Key: appKey, Hostname: appHost, Owner: model.Owner{ContainerName: "web", Service: "web"},
		ZoneID: testZone, AccountID: testAccount, Credential: "default", CFID: rec.ID, RecordType: model.DNSTypeA,
		Content: "203.0.113.10", Proxied: true, TTL: 1, Status: model.StatusActive, Cleanup: true, Legacy: true,
	})
	h.src.set(ctr("web", dnsOnly(dnsSpec("web", appHost))))
	h.cycle()
	if got := h.recordsNamed(appHost); len(got) != 1 || got[0].Comment != ownMarker || got[0].ID != rec.ID {
		t.Fatalf("records %+v", got)
	}
	if row := h.row(model.KindDNS, appKey); row.Legacy || row.Status != model.StatusActive {
		t.Fatalf("row %+v", row)
	}
	h.assertIdempotent()
}

func TestLegacyRowIsRekeyed(t *testing.T) {
	h := newHarness(t)
	rec := h.cf.PutRecord(testZone, cloudflare.DNSRecord{Name: "example.com", Type: model.DNSTypeMX, Content: "mail.example.com", Priority: 10, TTL: 1})
	old := h.st.seed(model.Resource{
		Kind: model.KindDNS, Key: "example.com|MX|mail.example.com", Hostname: "example.com",
		Owner: model.Owner{ContainerName: "web", Service: "mx"}, ZoneID: testZone, AccountID: testAccount,
		Credential: "default", CFID: rec.ID, RecordType: model.DNSTypeMX, Content: "mail.example.com",
		Priority: 10, TTL: 1, Status: model.StatusActive, Cleanup: true, Legacy: true,
	})
	mx := model.DNSSpec{Service: "mx", Hostname: "example.com", Type: model.DNSTypeMX, Target: "mail.example.com", Priority: 10, Cleanup: true}
	h.src.set(ctr("web", dnsOnly(mx)))
	h.cycle()
	row := h.row(model.KindDNS, model.DNSKey("example.com", model.DNSTypeMX, "10 mail.example.com"))
	if row.ID != old.ID || row.Legacy || row.CFID != rec.ID {
		t.Fatalf("row %+v", row)
	}
	if n := len(h.st.list(false)); n != 1 || len(h.records()) != 1 {
		t.Fatalf("rows=%d records=%+v", n, h.records())
	}
}
