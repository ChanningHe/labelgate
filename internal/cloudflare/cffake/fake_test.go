package cffake

import (
	"context"
	"errors"
	"testing"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

func TestResolveZoneLongestSuffix(t *testing.T) {
	f := New()
	f.AddZone(cloudflare.Zone{ID: "z1", Name: "example.com", Credential: "default"})
	f.AddZone(cloudflare.Zone{ID: "z2", Name: "sub.example.com", Credential: "team"})
	ctx := context.Background()
	z, err := f.ResolveZone(ctx, "App.Sub.Example.com", "")
	if err != nil || z.ID != "z2" {
		t.Fatalf("got %+v %v", z, err)
	}
	if _, err := f.ResolveZone(ctx, "badexample.com", ""); !errors.Is(err, cloudflare.ErrNoZone) {
		t.Fatalf("suffix must respect label boundary, got %v", err)
	}
	if _, err := f.ResolveZone(ctx, "a.example.com", "nope"); !errors.Is(err, cloudflare.ErrUnknownCredential) {
		t.Fatalf("got %v", err)
	}
}

func TestCreateDNSRecordConflicts(t *testing.T) {
	f := New()
	zone := cloudflare.Zone{ID: "z1", Name: "example.com", Credential: "default"}
	f.AddZone(zone)
	f.PutRecord("z1", cloudflare.DNSRecord{Name: "a.example.com", Type: model.DNSTypeCNAME, Content: "x.example.com"})
	_, err := f.CreateDNSRecord(context.Background(), zone, cloudflare.DNSRecord{Name: "a.example.com", Type: model.DNSTypeA, Content: "1.2.3.4"})
	if !errors.Is(err, cloudflare.ErrAlreadyExists) {
		t.Fatalf("want ErrAlreadyExists, got %v", err)
	}
	f.FailNext(OpListDNSRecords, errors.New("boom"))
	if _, err := f.ListDNSRecords(context.Background(), zone); err == nil {
		t.Fatal("injected failure not returned")
	}
	if _, err := f.ListDNSRecords(context.Background(), zone); err != nil {
		t.Fatalf("failure must be one-shot: %v", err)
	}
}
