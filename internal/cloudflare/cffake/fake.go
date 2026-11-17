// Package cffake is an in-memory cloudflare.API for tests.
//
// It emulates the Cloudflare behaviors the reconciler relies on: CNAME
// exclusivity and identical-record rejection on DNS create (ErrAlreadyExists),
// ErrNotFound on missing objects, longest-suffix zone resolution, tunnel
// configuration round-trip (stored as JSON, so callers never share memory with
// the fake), and one-shot failure injection per operation.
//
// It mirrors the production client's input checks and normalization so that
// reconciler tests see what Cloudflare would return: empty IDs -> ErrInvalid,
// unknown credentials -> ErrUnknownCredential, DNS records normalized with
// cloudflare.NormalizeDNSRecord, Access rules with
// cloudflare.NormalizeAccessRules, empty Tags/PolicyIDs/rule lists as nil.
package cffake

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/channinghe/labelgate/internal/cloudflare"
	"github.com/channinghe/labelgate/internal/model"
)

// Op names an API method for failure injection and call recording.
type Op string

const (
	OpVerify             Op = "Verify"
	OpResolveZone        Op = "ResolveZone"
	OpListDNSRecords     Op = "ListDNSRecords"
	OpCreateDNSRecord    Op = "CreateDNSRecord"
	OpUpdateDNSRecord    Op = "UpdateDNSRecord"
	OpDeleteDNSRecord    Op = "DeleteDNSRecord"
	OpGetTunnelConfig    Op = "GetTunnelConfig"
	OpPutTunnelConfig    Op = "PutTunnelConfig"
	OpEnsureAccessTag    Op = "EnsureAccessTag"
	OpListAccessApps     Op = "ListAccessApps"
	OpCreateAccessApp    Op = "CreateAccessApp"
	OpUpdateAccessApp    Op = "UpdateAccessApp"
	OpDeleteAccessApp    Op = "DeleteAccessApp"
	OpListAccessPolicies Op = "ListAccessPolicies"
	OpCreateAccessPolicy Op = "CreateAccessPolicy"
	OpUpdateAccessPolicy Op = "UpdateAccessPolicy"
	OpDeleteAccessPolicy Op = "DeleteAccessPolicy"
)

// Call records one API invocation. Arg is the most identifying argument
// (hostname, record/app/policy ID, tunnel ID, account ID or tag).
type Call struct {
	Op  Op
	Arg string
}

// Fake implements cloudflare.API in memory. The zero value is not usable; call New.
type Fake struct {
	mu            sync.Mutex
	credentials   map[string]bool
	zones         []cloudflare.Zone
	tunnels       []cloudflare.Tunnel
	defaultTunnel string
	records       map[string]map[string]cloudflare.DNSRecord // zone ID -> record ID -> record
	tunnelCfg     map[string][]byte                          // tunnel ID -> JSON config
	tags          map[string]map[string]bool                 // account ID -> tag set
	apps          map[string]map[string]cloudflare.AccessApp
	policies      map[string]map[string]cloudflare.AccessPolicy
	failures      map[Op][]error
	calls         []Call
	seq           int
}

var _ cloudflare.API = (*Fake)(nil)

// New returns an empty fake with the root credential ("default") registered.
func New() *Fake {
	return &Fake{
		credentials: map[string]bool{"default": true},
		records:     map[string]map[string]cloudflare.DNSRecord{},
		tunnelCfg:   map[string][]byte{},
		tags:        map[string]map[string]bool{},
		apps:        map[string]map[string]cloudflare.AccessApp{},
		policies:    map[string]map[string]cloudflare.AccessPolicy{},
		failures:    map[Op][]error{},
	}
}

// AddCredential registers a credential name.
func (f *Fake) AddCredential(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.credentials[name] = true
}

// AddZone registers a zone (and its credential).
func (f *Fake) AddZone(z cloudflare.Zone) {
	f.mu.Lock()
	defer f.mu.Unlock()
	z.Name = strings.ToLower(z.Name)
	f.credentials[z.Credential] = true
	f.zones = append(f.zones, z)
}

// AddTunnel registers a tunnel; isDefault makes it the target of ref "".
func (f *Fake) AddTunnel(t cloudflare.Tunnel, isDefault bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.ID = strings.ToLower(t.ID)
	f.tunnels = append(f.tunnels, t)
	if isDefault {
		f.defaultTunnel = t.Ref
	}
}

// FailNext makes the next call of op return err (queued; one error per call).
func (f *Fake) FailNext(op Op, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[op] = append(f.failures[op], err)
}

// Calls returns a copy of the recorded calls.
func (f *Fake) Calls() []Call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Call(nil), f.calls...)
}

// ResetCalls clears the call log.
func (f *Fake) ResetCalls() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// CountCalls returns how many recorded calls have the given op.
func (f *Fake) CountCalls(op Op) int {
	n := 0
	for _, c := range f.Calls() {
		if c.Op == op {
			n++
		}
	}
	return n
}

// begin records the call and pops an injected failure. Caller holds f.mu.
func (f *Fake) begin(ctx context.Context, op Op, arg string) error {
	f.calls = append(f.calls, Call{Op: op, Arg: arg})
	if err := ctx.Err(); err != nil {
		return err
	}
	if q := f.failures[op]; len(q) > 0 {
		f.failures[op] = q[1:]
		return q[0]
	}
	return nil
}

func (f *Fake) newID(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s-%04d", prefix, f.seq)
}

func notFound(what, id string) error {
	return fmt.Errorf("%s %s: %w", what, id, cloudflare.ErrNotFound)
}

func alreadyExists(what, id string) error {
	return fmt.Errorf("%s %s: %w", what, id, cloudflare.ErrAlreadyExists)
}

func invalid(what string) error {
	return fmt.Errorf("%s: %w", what, model.ErrInvalid)
}

// checkCredential mirrors the production client's credential lookup. Caller holds f.mu.
func (f *Fake) checkCredential(name string) error {
	if !f.credentials[name] {
		return fmt.Errorf("credential %q: %w", name, cloudflare.ErrUnknownCredential)
	}
	return nil
}

// checkZone validates a zone argument. Caller holds f.mu.
func (f *Fake) checkZone(z cloudflare.Zone) error {
	if z.ID == "" {
		return invalid("empty zone ID")
	}
	return f.checkCredential(z.Credential)
}

// checkAccount validates an account argument. Caller holds f.mu.
func (f *Fake) checkAccount(a cloudflare.Account) error {
	if a.ID == "" {
		return invalid("empty account ID")
	}
	return f.checkCredential(a.Credential)
}

// checkTunnel validates a tunnel argument. Caller holds f.mu.
func (f *Fake) checkTunnel(t cloudflare.Tunnel) error {
	if t.ID == "" || t.AccountID == "" {
		return invalid("tunnel without ID or account")
	}
	return f.checkCredential(t.Credential)
}

// Verify always succeeds unless a failure is injected.
func (f *Fake) Verify(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.begin(ctx, OpVerify, "")
}

// Health reports reachable with the current time.
func (f *Fake) Health() cloudflare.Health {
	return cloudflare.Health{Reachable: true, LastCheck: time.Now()}
}

// ResolveZone implements longest-suffix matching as specified on cloudflare.API.
func (f *Fake) ResolveZone(ctx context.Context, hostname, credential string) (cloudflare.Zone, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	host := model.NormalizeHostname(hostname)
	if err := f.begin(ctx, OpResolveZone, host); err != nil {
		return cloudflare.Zone{}, err
	}
	if credential != "" && !f.credentials[credential] {
		return cloudflare.Zone{}, fmt.Errorf("%s: %w", credential, cloudflare.ErrUnknownCredential)
	}
	best, found := cloudflare.Zone{}, false
	for _, z := range f.zones {
		if credential != "" && z.Credential != credential {
			continue
		}
		if host != z.Name && !strings.HasSuffix(host, "."+z.Name) {
			continue
		}
		if !found || betterZone(z, best) {
			best, found = z, true
		}
	}
	if !found {
		return cloudflare.Zone{}, fmt.Errorf("%s: %w", host, cloudflare.ErrNoZone)
	}
	return best, nil
}

func betterZone(z, cur cloudflare.Zone) bool {
	if len(z.Name) != len(cur.Name) {
		return len(z.Name) > len(cur.Name)
	}
	if (z.Credential == "default") != (cur.Credential == "default") {
		return z.Credential == "default"
	}
	return z.Credential < cur.Credential
}

// ResolveTunnel resolves by configured name or ID; "" = default.
func (f *Fake) ResolveTunnel(ref string) (cloudflare.Tunnel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ref == "" {
		if f.defaultTunnel == "" {
			return cloudflare.Tunnel{}, cloudflare.ErrNoDefaultTunnel
		}
		ref = f.defaultTunnel
	}
	for _, t := range f.tunnels {
		if t.Ref == ref || t.ID == strings.ToLower(ref) {
			return t, nil
		}
	}
	return cloudflare.Tunnel{}, fmt.Errorf("%s: %w", ref, cloudflare.ErrUnknownTunnel)
}

// PutRecord seeds a record (assigning an ID if empty) and returns it.
func (f *Fake) PutRecord(zoneID string, rec cloudflare.DNSRecord) cloudflare.DNSRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	if rec.ID == "" {
		rec.ID = f.newID("rec")
	}
	rec = cloudflare.NormalizeDNSRecord(rec)
	f.zoneRecords(zoneID)[rec.ID] = rec
	return rec
}

// Records returns the zone's records sorted by name, type, content.
func (f *Fake) Records(zoneID string) []cloudflare.DNSRecord {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sortedRecords(f.records[zoneID])
}

func (f *Fake) zoneRecords(zoneID string) map[string]cloudflare.DNSRecord {
	m, ok := f.records[zoneID]
	if !ok {
		m = map[string]cloudflare.DNSRecord{}
		f.records[zoneID] = m
	}
	return m
}

func sortedRecords(m map[string]cloudflare.DNSRecord) []cloudflare.DNSRecord {
	out := make([]cloudflare.DNSRecord, 0, len(m))
	for _, r := range m {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		return a.Content < b.Content
	})
	return out
}

// ListDNSRecords returns all records of the zone.
func (f *Fake) ListDNSRecords(ctx context.Context, zone cloudflare.Zone) ([]cloudflare.DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpListDNSRecords, zone.ID); err != nil {
		return nil, err
	}
	if err := f.checkZone(zone); err != nil {
		return nil, err
	}
	return sortedRecords(f.records[zone.ID]), nil
}

// CreateDNSRecord emulates Cloudflare's CNAME exclusivity and identical-record rules.
func (f *Fake) CreateDNSRecord(ctx context.Context, zone cloudflare.Zone, rec cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec = cloudflare.NormalizeDNSRecord(rec)
	if err := f.begin(ctx, OpCreateDNSRecord, rec.Name); err != nil {
		return cloudflare.DNSRecord{}, err
	}
	if err := f.checkZone(zone); err != nil {
		return cloudflare.DNSRecord{}, err
	}
	for _, ex := range f.records[zone.ID] {
		if ex.Name != rec.Name {
			continue
		}
		cnameClash := ex.Type == model.DNSTypeCNAME || rec.Type == model.DNSTypeCNAME
		identical := ex.Type == rec.Type && ex.Content == rec.Content
		if cnameClash || identical {
			return cloudflare.DNSRecord{}, alreadyExists("dns record", rec.Name)
		}
	}
	rec.ID = f.newID("rec")
	f.zoneRecords(zone.ID)[rec.ID] = rec
	return rec, nil
}

// UpdateDNSRecord overwrites an existing record.
func (f *Fake) UpdateDNSRecord(ctx context.Context, zone cloudflare.Zone, rec cloudflare.DNSRecord) (cloudflare.DNSRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpUpdateDNSRecord, rec.ID); err != nil {
		return cloudflare.DNSRecord{}, err
	}
	if rec.ID == "" {
		return cloudflare.DNSRecord{}, invalid("empty record ID")
	}
	if err := f.checkZone(zone); err != nil {
		return cloudflare.DNSRecord{}, err
	}
	m := f.records[zone.ID]
	if _, ok := m[rec.ID]; !ok {
		return cloudflare.DNSRecord{}, notFound("dns record", rec.ID)
	}
	rec = cloudflare.NormalizeDNSRecord(rec)
	m[rec.ID] = rec
	return rec, nil
}

// DeleteDNSRecord removes a record.
func (f *Fake) DeleteDNSRecord(ctx context.Context, zone cloudflare.Zone, recordID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpDeleteDNSRecord, recordID); err != nil {
		return err
	}
	if recordID == "" {
		return invalid("empty record ID")
	}
	if err := f.checkZone(zone); err != nil {
		return err
	}
	m := f.records[zone.ID]
	if _, ok := m[recordID]; !ok {
		return notFound("dns record", recordID)
	}
	delete(m, recordID)
	return nil
}

// SetTunnelConfig seeds a tunnel configuration from JSON (e.g. a fixture).
func (f *Fake) SetTunnelConfig(tunnelID string, configJSON string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnelCfg[strings.ToLower(tunnelID)] = []byte(configJSON)
}

// TunnelConfigJSON returns the stored configuration JSON ("" if never set).
func (f *Fake) TunnelConfigJSON(tunnelID string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return string(f.tunnelCfg[strings.ToLower(tunnelID)])
}

// GetTunnelConfig decodes the stored configuration.
func (f *Fake) GetTunnelConfig(ctx context.Context, t cloudflare.Tunnel) (cloudflare.TunnelConfig, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpGetTunnelConfig, t.ID); err != nil {
		return cloudflare.TunnelConfig{}, err
	}
	if err := f.checkTunnel(t); err != nil {
		return cloudflare.TunnelConfig{}, err
	}
	if !f.knownTunnel(t.ID) {
		return cloudflare.TunnelConfig{}, notFound("tunnel", t.ID)
	}
	var cfg cloudflare.TunnelConfig
	raw := f.tunnelCfg[t.ID]
	if len(raw) == 0 {
		return cfg, nil
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cloudflare.TunnelConfig{}, fmt.Errorf("fake tunnel config: %w", err)
	}
	return cfg, nil
}

// PutTunnelConfig stores the configuration as JSON.
func (f *Fake) PutTunnelConfig(ctx context.Context, t cloudflare.Tunnel, cfg cloudflare.TunnelConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpPutTunnelConfig, t.ID); err != nil {
		return err
	}
	if err := f.checkTunnel(t); err != nil {
		return err
	}
	if !f.knownTunnel(t.ID) {
		return notFound("tunnel", t.ID)
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	f.tunnelCfg[t.ID] = raw
	return nil
}

func (f *Fake) knownTunnel(id string) bool {
	for _, t := range f.tunnels {
		if t.ID == id {
			return true
		}
	}
	return false
}

// Tags returns the account's tags, sorted.
func (f *Fake) Tags(accountID string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.tags[accountID]))
	for t := range f.tags[accountID] {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// EnsureAccessTag creates the tag if missing.
func (f *Fake) EnsureAccessTag(ctx context.Context, acct cloudflare.Account, tag string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpEnsureAccessTag, tag); err != nil {
		return err
	}
	if err := f.checkAccount(acct); err != nil {
		return err
	}
	if f.tags[acct.ID] == nil {
		f.tags[acct.ID] = map[string]bool{}
	}
	f.tags[acct.ID][tag] = true
	return nil
}

// PutApp seeds an application (assigning an ID if empty) and returns it.
func (f *Fake) PutApp(accountID string, app cloudflare.AccessApp) cloudflare.AccessApp {
	f.mu.Lock()
	defer f.mu.Unlock()
	if app.ID == "" {
		app.ID = f.newID("app")
	}
	app.Domain = strings.ToLower(app.Domain)
	f.accountApps(accountID)[app.ID] = cloneApp(app)
	return cloneApp(app)
}

// Apps returns the account's applications sorted by domain then ID.
func (f *Fake) Apps(accountID string) []cloudflare.AccessApp {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sortedApps(f.apps[accountID])
}

func (f *Fake) accountApps(accountID string) map[string]cloudflare.AccessApp {
	m, ok := f.apps[accountID]
	if !ok {
		m = map[string]cloudflare.AccessApp{}
		f.apps[accountID] = m
	}
	return m
}

func sortedApps(m map[string]cloudflare.AccessApp) []cloudflare.AccessApp {
	out := make([]cloudflare.AccessApp, 0, len(m))
	for _, a := range m {
		out = append(out, cloneApp(a))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func cloneApp(a cloudflare.AccessApp) cloudflare.AccessApp {
	a.Tags = append([]string(nil), a.Tags...)
	a.PolicyIDs = append([]string(nil), a.PolicyIDs...)
	return a
}

// ListAccessApps returns all applications of the account.
func (f *Fake) ListAccessApps(ctx context.Context, acct cloudflare.Account) ([]cloudflare.AccessApp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpListAccessApps, acct.ID); err != nil {
		return nil, err
	}
	if err := f.checkAccount(acct); err != nil {
		return nil, err
	}
	return sortedApps(f.apps[acct.ID]), nil
}

// CreateAccessApp rejects a second application for the same domain.
func (f *Fake) CreateAccessApp(ctx context.Context, acct cloudflare.Account, app cloudflare.AccessApp) (cloudflare.AccessApp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	app.Domain = strings.ToLower(app.Domain)
	if err := f.begin(ctx, OpCreateAccessApp, app.Domain); err != nil {
		return cloudflare.AccessApp{}, err
	}
	if err := f.checkAccount(acct); err != nil {
		return cloudflare.AccessApp{}, err
	}
	for _, ex := range f.apps[acct.ID] {
		if ex.Domain == app.Domain {
			return cloudflare.AccessApp{}, alreadyExists("access app", app.Domain)
		}
	}
	if err := f.checkPolicies(acct.ID, app.PolicyIDs); err != nil {
		return cloudflare.AccessApp{}, err
	}
	app.ID = f.newID("app")
	f.accountApps(acct.ID)[app.ID] = cloneApp(app)
	return cloneApp(app), nil
}

// UpdateAccessApp overwrites an existing application.
func (f *Fake) UpdateAccessApp(ctx context.Context, acct cloudflare.Account, app cloudflare.AccessApp) (cloudflare.AccessApp, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpUpdateAccessApp, app.ID); err != nil {
		return cloudflare.AccessApp{}, err
	}
	if app.ID == "" {
		return cloudflare.AccessApp{}, invalid("empty app ID")
	}
	if err := f.checkAccount(acct); err != nil {
		return cloudflare.AccessApp{}, err
	}
	m := f.apps[acct.ID]
	if _, ok := m[app.ID]; !ok {
		return cloudflare.AccessApp{}, notFound("access app", app.ID)
	}
	if err := f.checkPolicies(acct.ID, app.PolicyIDs); err != nil {
		return cloudflare.AccessApp{}, err
	}
	app.Domain = strings.ToLower(app.Domain)
	m[app.ID] = cloneApp(app)
	return cloneApp(app), nil
}

// DeleteAccessApp removes an application.
func (f *Fake) DeleteAccessApp(ctx context.Context, acct cloudflare.Account, appID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpDeleteAccessApp, appID); err != nil {
		return err
	}
	if appID == "" {
		return invalid("empty app ID")
	}
	if err := f.checkAccount(acct); err != nil {
		return err
	}
	m := f.apps[acct.ID]
	if _, ok := m[appID]; !ok {
		return notFound("access app", appID)
	}
	delete(m, appID)
	return nil
}

// checkPolicies emulates Cloudflare rejecting unknown policy IDs. Caller holds f.mu.
func (f *Fake) checkPolicies(accountID string, ids []string) error {
	for _, id := range ids {
		if _, ok := f.policies[accountID][id]; !ok {
			return notFound("access policy", id)
		}
	}
	return nil
}

// PutPolicy seeds a reusable policy (assigning an ID if empty) and returns it.
func (f *Fake) PutPolicy(accountID string, p cloudflare.AccessPolicy) cloudflare.AccessPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p.ID == "" {
		p.ID = f.newID("pol")
	}
	if n, err := normalizePolicy(p); err == nil {
		p = n // invalid seeds are stored verbatim (hand-made live state)
	}
	f.accountPolicies(accountID)[p.ID] = clonePolicy(p)
	return clonePolicy(p)
}

// Policies returns the account's reusable policies sorted by ID.
func (f *Fake) Policies(accountID string) []cloudflare.AccessPolicy {
	f.mu.Lock()
	defer f.mu.Unlock()
	return sortedPolicies(f.policies[accountID])
}

func (f *Fake) accountPolicies(accountID string) map[string]cloudflare.AccessPolicy {
	m, ok := f.policies[accountID]
	if !ok {
		m = map[string]cloudflare.AccessPolicy{}
		f.policies[accountID] = m
	}
	return m
}

func sortedPolicies(m map[string]cloudflare.AccessPolicy) []cloudflare.AccessPolicy {
	out := make([]cloudflare.AccessPolicy, 0, len(m))
	for _, p := range m {
		out = append(out, clonePolicy(p))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// normalizePolicy applies the production client's rule normalization.
func normalizePolicy(p cloudflare.AccessPolicy) (cloudflare.AccessPolicy, error) {
	var err error
	if p.Include, err = cloudflare.NormalizeAccessRules(p.Include); err != nil {
		return p, err
	}
	if p.Require, err = cloudflare.NormalizeAccessRules(p.Require); err != nil {
		return p, err
	}
	p.Exclude, err = cloudflare.NormalizeAccessRules(p.Exclude)
	return p, err
}

func clonePolicy(p cloudflare.AccessPolicy) cloudflare.AccessPolicy {
	p.Include = cloneRules(p.Include)
	p.Require = cloneRules(p.Require)
	p.Exclude = cloneRules(p.Exclude)
	return p
}

func cloneRules(rs []model.AccessRule) []model.AccessRule {
	if len(rs) == 0 {
		return nil
	}
	out := make([]model.AccessRule, len(rs))
	for i, r := range rs {
		out[i] = model.AccessRule{Selector: r.Selector, Values: append([]string(nil), r.Values...)}
	}
	return out
}

// ListAccessPolicies returns all reusable policies of the account.
func (f *Fake) ListAccessPolicies(ctx context.Context, acct cloudflare.Account) ([]cloudflare.AccessPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpListAccessPolicies, acct.ID); err != nil {
		return nil, err
	}
	if err := f.checkAccount(acct); err != nil {
		return nil, err
	}
	return sortedPolicies(f.policies[acct.ID]), nil
}

// CreateAccessPolicy stores a new reusable policy.
func (f *Fake) CreateAccessPolicy(ctx context.Context, acct cloudflare.Account, p cloudflare.AccessPolicy) (cloudflare.AccessPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpCreateAccessPolicy, p.Name); err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	if err := f.checkAccount(acct); err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	p, err := normalizePolicy(p)
	if err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	p.ID = f.newID("pol")
	f.accountPolicies(acct.ID)[p.ID] = clonePolicy(p)
	return clonePolicy(p), nil
}

// UpdateAccessPolicy overwrites an existing reusable policy.
func (f *Fake) UpdateAccessPolicy(ctx context.Context, acct cloudflare.Account, p cloudflare.AccessPolicy) (cloudflare.AccessPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpUpdateAccessPolicy, p.ID); err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	if p.ID == "" {
		return cloudflare.AccessPolicy{}, invalid("empty policy ID")
	}
	if err := f.checkAccount(acct); err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	p, err := normalizePolicy(p)
	if err != nil {
		return cloudflare.AccessPolicy{}, err
	}
	m := f.policies[acct.ID]
	if _, ok := m[p.ID]; !ok {
		return cloudflare.AccessPolicy{}, notFound("access policy", p.ID)
	}
	m[p.ID] = clonePolicy(p)
	return clonePolicy(p), nil
}

// DeleteAccessPolicy removes a reusable policy; fails while an app still references it.
func (f *Fake) DeleteAccessPolicy(ctx context.Context, acct cloudflare.Account, policyID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.begin(ctx, OpDeleteAccessPolicy, policyID); err != nil {
		return err
	}
	if policyID == "" {
		return invalid("empty policy ID")
	}
	if err := f.checkAccount(acct); err != nil {
		return err
	}
	m := f.policies[acct.ID]
	if _, ok := m[policyID]; !ok {
		return notFound("access policy", policyID)
	}
	for _, a := range f.apps[acct.ID] {
		for _, id := range a.PolicyIDs {
			if id == policyID {
				return fmt.Errorf("access policy %s still used by app %s", policyID, a.ID)
			}
		}
	}
	delete(m, policyID)
	return nil
}
