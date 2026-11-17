package reconcile

import "github.com/channinghe/labelgate/internal/config"

// PoliciesFromConfig builds AgentPolicies from the main instance configuration.
//
// Required behavior (WP4):
//   - ID in cfg.Agent.Agents: Allowed; Hostnames = entry.Hostnames;
//     DefaultTunnel = entry.DefaultTunnel; Tunnels = entry.Tunnels plus
//     entry.DefaultTunnel; if both empty, Tunnels = [""] (global default only).
//   - Otherwise, if cfg.Agent.AcceptToken != "": Allowed; Hostnames =
//     AcceptHostnames; Tunnels = AcceptTunnels or [""].
//   - Otherwise: Allowed=false.
//   - Agents disabled (cfg.Agent.Enabled=false): every agent Allowed=false.
//   - The returned func is pure (closes over copies, no shared mutable state).
func PoliciesFromConfig(cfg config.Config) AgentPolicies {
	return func(string) AgentPolicy { return AgentPolicy{} }
}
