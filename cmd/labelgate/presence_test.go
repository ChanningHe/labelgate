package main

import (
	"context"
	"testing"
)

type liveHub map[string]bool

func (liveHub) Run(context.Context) error       { return nil }
func (h liveHub) Connected(agentID string) bool { return h[agentID] }

func TestPresenceLateBindsHub(t *testing.T) {
	var p presence
	if p.connected("edge") {
		t.Fatal("connected before the hub exists")
	}
	p.set(nil)
	if p.connected("edge") {
		t.Fatal("nil hub reported a session")
	}
	p.set(liveHub{"edge": true})
	if !p.connected("edge") || p.connected("other") {
		t.Fatal("presence does not delegate to the hub")
	}
}
