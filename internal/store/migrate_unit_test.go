package store

import (
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func TestLegacyRowToResourceRejectsUnrepresentable(t *testing.T) {
	tests := []struct {
		name string
		row  legacyRow
		ok   bool
	}{
		{"valid dns", legacyRow{resourceType: "dns", hostname: "a.example.com", recordType: "a", status: "active"}, true},
		{"unknown kind", legacyRow{resourceType: "page_rule", hostname: "a.example.com"}, false},
		{"empty hostname", legacyRow{resourceType: "dns", hostname: " . ", recordType: "A"}, false},
		{"dns without record type", legacyRow{resourceType: "dns", hostname: "a.example.com"}, false},
		{"tunnel needs no record type", legacyRow{resourceType: "tunnel_ingress", hostname: "a.example.com", tunnelID: "t"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := tt.row.toResource()

			if ok != tt.ok {
				t.Errorf("ok = %v, want %v", ok, tt.ok)
			}
		})
	}
}

func TestLegacyRowToResourceNormalizes(t *testing.T) {
	row := legacyRow{resourceType: "dns", hostname: " Mixed.Example.COM. ", recordType: " cname ", content: "T.example.com"}

	r, ok := row.toResource()

	if !ok || r.Hostname != "mixed.example.com" || r.RecordType != model.DNSTypeCNAME || r.Key != "mixed.example.com|CNAME" {
		t.Errorf("resource = %+v (ok=%v)", r, ok)
	}
}

func TestLegacyAccessRowFallsBackToCFID(t *testing.T) {
	row := legacyRow{resourceType: "access_app", hostname: "s.example.com", cfID: "cf-only"}

	r, ok := row.toResource()

	if !ok || r.CFID != "cf-only" {
		t.Errorf("CFID = %q (ok=%v), want fallback to cf_id", r.CFID, ok)
	}
}

func TestDedupeLatestTieBreaks(t *testing.T) {
	at := func(sec int64) time.Time { return time.Unix(sec, 0).UTC() }
	mk := func(id string, created, updated int64) keyedResource {
		return keyedResource{legacyID: id, res: model.Resource{
			Kind: model.KindDNS, Key: "k", CreatedAt: at(created), UpdatedAt: at(updated),
		}}
	}
	tests := []struct {
		name string
		in   []keyedResource
		want string
	}{
		{"latest updated wins", []keyedResource{mk("a", 1, 5), mk("b", 1, 9), mk("c", 1, 7)}, "b"},
		{"equal updated: latest created wins", []keyedResource{mk("a", 1, 5), mk("b", 3, 5), mk("c", 2, 5)}, "b"},
		{"all equal: greater legacy id wins", []keyedResource{mk("a", 1, 5), mk("c", 1, 5), mk("b", 1, 5)}, "c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kept, dropped := dedupeLatest(tt.in)

			if len(kept) != 1 || kept[0].legacyID != tt.want || dropped != len(tt.in)-1 {
				t.Errorf("kept %+v dropped %d, want id %q", kept, dropped, tt.want)
			}
		})
	}
}

func TestTunnelSourceLess(t *testing.T) {
	mk := func(agent, container, svc, path string) model.Resource {
		return model.Resource{Owner: model.Owner{AgentID: agent, ContainerName: container, Service: svc}, Path: path}
	}
	tests := []struct {
		name string
		a, b model.Resource
		want bool
	}{
		{"smaller service first", mk("", "z", "a", ""), mk("", "a", "b", ""), true},
		{"larger service later", mk("", "a", "b", ""), mk("", "z", "a", ""), false},
		{"same service: owner order (local first)", mk("", "z", "s", ""), mk("edge", "a", "s", ""), true},
		{"same service: owner order reversed", mk("edge", "a", "s", ""), mk("", "z", "s", ""), false},
		{"same owner: path decides", mk("", "c", "s", "/a"), mk("", "c", "s", "/b"), true},
		{"identical", mk("", "c", "s", "/a"), mk("", "c", "s", "/a"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tunnelSourceLess(tt.a, tt.b); got != tt.want {
				t.Errorf("tunnelSourceLess = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMapLegacyStatus(t *testing.T) {
	tests := []struct {
		status  string
		cleanup bool
		want    model.Status
	}{
		{"active", true, model.StatusActive},
		{"active", false, model.StatusActive},
		{"error", true, model.StatusError},
		{"orphaned", true, model.StatusPendingCleanup},
		{"orphaned", false, model.StatusOrphaned},
		{"pending_cleanup", false, model.StatusPendingCleanup},
		{"pending_cleanup", true, model.StatusPendingCleanup},
		{"", true, model.StatusPending},
		{"something", true, model.StatusPending},
	}
	for _, tt := range tests {
		if got := mapLegacyStatus(tt.status, tt.cleanup); got != tt.want {
			t.Errorf("mapLegacyStatus(%q, %v) = %v, want %v", tt.status, tt.cleanup, got, tt.want)
		}
	}
}
