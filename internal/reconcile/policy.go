package reconcile

import (
	"strings"

	"github.com/channinghe/labelgate/internal/config"
)

// PoliciesFromConfig builds AgentPolicies from the main instance configuration.
//
// Behavior:
//   - ID in cfg.Agent.Agents: Allowed; Hostnames = entry.Hostnames;
//     Services = entry.Services; DefaultTunnel = entry.DefaultTunnel; Tunnels = entry.Tunnels plus
//     entry.DefaultTunnel; if both empty, Tunnels = [""] (global default only).
//   - Otherwise, if cfg.Agent.AcceptToken != "": Allowed; Hostnames =
//     AcceptHostnames; Services = AcceptServices; Tunnels = AcceptTunnels or [""].
//   - Otherwise: Allowed=false.
//   - Agents disabled (cfg.Agent.Enabled=false): every agent Allowed=false.
//   - The returned func is pure (closes over copies, no shared mutable state).
func PoliciesFromConfig(cfg config.Config) AgentPolicies {
	if !cfg.Agent.Enabled {
		return func(string) AgentPolicy { return AgentPolicy{} }
	}
	named := make(map[string]AgentPolicy, len(cfg.Agent.Agents))
	for id, e := range cfg.Agent.Agents {
		named[id] = namedPolicy(e)
	}
	var accept *AgentPolicy
	if cfg.Agent.AcceptToken != "" {
		p := AgentPolicy{
			Allowed:   true,
			Hostnames: cloneStrings(cfg.Agent.AcceptHostnames),
			Services:  cloneStrings(cfg.Agent.AcceptServices),
			Tunnels:   cloneStrings(cfg.Agent.AcceptTunnels),
			viaAccept: true,
		}
		if len(p.Tunnels) == 0 {
			p.Tunnels = []string{""}
		}
		accept = &p
	}
	return func(id string) AgentPolicy {
		if p, ok := named[id]; ok {
			return copyPolicy(p)
		}
		if accept != nil {
			return copyPolicy(*accept)
		}
		return AgentPolicy{}
	}
}

func namedPolicy(e config.AgentEntry) AgentPolicy {
	// The agent's default tunnel ("" = global default) is always allowed:
	// labels without `tunnel` resolve to it.
	tunnels := append(cloneStrings(e.Tunnels), e.DefaultTunnel)
	return AgentPolicy{
		Allowed:       true,
		Hostnames:     cloneStrings(e.Hostnames),
		Services:      cloneStrings(e.Services),
		Tunnels:       tunnels,
		DefaultTunnel: e.DefaultTunnel,
	}
}

// key returns the main-instance config key of one of this policy's
// allow-lists ("hostnames", "tunnels" or "services"): agent.agents.<id>.<list>
// for named agents, agent.accept_<list> for accept_token agents. The agent ID
// is printed as-is; the result is for humans, not for key lookups.
func (p AgentPolicy) key(id, list string) string {
	if p.viaAccept {
		return "agent.accept_" + list
	}
	return "agent.agents." + id + "." + list
}

// fixHint says where to change an allow-list: the key, the environment
// variable that sets it for accept_token agents, and that the main instance
// (not the agent) owns it.
func (p AgentPolicy) fixHint(id, list string) string {
	hint := p.key(id, list)
	if p.viaAccept {
		hint += " (env LABELGATE_AGENT_ACCEPT_" + strings.ToUpper(list) + ", comma-separated)"
	}
	return hint + " in the main instance config"
}

func copyPolicy(p AgentPolicy) AgentPolicy {
	p.Hostnames = cloneStrings(p.Hostnames)
	p.Tunnels = cloneStrings(p.Tunnels)
	p.Services = cloneStrings(p.Services)
	return p
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return append([]string(nil), s...)
}
