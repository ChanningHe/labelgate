package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/channinghe/labelgate/internal/model"
)

func TestListDNSRecordsAutoPagingAndDecoding(t *testing.T) {
	// Arrange: 5 records over 3 pages, incl. SRV/CAA data objects and foreign types.
	f := newFakeCF(t)
	f.maxPerPage = 2
	f.seed("dns:z1", map[string]any{"id": "a", "name": "A.Example.com", "type": "A", "content": "1.2.3.4", "proxied": true, "ttl": 1, "comment": "labelgate:abcd1234 note"})
	f.seed("dns:z1", map[string]any{"id": "mx", "name": "example.com", "type": "MX", "content": "Mail.Example.com.", "priority": 10, "ttl": 300, "comment": nil})
	f.seed("dns:z1", map[string]any{"id": "srv", "name": "_sip._tcp.example.com", "type": "SRV", "content": "5 5060 sip.example.com", "priority": 1, "ttl": 300,
		"data": map[string]any{"priority": 1, "weight": 5, "port": 5060, "target": "sip.example.com"}})
	f.seed("dns:z1", map[string]any{"id": "caa", "name": "example.com", "type": "CAA", "content": `128 issuewild "letsencrypt.org"`, "ttl": 3600,
		"data": map[string]any{"flags": 128, "tag": "issuewild", "value": "letsencrypt.org"}})
	f.seed("dns:z1", map[string]any{"id": "ns", "name": "sub.example.com", "type": "NS", "content": "ns1.other.net", "ttl": 3600})
	c := f.client(t, rootCfg())

	// Act
	recs, err := c.ListDNSRecords(context.Background(), testZone)

	// Assert
	if err != nil {
		t.Fatal(err)
	}
	if n := f.count("GET", "/zones/z1/dns_records"); n != 3 {
		t.Fatalf("requests = %d, want 3", n)
	}
	want := []DNSRecord{
		{ID: "a", Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4", Proxied: true, TTL: 1, Comment: "labelgate:abcd1234 note"},
		{ID: "mx", Name: "example.com", Type: model.DNSTypeMX, Content: "mail.example.com", TTL: 300, Priority: 10},
		{ID: "srv", Name: "_sip._tcp.example.com", Type: model.DNSTypeSRV, Content: "sip.example.com", TTL: 300, Priority: 1, Weight: 5, Port: 5060},
		{ID: "caa", Name: "example.com", Type: model.DNSTypeCAA, Content: "letsencrypt.org", TTL: 3600, Flags: 128, Tag: "issuewild"},
		{ID: "ns", Name: "sub.example.com", Type: "NS", Content: "ns1.other.net", TTL: 3600},
	}
	if !reflect.DeepEqual(recs, want) {
		t.Fatalf("records:\n got %+v\nwant %+v", recs, want)
	}
}

func TestCreateDNSRecordBodies(t *testing.T) {
	tests := []struct {
		name string
		rec  DNSRecord
		body map[string]any
	}{
		{"A proxied forces ttl 1", DNSRecord{Name: "A.example.com", Type: model.DNSTypeA, Content: "1.2.3.4", Proxied: true, TTL: 300, Comment: "labelgate:abcd1234 hi"},
			map[string]any{"name": "a.example.com", "type": "A", "content": "1.2.3.4", "proxied": true, "ttl": 1.0, "comment": "labelgate:abcd1234 hi"}},
		{"CNAME unproxied auto ttl", DNSRecord{Name: "c.example.com", Type: model.DNSTypeCNAME, Content: "T.cfargotunnel.com.", Comment: "labelgate:abcd1234"},
			map[string]any{"name": "c.example.com", "type": "CNAME", "content": "t.cfargotunnel.com", "proxied": false, "ttl": 1.0, "comment": "labelgate:abcd1234"}},
		{"TXT never proxied", DNSRecord{Name: "t.example.com", Type: model.DNSTypeTXT, Content: "v=spf1 -all", Proxied: true, TTL: 120},
			map[string]any{"name": "t.example.com", "type": "TXT", "content": "v=spf1 -all", "ttl": 120.0, "comment": ""}},
		{"MX priority", DNSRecord{Name: "example.com", Type: model.DNSTypeMX, Content: "mail.example.com", Priority: 10, TTL: 300},
			map[string]any{"name": "example.com", "type": "MX", "content": "mail.example.com", "priority": 10.0, "ttl": 300.0, "comment": ""}},
		{"MX priority zero is sent", DNSRecord{Name: "example.com", Type: model.DNSTypeMX, Content: "mail.example.com"},
			map[string]any{"name": "example.com", "type": "MX", "content": "mail.example.com", "priority": 0.0, "ttl": 1.0, "comment": ""}},
		{"SRV data", DNSRecord{Name: "_sip._tcp.example.com", Type: model.DNSTypeSRV, Content: "sip.example.com", Priority: 1, Weight: 5, Port: 5060},
			map[string]any{"name": "_sip._tcp.example.com", "type": "SRV", "ttl": 1.0, "comment": "",
				"data": map[string]any{"priority": 1.0, "weight": 5.0, "port": 5060.0, "target": "sip.example.com"}}},
		{"CAA flags/tag/value", DNSRecord{Name: "example.com", Type: model.DNSTypeCAA, Content: "letsencrypt.org", Flags: 128, Tag: "iodef"},
			map[string]any{"name": "example.com", "type": "CAA", "ttl": 1.0, "comment": "",
				"data": map[string]any{"flags": 128.0, "tag": "iodef", "value": "letsencrypt.org"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Arrange
			f := newFakeCF(t)
			c := f.client(t, rootCfg())
			// Act
			got, err := c.CreateDNSRecord(context.Background(), testZone, tt.rec)
			// Assert
			if err != nil {
				t.Fatal(err)
			}
			if body := f.lastBody(t, "POST", "/zones/z1/dns_records"); !reflect.DeepEqual(body, tt.body) {
				t.Fatalf("body:\n got %v\nwant %v", body, tt.body)
			}
			want := NormalizeDNSRecord(tt.rec)
			want.ID = got.ID
			if got.ID == "" || !reflect.DeepEqual(got, want) {
				t.Fatalf("returned %+v, want %+v", got, want)
			}
		})
	}
}

func TestCreateDNSRecordAlreadyExists(t *testing.T) {
	f := newFakeCF(t)
	f.seed("dns:z1", map[string]any{"name": "a.example.com", "type": "CNAME", "content": "x.example.com"})
	c := f.client(t, rootCfg())
	_, err := c.CreateDNSRecord(context.Background(), testZone, DNSRecord{Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4"})
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
	if n := f.count("POST", "/zones/z1/dns_records"); n != 1 {
		t.Fatalf("conflicts must not be retried: %d", n)
	}
}

func TestUpdateAndDeleteDNSRecord(t *testing.T) {
	// Arrange: a foreign record carrying a user comment becomes owned by stamping the marker.
	f := newFakeCF(t)
	f.seed("dns:z1", map[string]any{"id": "r1", "name": "a.example.com", "type": "A", "content": "1.1.1.1", "ttl": 300, "comment": "user note"})
	c := f.client(t, rootCfg())
	ctx := context.Background()
	comment := model.DNSComment("abcd1234", "user note")

	// Act
	got, err := c.UpdateDNSRecord(ctx, testZone, DNSRecord{ID: "r1", Name: "a.example.com", Type: model.DNSTypeA, Content: "2.2.2.2", TTL: 300, Comment: comment})

	// Assert
	if err != nil || got.Content != "2.2.2.2" || got.Comment != comment || !model.IsOwnedComment("abcd1234", got.Comment) {
		t.Fatalf("update: %+v %v", got, err)
	}
	if body := f.lastBody(t, "PUT", "/zones/z1/dns_records/r1"); body["comment"] != comment {
		t.Fatalf("comment not sent: %v", body)
	}
	recs, _ := c.ListDNSRecords(ctx, testZone)
	if len(recs) != 1 || recs[0].Comment != comment {
		t.Fatalf("live %+v", recs)
	}
	if err := c.DeleteDNSRecord(ctx, testZone, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteDNSRecord(ctx, testZone, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := c.UpdateDNSRecord(ctx, testZone, DNSRecord{ID: "r1", Name: "a.example.com", Type: model.DNSTypeA, Content: "1.1.1.1"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing: %v", err)
	}
}

func TestDNSInputValidation(t *testing.T) {
	f := newFakeCF(t)
	c := f.client(t, rootCfg())
	ctx := context.Background()
	calls := map[string]error{}
	_, calls["update without id"] = c.UpdateDNSRecord(ctx, testZone, DNSRecord{Name: "a.example.com"})
	calls["delete without id"] = c.DeleteDNSRecord(ctx, testZone, "")
	_, calls["zone without id"] = c.ListDNSRecords(ctx, Zone{Name: "example.com", Credential: "default"})
	for name, err := range calls {
		if !errors.Is(err, model.ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
	if _, err := c.ListDNSRecords(ctx, Zone{ID: "z1", Credential: "ghost"}); !errors.Is(err, ErrUnknownCredential) {
		t.Errorf("unknown credential: %v", err)
	}
	if len(f.reqs()) != 0 {
		t.Fatalf("invalid input must not reach Cloudflare: %v", f.reqs())
	}
}

func TestNormalizeDNSRecord(t *testing.T) {
	tests := []struct{ in, want DNSRecord }{
		{DNSRecord{Name: " A.B.com. ", Type: model.DNSTypeA, Content: "1.2.3.4", TTL: 0, Priority: 5, Flags: 3, Tag: "x"},
			DNSRecord{Name: "a.b.com", Type: model.DNSTypeA, Content: "1.2.3.4", TTL: 1}},
		{DNSRecord{Name: "b.com", Type: model.DNSTypeAAAA, Content: "::1", Proxied: true, TTL: 300},
			DNSRecord{Name: "b.com", Type: model.DNSTypeAAAA, Content: "::1", Proxied: true, TTL: 1}},
		{DNSRecord{Name: "b.com", Type: model.DNSTypeMX, Content: "M.b.com.", Proxied: true, TTL: 60, Priority: 5, Weight: 9},
			DNSRecord{Name: "b.com", Type: model.DNSTypeMX, Content: "m.b.com", TTL: 60, Priority: 5}},
		{DNSRecord{Name: "b.com", Type: model.DNSTypeCAA, Content: "CA.org", Flags: 128, Tag: "issue", Priority: 1},
			DNSRecord{Name: "b.com", Type: model.DNSTypeCAA, Content: "CA.org", TTL: 1, Flags: 128, Tag: "issue"}},
		{DNSRecord{Name: "b.com", Type: model.DNSTypeTXT, Content: "Hello", Proxied: true, TTL: 120},
			DNSRecord{Name: "b.com", Type: model.DNSTypeTXT, Content: "Hello", TTL: 120}},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			if got := NormalizeDNSRecord(tt.in); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %+v want %+v", got, tt.want)
			}
			if got := NormalizeDNSRecord(NormalizeDNSRecord(tt.in)); !reflect.DeepEqual(got, tt.want) {
				t.Fatal("not idempotent")
			}
		})
	}
}
