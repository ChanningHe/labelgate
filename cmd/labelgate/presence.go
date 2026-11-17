package main

import "sync/atomic"

// presence late-binds the agent hub's session set into the reconciler,
// which is constructed before the hub (the hub triggers the reconciler).
type presence struct {
	h atomic.Pointer[hub]
}

func (p *presence) set(h hub) {
	if h != nil {
		p.h.Store(&h)
	}
}

// connected reports whether agentID has a live session (false before set).
func (p *presence) connected(agentID string) bool {
	h := p.h.Load()
	return h != nil && (*h).Connected(agentID)
}
