package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/channinghe/labelgate/internal/agent"
	"github.com/channinghe/labelgate/internal/api"
	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/reconcile"
)

const (
	e2eAgentID = "edge1"
	edgeHost   = "edge.example.com"
)

func agentConfig(mode config.ConnectMode, id, token, addr string) config.Config {
	cfg := config.Defaults()
	cfg.Mode = config.ModeAgent
	cfg.LogLevel = "debug"
	cfg.Retry = fastRetry()
	cfg.Connect = config.ConnectConfig{Mode: mode, Token: token, AgentID: id, HeartbeatInterval: time.Minute}
	if mode == config.ConnectInbound {
		cfg.Connect.Listen = addr
	} else {
		cfg.Connect.Endpoint = "ws://" + addr + "/ws"
	}
	return cfg
}

// startAgent runs an agent process with src as its Docker source.
func startAgent(t *testing.T, cfg config.Config, src *scriptSource) *proc {
	t.Helper()
	p := &proc{t: t, cfg: cfg, src: src}
	p.run(realBuilders(), time.Now)
	return p
}

// remoteSource is an agent host running one tunnelled container.
func remoteSource() *scriptSource {
	return newScriptSource(time.Now, model.Container{ID: "c-remote", Name: "remote", State: "running", Labels: map[string]string{
		"labelgate.tunnel.edge.hostname": edgeHost,
		"labelgate.tunnel.edge.service":  "http://remote:9000",
	}})
}

// agentMainConfig enables the agent listener with one named agent.
func (e *e2eEnv) agentMainConfig(token, connectTo string) config.Config {
	cfg := e.mainConfig()
	cfg.Agent.Enabled = true
	cfg.Agent.Listen = freeAddr(e.t)
	cfg.Agent.Agents = map[string]config.AgentEntry{e2eAgentID: {
		Token: token, Name: "Edge One", ConnectTo: connectTo, Services: []string{"remote"},
	}}
	return cfg
}

func agentsOf(t *testing.T, p *proc) map[string]api.AgentDTO {
	t.Helper()
	var list api.AgentListDTO
	p.api("/agents", &list)
	out := map[string]api.AgentDTO{}
	for _, a := range list.Agents {
		out[a.ID] = a
	}
	return out
}

// waitEdgeActive waits until the agent's tunnel rule and CNAME are applied.
func waitEdgeActive(t *testing.T, m *proc) {
	t.Helper()
	eventually(t, "agent resources active", func() bool {
		tun, ok := resources(t, m, "tunnels")[edgeHost+"||"]
		cname, ok2 := resources(t, m, "dns")[edgeHost+"|CNAME|"]
		return ok && ok2 && tun.Status == "active" && cname.Status == "active" && tun.AgentID == e2eAgentID
	})
}

func assertEdgeApplied(t *testing.T, env *e2eEnv) {
	t.Helper()
	cname := onlyOne(t, "agent CNAME", env.cf.find("dns:"+e2eZoneID, "name", edgeHost))
	if cname["content"] != tunnelCNAM || cname["comment"] != ownedMark {
		t.Errorf("agent CNAME = %v", cname)
	}
	assertUnmanagedTunnelParts(t, env.cf, edgeHost)
}

// Scenario 4: outbound agent; wrong token rejected; disconnect keeps resources.
func TestE2EOutboundAgent(t *testing.T) {
	env := newEnv(t)
	cfg := env.agentMainConfig(e2eAgentToken, "")
	m := env.startMain(cfg, newScriptSource(env.clock.Now))
	m.firstCycle()
	m.waitAPI()

	bad := startAgent(t, agentConfig(config.ConnectOutbound, e2eAgentID, "x"+e2eAgentToken, cfg.Agent.Listen), remoteSource())
	bad.waitLog("handshake with the main instance failed")
	bad.waitLog("tokens do not match: connect.token (env LABELGATE_CONNECT_TOKEN) on this agent must equal agent.agents." + e2eAgentID + ".token")
	m.waitLog("agent handshake failed")
	m.waitLog("connect.token on the agent must equal agent.agents." + e2eAgentID + ".token on this main instance")
	if err := bad.stop(); err != nil {
		t.Fatalf("bad agent: %v", err)
	}
	if got := agentsOf(t, m); len(got) != 0 {
		t.Fatalf("rejected agent is known to main: %v", got)
	}
	if mut := env.cf.mutations(); len(mut) != 0 {
		t.Fatalf("rejected agent caused mutations: %v", mut)
	}

	good := startAgent(t, agentConfig(config.ConnectOutbound, e2eAgentID, e2eAgentToken, cfg.Agent.Listen), remoteSource())
	waitEdgeActive(t, m)
	assertEdgeApplied(t, env)
	if a := agentsOf(t, m)[e2eAgentID]; !a.Connected || a.Status != "active" || a.ResourceCount != 2 || a.Name != "Edge One" {
		t.Fatalf("agent = %+v", a)
	}

	if err := good.stop(); err != nil {
		t.Fatalf("agent: %v", err)
	}
	eventually(t, "agent disconnected", func() bool {
		a := agentsOf(t, m)[e2eAgentID]
		return !a.Connected && a.Status == "disconnected"
	})
	before := len(env.cf.mutations())
	if st := m.cycle(); st.State != reconcile.SyncSuccess {
		t.Fatalf("cycle after disconnect: %+v", st)
	}
	if mut := env.cf.mutations(); len(mut) != before {
		t.Fatalf("disconnect caused mutations: %v", mut[before:])
	}
	wantResource(t, resources(t, m, "tunnels"), edgeHost+"||", "active")
	wantResource(t, resources(t, m, "dns"), edgeHost+"|CNAME|", "active")
	assertEdgeApplied(t, env)
}

// Scenario 5: inbound mode; a main with the wrong token learns nothing.
func TestE2EInboundAgentRejectsImpostorMain(t *testing.T) {
	agentAddr := freeAddr(t)
	ag := startAgent(t, agentConfig(config.ConnectInbound, e2eAgentID, e2eAgentToken, agentAddr), remoteSource())
	ag.waitLog("agent inbound listener started")
	wsURL := "ws://" + agentAddr + "/ws"

	env := newEnv(t)
	m := env.startMain(env.agentMainConfig(e2eAgentToken, wsURL), newScriptSource(env.clock.Now))
	m.waitAPI()
	waitEdgeActive(t, m)
	assertEdgeApplied(t, env)

	impEnv := newEnv(t)
	imp := impEnv.startMain(impEnv.agentMainConfig("x"+e2eAgentToken, wsURL), newScriptSource(impEnv.clock.Now))
	imp.waitLog("agent handshake failed")
	ag.waitLog("handshake with the main instance failed")
	imp.waitAPI()
	if got := agentsOf(t, imp); len(got) != 0 {
		t.Fatalf("impostor learned agents: %v", got)
	}
	if got := resources(t, imp, "tunnels"); len(got) != 0 {
		t.Fatalf("impostor learned containers: %v", got)
	}
	if mut := impEnv.cf.mutations(); len(mut) != 0 {
		t.Fatalf("impostor mutated its account: %v", mut)
	}
	if err := imp.stop(); err != nil {
		t.Fatalf("impostor: %v", err)
	}

	assertWireImpostorLearnsNothing(t, wsURL)
	if a := agentsOf(t, m)[e2eAgentID]; !a.Connected {
		t.Fatalf("genuine session lost after impostor attempts: %+v", a)
	}
}

// assertWireImpostorLearnsNothing dials the agent like a main that guesses the
// token: after the agent's hello it must receive nothing but a close.
func assertWireImpostorLearnsNothing(t *testing.T, wsURL string) {
	t.Helper()
	ws, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	defer ws.Close()
	_ = ws.SetReadDeadline(time.Now().Add(e2eWait))
	var env agent.Envelope
	if err := ws.ReadJSON(&env); err != nil || env.Type != agent.MsgHello {
		t.Fatalf("first frame = %+v, %v; want hello", env, err)
	}
	var hello agent.Hello
	if err := json.Unmarshal(env.Payload, &hello); err != nil {
		t.Fatal(err)
	}
	nonce := make([]byte, agent.NonceSize)
	_, _ = rand.Read(nonce)
	mac := agent.MAC("guessed-token-0123456789abcdef0123", agent.RoleMain, agent.ModeInbound, hello.AgentID, hello.Nonce, nonce)
	payload, _ := json.Marshal(agent.Challenge{Nonce: nonce, MAC: mac})
	if err := ws.WriteJSON(agent.Envelope{Type: agent.MsgChallenge, Payload: payload}); err != nil {
		t.Fatalf("send challenge: %v", err)
	}
	for {
		var next agent.Envelope
		err := ws.ReadJSON(&next)
		var ne net.Error
		switch {
		case errors.As(err, &ne) && ne.Timeout():
			t.Fatal("agent kept an unauthenticated connection open")
		case err != nil:
			return // closed without revealing anything
		case next.Type != agent.MsgError:
			t.Fatalf("agent sent %q to an unauthenticated main: %s", next.Type, next.Payload)
		}
	}
}
