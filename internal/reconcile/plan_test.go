package reconcile

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// randomScenario builds claims and tracked rows over a small hostname space
// so that conflicts of every kind are frequent.
func randomScenario(rng *rand.Rand) ([]claim, []model.Resource) {
	hosts := []string{"a.example.com", "b.example.com", "c.example.com"}
	src := &sourceInfo{AgentID: "", Defs: map[string][]model.AccessPolicyDef{}}
	var claims []claim
	for range 3 + rng.IntN(8) {
		name := fmt.Sprintf("c%d", rng.IntN(4))
		c := &model.Container{Name: name, ID: "id-" + name}
		host := hosts[rng.IntN(len(hosts))]
		svc := fmt.Sprintf("s%d", rng.IntN(3))
		switch rng.IntN(3) {
		case 0:
			claims = append(claims, dnsClaim(src, c, dnsSpec(svc, host)))
		case 1:
			s := dnsSpec(svc, host)
			s.Type, s.Target = model.DNSTypeCNAME, "x.example.net"
			claims = append(claims, dnsClaim(src, c, s))
		default:
			tid := []string{tunnelA, tunnelB}[rng.IntN(2)]
			res := model.Resource{Kind: model.KindTunnel, Key: model.TunnelKey(tid, host, ""), Hostname: host,
				Owner: ownerOf(src, c, svc), TunnelID: tid, Service: "http://x"}
			claims = append(claims, claim{Res: res, Class: classTunnelPrefix + tid, Src: src, Ctr: c, Tunnel: cloudflare.Tunnel{ID: tid}})
		}
	}
	claims = uniqueClaims(claims) // a source never yields two claims per (key, owner)
	var tracked []model.Resource
	for i, c := range claims {
		if rng.IntN(3) == 0 {
			r := c.Res
			r.ID, r.Status = int64(i+1), model.StatusActive
			tracked = append(tracked, r)
		}
	}
	return claims, dedupeTracked(tracked)
}

func uniqueClaims(cs []claim) []claim {
	seen := map[string]bool{}
	var out []claim
	for _, c := range cs {
		id := c.key().String() + "@" + c.Res.Owner.Key()
		if !seen[id] {
			seen[id] = true
			out = append(out, c)
		}
	}
	return out
}

func dedupeTracked(rows []model.Resource) []model.Resource {
	seen := map[rowKey]bool{}
	var out []model.Resource
	for _, r := range rows {
		if !seen[keyOf(r)] {
			seen[keyOf(r)] = true
			out = append(out, r)
		}
	}
	return out
}

func shuffled[T any](rng *rand.Rand, in []T) []T {
	out := append([]T(nil), in...)
	rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
	return out
}

func planFor(claims []claim, tracked []model.Resource) (resolution, plan) {
	held := holdSet{agents: map[string]bool{}, rows: map[int64]string{}}
	res := resolve(claims, tracked, held)
	live := liveState{Zones: map[string]zoneLive{"": {}}, Tunnels: map[string]tunnelLive{}, Accounts: map[string]accountLive{}}
	for _, tid := range []string{tunnelA, tunnelB} {
		live.Tunnels[tid] = tunnelLive{Tunnel: cloudflare.Tunnel{ID: tid}}
	}
	p := buildPlan(planInput{Now: time.Unix(1e9, 0), InstanceID: testInstance, RemoveDelay: time.Minute,
		Winners: res.Winners, Tracked: res.Tracked, Held: held, Live: live})
	return res, p
}

// Property: resolution and plan are independent of input order, and every
// (kind, key) has at most one winner.
func TestPlanIsDeterministic(t *testing.T) {
	for seed := range uint64(300) {
		rng := rand.New(rand.NewPCG(seed, seed*7+1))
		claims, tracked := randomScenario(rng)
		res0, plan0 := planFor(claims, tracked)
		for range 5 {
			res, p := planFor(shuffled(rng, claims), shuffled(rng, tracked))
			if !reflect.DeepEqual(winnerKeys(res0), winnerKeys(res)) ||
				!reflect.DeepEqual(dedupeRejected(res0.Rejected), dedupeRejected(res.Rejected)) ||
				!reflect.DeepEqual(plan0, p) {
				t.Fatalf("seed %d: order-dependent result", seed)
			}
		}
		seen := map[rowKey]bool{}
		for _, w := range res0.Winners {
			if seen[w.key()] {
				t.Fatalf("seed %d: two winners for %v", seed, w.key())
			}
			seen[w.key()] = true
		}
		assertOneClassPerHost(t, seed, res0.Winners)
	}
}

func winnerKeys(r resolution) []string {
	out := make([]string, 0, len(r.Winners))
	for _, w := range r.Winners {
		out = append(out, w.key().String()+"@"+w.Res.Owner.Key())
	}
	return out
}

func assertOneClassPerHost(t *testing.T, seed uint64, winners []claim) {
	t.Helper()
	classes := map[string]string{}
	for _, w := range winners {
		if w.Class == "" {
			continue
		}
		if c, ok := classes[w.Res.Hostname]; ok && c != w.Class {
			t.Fatalf("seed %d: host %s has classes %s and %s", seed, w.Res.Hostname, c, w.Class)
		}
		classes[w.Res.Hostname] = w.Class
	}
}

// Property: resolving against the previous cycle's outcome never changes the
// winners (existing owner wins; no flapping).
func TestResolveIsStableAcrossCycles(t *testing.T) {
	for seed := range uint64(200) {
		rng := rand.New(rand.NewPCG(seed, 3))
		claims, tracked := randomScenario(rng)
		res1, _ := planFor(claims, tracked)
		var next []model.Resource
		for i, w := range res1.Winners {
			r := w.Res
			r.ID = int64(i + 1)
			next = append(next, r)
		}
		res2, _ := planFor(shuffled(rng, claims), next)
		if !reflect.DeepEqual(winnerKeys(res1), winnerKeys(res2)) {
			t.Fatalf("seed %d: winners flapped\n%v\n%v", seed, winnerKeys(res1), winnerKeys(res2))
		}
	}
}

func TestDesiredRowTransitions(t *testing.T) {
	old := model.Resource{ID: 7, Kind: model.KindDNS, Key: "k", Hostname: "h", Content: "1.1.1.1", Status: model.StatusActive,
		CFID: "rec", Owner: model.Owner{ContainerName: "a", Service: "s"}, Cleanup: true}
	tests := []struct {
		name   string
		mut    func(*claim)
		orphan bool
		want   model.Status
	}{
		{"unchanged keeps status", func(*claim) {}, false, model.StatusActive},
		{"spec change is pending", func(c *claim) { c.Res.Content = "2.2.2.2" }, false, model.StatusPending},
		{"owner takeover is pending", func(c *claim) { c.Res.Owner.ContainerName = "b" }, false, model.StatusPending},
		{"revival is pending", func(*claim) {}, true, model.StatusPending},
		{"resolution error keeps old content", func(c *claim) { c.Res.Content, c.Err = "", "boom" }, false, model.StatusActive},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := old
			if tc.orphan {
				o.OrphanedAt, o.Status = time.Unix(5, 0), model.StatusPendingCleanup
			}
			c := claim{Res: old}
			c.Res.ID, c.Res.Status, c.Res.CFID = 0, "", ""
			tc.mut(&c)
			n := desiredRow(o, true, c)
			if n.Status != tc.want || n.ID != 7 || n.CFID != "rec" || !n.OrphanedAt.IsZero() || n.Content == "" {
				t.Fatalf("row %+v", n)
			}
		})
	}
}
