package config

import "errors"

// Validate checks cfg and returns the first problem as *ValidationError.
// It never modifies cfg (no silent fallbacks). Rules are listed in
// dev/docs/v0.3-design.md §7 ("Validation rules"); summary:
//   - mode, log_level, log_format, connect.mode: enumerations, else error.
//   - durations > 0 where a zero would spin or panic (poll_interval,
//     sync.interval >= 10s, heartbeat_interval >= 1s, retry.delay); remove_delay,
//     orphan_ttl, agent_grace >= 0; retry.attempts >= 1; retry.backoff >= 1;
//     retry.max_delay >= retry.delay.
//   - docker.endpoint scheme unix:// or tcp:// (ssh:// -> removal message).
//   - credential / tunnel names ^[a-z0-9][a-z0-9_-]*$ and not "default";
//     tokens non-empty; tunnel ids/account ids non-empty; tunnel credential exists.
//   - default_tunnel and every agent tunnel reference resolvable.
//   - api.base_path: starts with "/", no trailing "/", not "/dashboard".
//   - agent IDs model.ValidAgentID, tokens non-empty, connect_to ws:// or wss://,
//     glob patterns valid (path.Match).
//   - agent mode: token non-empty; outbound requires endpoint (ws/wss URL);
//     inbound requires listen.
//   - instance_id empty or model.ValidInstanceID.
//   - TLS cert and key set together.
//   - main mode: at least one credential unless skip_credential_validation.
func Validate(cfg Config) error {
	return errors.New("config.Validate: not implemented")
}
