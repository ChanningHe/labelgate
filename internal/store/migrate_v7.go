package store

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

// applyV7 creates schema 7 and, when a v0.2 database is present, converts it
// (dev/docs/v0.3-design.md §5.2). It runs inside the caller's transaction.
func applyV7(ctx context.Context, tx *sql.Tx, env stepEnv) error {
	legacy, err := tableExists(ctx, tx, "managed_resources")
	if err != nil {
		return err
	}
	hasOldAgents, err := tableExists(ctx, tx, "agents")
	if err != nil {
		return err
	}
	if hasOldAgents {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE agents RENAME TO agents_v2"); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, schemaV7); err != nil {
		return err
	}
	id, err := newInstanceID()
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES ('instance_id', ?)`, id); err != nil {
		return err
	}
	if legacy {
		if err := convertLegacyResources(ctx, tx, env); err != nil {
			return err
		}
	}
	if hasOldAgents {
		if err := convertLegacyAgents(ctx, tx, env.now); err != nil {
			return err
		}
	}
	return dropLegacyTables(ctx, tx)
}

func dropLegacyTables(ctx context.Context, tx *sql.Tx) error {
	for _, t := range []string{"managed_resources", "agents_v2", "sync_state"} {
		if _, err := tx.ExecContext(ctx, "DROP TABLE IF EXISTS "+t); err != nil {
			return err
		}
	}
	return nil
}

// legacyRow is one managed_resources row with NULLs folded to zero values and
// timestamps already converted to unix seconds.
type legacyRow struct {
	id                                      string
	resourceType, cfID, zoneID, hostname    string
	recordType, content                     string
	proxied                                 bool
	ttl                                     int
	tunnelID, service, path                 string
	accessAppID, accountID                  string
	appName, policyName, decision           string
	containerID, containerName, serviceName string
	agentID, credential                     string
	status                                  string
	cleanup                                 bool
	lastError                               string
	created, updated                        int64
}

const legacySelect = `SELECT id, resource_type, COALESCE(cf_id,''), COALESCE(zone_id,''), hostname,
	COALESCE(record_type,''), COALESCE(content,''), COALESCE(proxied,0), COALESCE(ttl,0),
	COALESCE(tunnel_id,''), COALESCE(service,''), COALESCE(path,''),
	COALESCE(access_app_id,''), COALESCE(account_id,''),
	COALESCE(access_app_name,''), COALESCE(access_policy_name,''), COALESCE(access_decision,''),
	COALESCE(container_id,''), COALESCE(container_name,''), service_name,
	COALESCE(agent_id,''), COALESCE(credential,''),
	COALESCE(status,'active'), COALESCE(cleanup_enabled,1), COALESCE(last_error,''),
	CAST(created_at AS TEXT), CAST(updated_at AS TEXT)
	FROM managed_resources WHERE COALESCE(status,'active') != 'deleted'`

func loadLegacyRows(ctx context.Context, tx *sql.Tx, now int64) ([]legacyRow, error) {
	rows, err := tx.QueryContext(ctx, legacySelect)
	if err != nil {
		return nil, fmt.Errorf("read managed_resources: %w", err)
	}
	defer rows.Close()
	var out []legacyRow
	for rows.Next() {
		var r legacyRow
		var proxied, cleanup int
		var created, updated sql.NullString
		err := rows.Scan(&r.id, &r.resourceType, &r.cfID, &r.zoneID, &r.hostname,
			&r.recordType, &r.content, &proxied, &r.ttl,
			&r.tunnelID, &r.service, &r.path, &r.accessAppID, &r.accountID,
			&r.appName, &r.policyName, &r.decision,
			&r.containerID, &r.containerName, &r.serviceName,
			&r.agentID, &r.credential, &r.status, &cleanup, &r.lastError,
			&created, &updated)
		if err != nil {
			return nil, fmt.Errorf("scan managed_resources: %w", err)
		}
		r.proxied, r.cleanup = proxied != 0, cleanup != 0
		r.created = legacyTimeOr(created, now)
		r.updated = legacyTimeOr(updated, now)
		out = append(out, r)
	}
	return out, rows.Err()
}

// legacyTimeOr parses a legacy timestamp; NULL or unparseable text becomes fallback.
func legacyTimeOr(s sql.NullString, fallback int64) int64 {
	if !s.Valid {
		return fallback
	}
	if sec, ok := parseLegacyTime(s.String); ok {
		return sec
	}
	return fallback
}

// normalizeLegacyAgentID lower-cases a v0.2 agent ID so mixed-case IDs
// (e.g. "NAS.lan") stay valid under the v0.3 pattern. An ID that is still
// invalid after lower-casing is returned unchanged.
func normalizeLegacyAgentID(id string) string {
	if lower := strings.ToLower(id); model.ValidAgentID(lower) {
		return lower
	}
	return id
}

// toResource maps a legacy row to a schema-7 resource. ok is false when the
// row cannot be represented (unknown kind, empty hostname, DNS without type).
func (r legacyRow) toResource() (model.Resource, bool) {
	host := model.NormalizeHostname(r.hostname)
	res := model.Resource{
		Kind:     model.Kind(r.resourceType),
		Hostname: host,
		Owner: model.Owner{AgentID: normalizeLegacyAgentID(r.agentID), ContainerName: r.containerName,
			ContainerID: r.containerID, Service: r.serviceName},
		Credential: r.credential, AccountID: r.accountID, ZoneID: r.zoneID, CFID: r.cfID,
		RecordType: model.DNSType(strings.ToUpper(strings.TrimSpace(r.recordType))),
		Content:    r.content, Proxied: r.proxied, TTL: r.ttl,
		TunnelID: r.tunnelID, Path: r.path, Service: r.service,
		AppName: r.appName, PolicyName: r.policyName, Decision: r.decision,
		Cleanup: r.cleanup, LastError: r.lastError,
		CreatedAt: timeFromUnix(r.created), UpdatedAt: timeFromUnix(r.updated),
	}
	if host == "" || !validKind(res.Kind) {
		return model.Resource{}, false
	}
	switch res.Kind {
	case model.KindDNS:
		if res.RecordType == "" {
			return model.Resource{}, false
		}
		res.Key = model.DNSKey(host, res.RecordType, res.Content)
		res.Legacy = true
	case model.KindTunnel:
		res.Key = model.TunnelKey(res.TunnelID, host, res.Path)
		res.RecordType = ""
		res.Content = ""
	case model.KindAccess:
		res.Key = model.AccessKey(host)
		res.CFID = firstNonEmpty(r.accessAppID, r.cfID)
		res.RecordType = ""
		res.Legacy = true
	}
	res.Status = mapLegacyStatus(r.status, r.cleanup)
	if res.Status == model.StatusPendingCleanup || res.Status == model.StatusOrphaned {
		res.OrphanedAt = res.UpdatedAt
	}
	return res, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func mapLegacyStatus(status string, cleanup bool) model.Status {
	switch status {
	case "active":
		return model.StatusActive
	case "error":
		return model.StatusError
	case "orphaned":
		if cleanup {
			return model.StatusPendingCleanup
		}
		return model.StatusOrphaned
	case "pending_cleanup":
		return model.StatusPendingCleanup
	default:
		return model.StatusPending // unknown legacy state: re-apply rather than trust
	}
}

// dedupeLatest keeps, per (kind, key), the resource with the latest
// UpdatedAt (ties: latest CreatedAt, then the greater legacy id). It returns
// the survivors and the number of dropped rows.
func dedupeLatest(rows []keyedResource) ([]keyedResource, int) {
	best := map[[2]string]keyedResource{}
	dropped := 0
	for _, r := range rows {
		k := [2]string{string(r.res.Kind), r.res.Key}
		cur, seen := best[k]
		if !seen {
			best[k] = r
			continue
		}
		dropped++
		if r.newerThan(cur) {
			best[k] = r
		}
	}
	out := make([]keyedResource, 0, len(best))
	for _, r := range best {
		out = append(out, r)
	}
	return out, dropped
}

// keyedResource pairs a converted resource with its legacy id for tie-breaking.
type keyedResource struct {
	res      model.Resource
	legacyID string
}

func (a keyedResource) newerThan(b keyedResource) bool {
	if !a.res.UpdatedAt.Equal(b.res.UpdatedAt) {
		return a.res.UpdatedAt.After(b.res.UpdatedAt)
	}
	if !a.res.CreatedAt.Equal(b.res.CreatedAt) {
		return a.res.CreatedAt.After(b.res.CreatedAt)
	}
	return a.legacyID > b.legacyID
}

func convertLegacyResources(ctx context.Context, tx *sql.Tx, env stepEnv) error {
	rows, err := loadLegacyRows(ctx, tx, env.now)
	if err != nil {
		return err
	}
	converted := make([]keyedResource, 0, len(rows))
	skipped := 0
	for _, r := range rows {
		res, ok := r.toResource()
		if !ok {
			skipped++
			continue
		}
		converted = append(converted, keyedResource{res: res, legacyID: r.id})
	}
	kept, dropped := dedupeLatest(converted)
	if skipped > 0 || dropped > 0 {
		env.log.Warn("some v0.2 resources could not be migrated and were dropped from the database; "+
			"their Cloudflare objects were not changed: review them in the Cloudflare dashboard",
			"unrepresentable", skipped, "duplicates", dropped)
	}
	resources := make([]model.Resource, 0, len(kept)+len(kept)/2)
	for _, k := range kept {
		resources = append(resources, k.res)
	}
	resources = append(resources, synthesizeTunnelCNAMEs(resources)...)
	sortForInsert(resources)
	for _, r := range resources {
		if err := insertMigrated(ctx, tx, r); err != nil {
			return fmt.Errorf("insert migrated %s %q: %w", r.Kind, r.Key, err)
		}
	}
	return nil
}

func sortForInsert(rs []model.Resource) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Key < b.Key
	})
}

// synthesizeTunnelCNAMEs returns a CNAME row for every distinct
// (tunnel_id, hostname) of tunnel rows that has no DNS row keyed host|CNAME:
// v0.2 created those CNAMEs but never tracked them. Owner, status and cleanup
// come from the tunnel row with the smallest owner.
func synthesizeTunnelCNAMEs(rs []model.Resource) []model.Resource {
	haveDNS := map[string]bool{}
	type pair struct{ tunnelID, host string }
	src := map[pair]model.Resource{}
	for _, r := range rs {
		switch {
		case r.Kind == model.KindDNS:
			haveDNS[r.Key] = true
		case r.Kind == model.KindTunnel && r.TunnelID != "":
			p := pair{r.TunnelID, r.Hostname}
			if cur, ok := src[p]; !ok || tunnelSourceLess(r, cur) {
				src[p] = r
			}
		}
	}
	pairs := make([]pair, 0, len(src))
	for p := range src {
		pairs = append(pairs, p)
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].tunnelID != pairs[j].tunnelID {
			return pairs[i].tunnelID < pairs[j].tunnelID
		}
		return pairs[i].host < pairs[j].host
	})
	out := make([]model.Resource, 0, len(pairs))
	for _, p := range pairs {
		key := model.DNSKey(p.host, model.DNSTypeCNAME, "")
		if haveDNS[key] { // identity is host|CNAME: one row per hostname, lowest tunnel ID wins
			continue
		}
		haveDNS[key] = true
		out = append(out, tunnelCNAME(src[p]))
	}
	return out
}

// tunnelSourceLess orders candidate tunnel rows: smallest service name first,
// then the remaining owner fields and path for determinism.
func tunnelSourceLess(a, b model.Resource) bool {
	if a.Owner.Service != b.Owner.Service {
		return a.Owner.Service < b.Owner.Service
	}
	if a.Owner.Less(b.Owner) || b.Owner.Less(a.Owner) {
		return a.Owner.Less(b.Owner)
	}
	return a.Path < b.Path
}

func tunnelCNAME(t model.Resource) model.Resource {
	return model.Resource{
		Kind:       model.KindDNS,
		Key:        model.DNSKey(t.Hostname, model.DNSTypeCNAME, ""),
		Hostname:   t.Hostname,
		Owner:      t.Owner,
		Credential: t.Credential, AccountID: t.AccountID, ZoneID: t.ZoneID,
		RecordType: model.DNSTypeCNAME, Content: model.TunnelTarget(t.TunnelID),
		Proxied: true, TTL: 1, TunnelID: t.TunnelID,
		Status: t.Status, Cleanup: t.Cleanup, Legacy: true,
		CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, OrphanedAt: t.OrphanedAt,
	}
}

func insertMigrated(ctx context.Context, tx *sql.Tx, r model.Resource) error {
	args, err := resourceArgs(r, false, r.UpdatedAt.Unix())
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, insertSQL, append(args, r.CreatedAt.Unix())...)
	return err
}

// convertLegacyAgents copies agents_v2 into agents. last_seen is the
// migration time (the v0.2 value was agent-supplied); snapshot stays NULL so
// the agent is held until it reports.
func convertLegacyAgents(ctx context.Context, tx *sql.Tx, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT id, COALESCE(name,''), COALESCE(default_tunnel,''),
		COALESCE(public_ip,''), CAST(created_at AS TEXT) FROM agents_v2 ORDER BY id`)
	if err != nil {
		return fmt.Errorf("read agents: %w", err)
	}
	type agentRow struct {
		id, name, tunnel, ip string
		created              int64
	}
	var agents []agentRow
	for rows.Next() {
		var a agentRow
		var created sql.NullString
		if err := rows.Scan(&a.id, &a.name, &a.tunnel, &a.ip, &created); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan agents: %w", err)
		}
		a.created = legacyTimeOr(created, now)
		agents = append(agents, a)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, a := range agents {
		_, err := tx.ExecContext(ctx, `INSERT INTO agents
			(id, name, default_tunnel, public_ip, last_seen, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, normalizeLegacyAgentID(a.id), a.name, a.tunnel, a.ip, now, a.created, now)
		if err != nil {
			return fmt.Errorf("insert migrated agent %q: %w", a.id, err)
		}
	}
	return nil
}
