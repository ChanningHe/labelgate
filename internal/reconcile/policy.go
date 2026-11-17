package reconcile

import "github.com/channinghe/labelgate/internal/config"

// PoliciesFromConfig builds AgentPolicies from the main instance configuration.
//
// Behavior:
//   - ID in cfg.Agent.Agents: Allowed; Hostnames = entry.Hostnames;
//     DefaultTunnel = entry.DefaultTunnel; Tunnels = entry.Tunnels plus
//     entry.DefaultTunnel; if both empty, Tunnels = [""] (global default only).
//   - Otherwise, if cfg.Agent.AcceptToken != "": Allowed; Hostnames =
//     AcceptHostnames; Tunnels = AcceptTunnels or [""].
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
			Tunnels:   cloneStrings(cfg.Agent.AcceptTunnels),
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
		Tunnels:       tunnels,
		DefaultTunnel: e.DefaultTunnel,
	}
}

func copyPolicy(p AgentPolicy) AgentPolicy {
	p.Hostnames = cloneStrings(p.Hostnames)
	p.Tunnels = cloneStrings(p.Tunnels)
	return p
}

func cloneStrings(s []string) []string {
	if len(s) == 0 {
		return nil
	}
	return append([]string(nil), s...)
}
