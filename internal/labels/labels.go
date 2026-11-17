// Package labels parses labelgate container labels into model specs.
//
// The label grammar is backward compatible with v0.2 (see
// docsite/content/docs/en/labels and `git show main:pkg/labels/parser_test.go`):
//
//	<prefix>.dns.<service>.<property>=<value>
//	<prefix>.tunnel.<service>.<property>=<value>
//	<prefix>.tunnel.<service>.origin.<setting>=<value>
//	<prefix>.access.<policy>.<property>=<value>
//	<prefix>.dns.default.<property> / <prefix>.tunnel.default.<property>  (per-container defaults)
package labels

import "github.com/channinghe/labelgate/internal/model"

// DefaultPrefix is the default label prefix.
const DefaultPrefix = "labelgate"

// Parse parses one container's labels. It is pure and deterministic.
//
// Required behavior (WP1), beyond the v0.2 grammar:
//   - Keys without the "<prefix>." prefix, with fewer than 3 dot-separated
//     parts after it, or with an unknown type segment are ignored.
//   - Service names: ^[a-z0-9]([a-z0-9-]*[a-z0-9])?$ and not reserved (v0.2
//     reserved list); "default" is the per-type defaults block for dns/tunnel and
//     is skipped for access. Violations -> LabelError, service skipped.
//   - Hostnames: model.NormalizeHostname then model.ValidHostname, else error.
//   - Defaults: DNS type A, target "auto", proxied true, ttl 0 (auto), cleanup
//     true, credential "" (automatic); tunnel tunnel "" (default tunnel),
//     credential "", cleanup true. NO hardcoded "default" credential/tunnel.
//   - dns.default / tunnel.default supply defaults for type, target, proxied,
//     ttl, credential, cleanup (dns) and tunnel, credential, cleanup (tunnel).
//   - Invalid bool/int values, unknown properties, unknown origin settings,
//     invalid record types, CAA tag not in {issue, issuewild, iodef}, CAA flags
//     outside 0-255, ports outside 0-65535 -> LabelError. Unknown property
//     errors do not drop the service; value errors do.
//   - DNS: proxied forced false for TXT/MX/SRV/CAA. Target for A/AAAA must be
//     auto, container or an IP of the right family; CNAME/MX/SRV targets are
//     normalized hostnames.
//   - Tunnel: service URL required; scheme one of http, https, tcp, ssh, rdp,
//     udp, unix, unix+tls, hello_world, http_status:<code>, bastion.
//     origin.* durations accept Go durations ("30s") or plain seconds ("30").
//   - Access: v0.2 semantics (decision default allow; allow/block need >=1
//     include; valueless selectors everyone/certificate; values comma-split).
//   - Output slices sorted (see model.ParsedLabels).
func Parse(prefix string, labels map[string]string) model.ParsedLabels {
	return model.ParsedLabels{Errors: []model.LabelError{{Key: prefix, Message: "labels.Parse: not implemented"}}}
}
