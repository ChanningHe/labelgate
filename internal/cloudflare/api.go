// Package cloudflare is the narrow Cloudflare layer used by the reconciler.
//
// api.go is a frozen contract: the API interface, its value types and errors.
// The production implementation is Client (client.go and friends); an
// in-memory implementation for tests lives in package cffake.
//
// The layer is deliberately "dumb": it performs CRUD with paging, timeouts,
// retries and error mapping. It never decides ownership; the reconciler does.
package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

// API is everything the reconciler needs from Cloudflare.
//
// Contract for every method:
//   - Bounded by a per-attempt timeout (30s) layered on ctx; retried with
//     backoff per config.RetryConfig (honouring Retry-After); ctx cancellation
//     is returned as ctx.Err() (errors.Is works). Retry semantics depend on
//     idempotency: reads, PUT/PATCH and DELETE are retried on 429, 5xx and
//     transport errors (including timeouts). Create calls (POST) are retried
//     only when the request certainly was not processed: on 429 or on a dial
//     error (e.g. connection refused). A 5xx or timeout on a create is NOT
//     retried, because a lost response could have created the object; the
//     reconciler re-lists on its next cycle instead.
//   - Errors wrap one of the sentinels below where applicable, plus *APIError
//     carrying Cloudflare's codes/messages.
//   - List methods return ALL items (auto-paging).
//   - Hostnames are compared case-insensitively and returned lower-case. This
//     applies only to hostnames: zone names, DNS record names (and hostname
//     targets of CNAME/MX/SRV), tunnel ingress hostnames and Access
//     application domains. Display names (AccessApp.Name, AccessPolicy.Name),
//     DNS TXT/CAA content, tags and comments are passed through verbatim.
//   - Safe for concurrent use (the reconciler is single-threaded, the API
//     server reads Health concurrently).
type API interface {
	// Verify checks every configured credential (token verify) and that every
	// configured tunnel is reachable with its credential. Called once at startup
	// unless skip_credential_validation.
	Verify(ctx context.Context) error
	// Health reports the outcome of the most recent request.
	Health() Health

	// ResolveZone selects the credential and zone for hostname.
	// credential != "": that credential only (ErrUnknownCredential if not
	// configured, ErrNoZone if it has no matching zone).
	// credential == "": longest-suffix zone match over all credentials' zones
	// (filtered by their configured zone patterns); ties prefer the root
	// credential, then the lexicographically smallest name.
	// Zone lists are cached per credential, refreshed hourly and on a miss
	// (at most once per minute per credential).
	ResolveZone(ctx context.Context, hostname, credential string) (Zone, error)
	// ResolveTunnel maps a tunnel reference to a configured tunnel. ref "" means
	// the default tunnel (ErrNoDefaultTunnel if none). ref may be a configured
	// name ("default" = root tunnel) or a configured tunnel ID (case-insensitive).
	// Unknown refs return ErrUnknownTunnel; there is no fallback.
	ResolveTunnel(ref string) (Tunnel, error)

	// ListDNSRecords returns every record of the zone.
	ListDNSRecords(ctx context.Context, zone Zone) ([]DNSRecord, error)
	// CreateDNSRecord creates rec (ID ignored) and returns the stored record.
	// A Cloudflare "record already exists" error maps to ErrAlreadyExists.
	CreateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error)
	// UpdateDNSRecord overwrites record rec.ID with rec (full PUT semantics).
	// Missing record -> ErrNotFound.
	UpdateDNSRecord(ctx context.Context, zone Zone, rec DNSRecord) (DNSRecord, error)
	// DeleteDNSRecord deletes a record. Missing record -> ErrNotFound.
	DeleteDNSRecord(ctx context.Context, zone Zone, recordID string) error

	// GetTunnelConfig returns the live remotely-managed configuration. A tunnel
	// that has never been configured returns an empty TunnelConfig and no error.
	GetTunnelConfig(ctx context.Context, t Tunnel) (TunnelConfig, error)
	// PutTunnelConfig replaces the configuration with cfg exactly as given
	// (MarshalJSON output). Callers must only PUT a config derived from a
	// successful GetTunnelConfig in the same cycle.
	PutTunnelConfig(ctx context.Context, t Tunnel, cfg TunnelConfig) error

	// EnsureAccessTag creates the Access tag if it does not exist (idempotent).
	EnsureAccessTag(ctx context.Context, acct Account, tag string) error
	// ListAccessApps returns every Access application of the account.
	ListAccessApps(ctx context.Context, acct Account) ([]AccessApp, error)
	// CreateAccessApp creates a self-hosted application (ID ignored).
	CreateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error)
	// UpdateAccessApp overwrites application app.ID. Missing -> ErrNotFound.
	UpdateAccessApp(ctx context.Context, acct Account, app AccessApp) (AccessApp, error)
	// DeleteAccessApp deletes an application. Missing -> ErrNotFound.
	DeleteAccessApp(ctx context.Context, acct Account, appID string) error
	// ListAccessPolicies returns every reusable Access policy of the account.
	ListAccessPolicies(ctx context.Context, acct Account) ([]AccessPolicy, error)
	// CreateAccessPolicy creates a reusable policy (ID ignored).
	CreateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error)
	// UpdateAccessPolicy overwrites reusable policy p.ID. Missing -> ErrNotFound.
	UpdateAccessPolicy(ctx context.Context, acct Account, p AccessPolicy) (AccessPolicy, error)
	// DeleteAccessPolicy deletes a reusable policy. Missing -> ErrNotFound.
	DeleteAccessPolicy(ctx context.Context, acct Account, policyID string) error
}

// Sentinel errors. Implementations wrap them; use errors.Is.
var (
	ErrNotFound          = model.ErrNotFound
	ErrAlreadyExists     = errors.New("already exists")
	ErrUnknownCredential = errors.New("unknown credential")
	ErrNoZone            = errors.New("no matching zone")
	ErrUnknownTunnel     = errors.New("unknown tunnel")
	ErrNoDefaultTunnel   = errors.New("no default tunnel configured")
	ErrUnauthorized      = errors.New("unauthorized")
)

// APIError carries Cloudflare's response details for a failed call.
type APIError struct {
	Op       string   // e.g. "dns.create"
	Status   int      // HTTP status; 0 for transport errors
	Codes    []int    // Cloudflare error codes
	Messages []string // Cloudflare error messages
	Err      error    // sentinel (ErrNotFound, ErrAlreadyExists, ErrUnauthorized) or transport error; may be nil
	// Credential names the configured credential used ("default" = root); "" if unknown.
	Credential string
	// startup is set by Verify: the process exits on this error, so hints must
	// not promise a retry on the next sync.
	startup bool
}

// Error reads "cloudflare <op>: HTTP <status> (codes [...]): <messages>",
// followed by how to fix it when the failure has a known cause.
func (e *APIError) Error() string {
	msg := strings.Join(e.Messages, "; ")
	if msg == "" && e.Err != nil {
		msg = e.Err.Error()
	}
	var b strings.Builder
	b.WriteString("cloudflare " + e.Op)
	if e.Status != 0 {
		fmt.Fprintf(&b, ": HTTP %d", e.Status)
	}
	if len(e.Codes) > 0 {
		fmt.Fprintf(&b, " (codes %v)", e.Codes)
	}
	b.WriteString(": " + msg)
	if hint := e.hint(); hint != "" {
		b.WriteString("; " + hint)
	}
	return b.String()
}

func (e *APIError) Unwrap() error { return e.Err }

// Health is the reachability of the Cloudflare API.
type Health struct {
	Reachable bool
	LastCheck time.Time // zero = no request made yet
	Error     string
}

// Zone is a Cloudflare zone together with the credential that manages it.
type Zone struct {
	ID         string
	Name       string // zone name, lower-case
	AccountID  string // account owning the zone; used for Access
	Credential string //credential name ("default" = root)
}

// Account returns the Access account of the zone.
func (z Zone) Account() Account { return Account{ID: z.AccountID, Credential: z.Credential} }

// Tunnel is a configured tunnel.
type Tunnel struct {
	Ref        string // configured name ("default" = root tunnel)
	ID         string // lower-case UUID
	AccountID  string
	Credential string
}

// Account is a Cloudflare account accessed through a credential.
type Account struct {
	ID         string
	Credential string
}

// DNSRecord is a DNS record as labelgate models it.
// Content holds: A/AAAA address, CNAME/MX/SRV target hostname, TXT text, CAA value.
type DNSRecord struct {
	ID       string
	Name     string // record name (FQDN), lower-case
	Type     model.DNSType
	Content  string
	Proxied  bool
	TTL      int // 1 = automatic
	Priority int // MX, SRV
	Weight   int // SRV
	Port     int // SRV
	Flags    int // CAA
	Tag      string
	Comment  string
}

// TunnelConfig is a remotely-managed cloudflared configuration.
//
// It round-trips unknown data: Extra holds every top-level key except
// "ingress" (originRequest, warp-routing, ...) verbatim, and each IngressRule
// keeps its full raw JSON object. MarshalJSON emits Extra plus "ingress".
type TunnelConfig struct {
	Ingress []IngressRule
	Extra   map[string]json.RawMessage
}

// MarshalJSON emits {"ingress":[...], <Extra...>}. "ingress" is omitted when
// there are no rules (an empty list is invalid for cloudflared).
func (c TunnelConfig) MarshalJSON() ([]byte, error) {
	out := make(map[string]json.RawMessage, len(c.Extra)+1)
	for k, v := range c.Extra {
		if k != "ingress" {
			out[k] = v
		}
	}
	if len(c.Ingress) > 0 {
		raw, err := json.Marshal(c.Ingress)
		if err != nil {
			return nil, err
		}
		out["ingress"] = raw
	}
	return json.Marshal(out)
}

// UnmarshalJSON splits "ingress" from the remaining keys. null/absent ingress = no rules.
func (c *TunnelConfig) UnmarshalJSON(data []byte) error {
	var all map[string]json.RawMessage
	if err := json.Unmarshal(data, &all); err != nil {
		return err
	}
	var rules []IngressRule
	if raw, ok := all["ingress"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &rules); err != nil {
			return fmt.Errorf("tunnel config ingress: %w", err)
		}
	}
	delete(all, "ingress")
	*c = TunnelConfig{Ingress: rules, Extra: all}
	return nil
}

// IngressRule is one ingress rule kept as its complete JSON object, so fields
// labelgate does not model (e.g. originRequest.access) survive a GET-merge-PUT.
// Treat it as immutable: With/Without return modified copies.
type IngressRule struct {
	Fields map[string]json.RawMessage
}

// Well-known ingress rule keys.
const (
	RuleHostname      = "hostname"
	RulePath          = "path"
	RuleService       = "service"
	RuleOriginRequest = "originRequest"
)

// NewIngressRule builds a rule from hostname, path and service. Empty
// hostname/path are omitted (catch-all when both are empty).
func NewIngressRule(hostname, path, service string) IngressRule {
	r := IngressRule{Fields: map[string]json.RawMessage{}}
	r = r.WithString(RuleService, service)
	if hostname != "" {
		r = r.WithString(RuleHostname, hostname)
	}
	if path != "" {
		r = r.WithString(RulePath, path)
	}
	return r
}

// String returns the string value of key, or "" if absent or not a string.
func (r IngressRule) String(key string) string {
	var s string
	if raw, ok := r.Fields[key]; ok {
		_ = json.Unmarshal(raw, &s)
	}
	return s
}

// Hostname returns the rule hostname, lower-cased ("" for catch-all rules).
func (r IngressRule) Hostname() string { return strings.ToLower(r.String(RuleHostname)) }

// Path returns the rule path regex ("" = all paths).
func (r IngressRule) Path() string { return r.String(RulePath) }

// Service returns the rule origin service.
func (r IngressRule) Service() string { return r.String(RuleService) }

// IsCatchAll reports whether the rule matches every request (no hostname, no path).
func (r IngressRule) IsCatchAll() bool { return r.Hostname() == "" && r.Path() == "" }

// With returns a copy of r with key set to raw.
func (r IngressRule) With(key string, raw json.RawMessage) IngressRule {
	out := make(map[string]json.RawMessage, len(r.Fields)+1)
	for k, v := range r.Fields {
		out[k] = v
	}
	out[key] = raw
	return IngressRule{Fields: out}
}

// WithString returns a copy of r with key set to the JSON string s.
func (r IngressRule) WithString(key, s string) IngressRule {
	raw, _ := json.Marshal(s) // marshalling a string cannot fail
	return r.With(key, raw)
}

// Without returns a copy of r without key.
func (r IngressRule) Without(key string) IngressRule {
	out := make(map[string]json.RawMessage, len(r.Fields))
	for k, v := range r.Fields {
		if k != key {
			out[k] = v
		}
	}
	return IngressRule{Fields: out}
}

// MarshalJSON emits the raw object.
func (r IngressRule) MarshalJSON() ([]byte, error) {
	if r.Fields == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(r.Fields)
}

// UnmarshalJSON stores the raw object.
func (r *IngressRule) UnmarshalJSON(data []byte) error {
	var f map[string]json.RawMessage
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*r = IngressRule{Fields: f}
	return nil
}

// AccessApp is a self-hosted Access application.
type AccessApp struct {
	ID              string
	Name            string // display name, kept verbatim (not lower-cased)
	Domain          string // hostname, lower-case
	SessionDuration string
	Tags            []string
	// PolicyIDs are the attached reusable policies in precedence order (1..n).
	PolicyIDs []string
}

// HasTag reports whether the application carries tag.
func (a AccessApp) HasTag(tag string) bool {
	for _, t := range a.Tags {
		if t == tag {
			return true
		}
	}
	return false
}

// AccessPolicy is a reusable (account-level) Access policy.
// Cloudflare offers no tags on policies: labelgate owns a policy iff its ID is
// recorded on an owned Access resource row.
type AccessPolicy struct {
	ID       string
	Name     string // display name, kept verbatim (not lower-cased)
	Decision string // API value: allow, deny, bypass, non_identity
	Include  []model.AccessRule
	Require  []model.AccessRule
	Exclude  []model.AccessRule
	// SessionDuration is left empty by labelgate (the application's applies).
	SessionDuration string
}
