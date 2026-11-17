package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/channinghe/labelgate/internal/model"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// Resource column layout. writeCols is every column except id and created_at,
// in the order produced by resourceArgs. Statements are assembled from this
// single list so column and placeholder counts cannot drift; no value is ever
// concatenated into SQL.
var writeCols = []string{
	"kind", "key", "rejected", "hostname",
	"agent_id", "container_name", "container_id", "service_name",
	"credential", "account_id", "zone_id", "cf_id",
	"record_type", "content", "proxied", "ttl", "priority", "weight", "port", "flags", "tag", "comment",
	"tunnel_id", "path", "service", "origin_json",
	"app_name", "policy_name", "decision", "policy_ids",
	"status", "cleanup", "last_error", "legacy",
	"updated_at", "orphaned_at", "synced_at",
}

var (
	// selectCols is the read column list (scanResource order).
	selectCols = "id, created_at, " + strings.Join(writeCols, ", ")
	insertSQL  = "INSERT INTO resources (" + strings.Join(writeCols, ", ") + ", created_at) VALUES (" +
		placeholders(len(writeCols)+1) + ")"
	// upsertSQL targets the partial unique index of non-rejected rows. Identity
	// columns (kind, key, rejected) and created_at keep their stored values.
	upsertSQL = insertSQL + " ON CONFLICT(kind, key) WHERE rejected = 0 DO UPDATE SET " +
		assignments(writeCols[3:], "excluded.") + " RETURNING id"
	updateSQL = "UPDATE resources SET " + assignments(writeCols, "") + " WHERE id = ?"
)

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// assignments renders "col = <prefix>col" pairs. With an empty prefix the
// right-hand side is a bind parameter instead.
func assignments(cols []string, prefix string) string {
	parts := make([]string, len(cols))
	for i, c := range cols {
		if prefix == "" {
			parts[i] = c + " = ?"
		} else {
			parts[i] = c + " = " + prefix + c
		}
	}
	return strings.Join(parts, ", ")
}

// originJSON is the stored form of model.OriginRequest: durations as integer
// seconds, zero values omitted.
type originJSON struct {
	ConnectTimeout         int64  `json:"connect_timeout,omitempty"`
	TLSTimeout             int64  `json:"tls_timeout,omitempty"`
	TCPKeepAlive           int64  `json:"tcp_keep_alive,omitempty"`
	KeepAliveTimeout       int64  `json:"keep_alive_timeout,omitempty"`
	KeepAliveConnections   int    `json:"keep_alive_connections,omitempty"`
	NoTLSVerify            bool   `json:"no_tls_verify,omitempty"`
	OriginServerName       string `json:"origin_server_name,omitempty"`
	CAPool                 string `json:"ca_pool,omitempty"`
	HTTPHostHeader         string `json:"http_host_header,omitempty"`
	NoHappyEyeballs        bool   `json:"no_happy_eyeballs,omitempty"`
	DisableChunkedEncoding bool   `json:"disable_chunked_encoding,omitempty"`
	ProxyType              string `json:"proxy_type,omitempty"`
}

func encodeOrigin(o model.OriginRequest) (string, error) {
	b, err := json.Marshal(originJSON{
		ConnectTimeout:         int64(o.ConnectTimeout / time.Second),
		TLSTimeout:             int64(o.TLSTimeout / time.Second),
		TCPKeepAlive:           int64(o.TCPKeepAlive / time.Second),
		KeepAliveTimeout:       int64(o.KeepAliveTimeout / time.Second),
		KeepAliveConnections:   o.KeepAliveConnections,
		NoTLSVerify:            o.NoTLSVerify,
		OriginServerName:       o.OriginServerName,
		CAPool:                 o.CAPool,
		HTTPHostHeader:         o.HTTPHostHeader,
		NoHappyEyeballs:        o.NoHappyEyeballs,
		DisableChunkedEncoding: o.DisableChunkedEncoding,
		ProxyType:              o.ProxyType,
	})
	return string(b), err
}

func decodeOrigin(s string) (model.OriginRequest, error) {
	var j originJSON
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		return model.OriginRequest{}, fmt.Errorf("store: decode origin_json: %w", err)
	}
	return model.OriginRequest{
		ConnectTimeout:         time.Duration(j.ConnectTimeout) * time.Second,
		TLSTimeout:             time.Duration(j.TLSTimeout) * time.Second,
		TCPKeepAlive:           time.Duration(j.TCPKeepAlive) * time.Second,
		KeepAliveTimeout:       time.Duration(j.KeepAliveTimeout) * time.Second,
		KeepAliveConnections:   j.KeepAliveConnections,
		NoTLSVerify:            j.NoTLSVerify,
		OriginServerName:       j.OriginServerName,
		CAPool:                 j.CAPool,
		HTTPHostHeader:         j.HTTPHostHeader,
		NoHappyEyeballs:        j.NoHappyEyeballs,
		DisableChunkedEncoding: j.DisableChunkedEncoding,
		ProxyType:              j.ProxyType,
	}, nil
}

func encodePolicyIDs(ids []string) (string, error) {
	if ids == nil {
		ids = []string{}
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

func decodePolicyIDs(s string) ([]string, error) {
	var ids []string
	if err := json.Unmarshal([]byte(s), &ids); err != nil {
		return nil, fmt.Errorf("store: decode policy_ids: %w", err)
	}
	if len(ids) == 0 {
		return nil, nil
	}
	return ids, nil
}

// Time codec: INTEGER unix seconds UTC; the zero time is NULL.

func unixOrNull(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return t.Unix()
}

func timeFromUnix(sec int64) time.Time { return time.Unix(sec, 0).UTC() }

func timeFromNull(n sql.NullInt64) time.Time {
	if !n.Valid {
		return time.Time{}
	}
	return timeFromUnix(n.Int64)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// resourceArgs returns bind values for writeCols, in order. updatedAt is the
// value for updated_at (the caller's clock, not r.UpdatedAt).
func resourceArgs(r model.Resource, rejected bool, updatedAt int64) ([]any, error) {
	origin, err := encodeOrigin(r.Origin)
	if err != nil {
		return nil, fmt.Errorf("store: encode origin: %w", err)
	}
	policyIDs, err := encodePolicyIDs(r.PolicyIDs)
	if err != nil {
		return nil, fmt.Errorf("store: encode policy ids: %w", err)
	}
	return []any{
		string(r.Kind), r.Key, boolInt(rejected), r.Hostname,
		r.Owner.AgentID, r.Owner.ContainerName, r.Owner.ContainerID, r.Owner.Service,
		r.Credential, r.AccountID, r.ZoneID, r.CFID,
		string(r.RecordType), r.Content, boolInt(r.Proxied), r.TTL, r.Priority, r.Weight, r.Port, r.Flags, r.Tag, r.Comment,
		r.TunnelID, r.Path, r.Service, origin,
		r.AppName, r.PolicyName, r.Decision, policyIDs,
		string(r.Status), boolInt(r.Cleanup), r.LastError, boolInt(r.Legacy),
		updatedAt, unixOrNull(r.OrphanedAt), unixOrNull(r.SyncedAt),
	}, nil
}

// rowScanner is satisfied by *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

func scanResource(sc rowScanner) (model.Resource, error) {
	var (
		r                          model.Resource
		kind, recType, status      string
		rejected, proxied, cleanup int
		legacy                     int
		origin, policyIDs          string
		created, updated           int64
		orphaned, synced           sql.NullInt64
	)
	err := sc.Scan(&r.ID, &created,
		&kind, &r.Key, &rejected, &r.Hostname,
		&r.Owner.AgentID, &r.Owner.ContainerName, &r.Owner.ContainerID, &r.Owner.Service,
		&r.Credential, &r.AccountID, &r.ZoneID, &r.CFID,
		&recType, &r.Content, &proxied, &r.TTL, &r.Priority, &r.Weight, &r.Port, &r.Flags, &r.Tag, &r.Comment,
		&r.TunnelID, &r.Path, &r.Service, &origin,
		&r.AppName, &r.PolicyName, &r.Decision, &policyIDs,
		&status, &cleanup, &r.LastError, &legacy,
		&updated, &orphaned, &synced,
	)
	if err != nil {
		return model.Resource{}, err
	}
	r.Kind, r.RecordType, r.Status = model.Kind(kind), model.DNSType(recType), model.Status(status)
	r.Rejected, r.Proxied, r.Cleanup, r.Legacy = rejected != 0, proxied != 0, cleanup != 0, legacy != 0
	r.CreatedAt, r.UpdatedAt = timeFromUnix(created), timeFromUnix(updated)
	r.OrphanedAt, r.SyncedAt = timeFromNull(orphaned), timeFromNull(synced)
	if r.Origin, err = decodeOrigin(origin); err != nil {
		return model.Resource{}, err
	}
	if r.PolicyIDs, err = decodePolicyIDs(policyIDs); err != nil {
		return model.Resource{}, err
	}
	return r, nil
}

var validStatuses = map[model.Status]bool{
	model.StatusPending: true, model.StatusActive: true, model.StatusError: true,
	model.StatusOrphaned: true, model.StatusPendingCleanup: true,
}

func validKind(k model.Kind) bool {
	for _, v := range model.Kinds {
		if v == k {
			return true
		}
	}
	return false
}

// validateResource checks the invariants the schema CHECK constraints would
// otherwise report as opaque SQL errors.
func validateResource(r model.Resource) error {
	switch {
	case !validKind(r.Kind):
		return fmt.Errorf("%w: resource kind %q", model.ErrInvalid, r.Kind)
	case r.Key == "":
		return fmt.Errorf("%w: resource key is empty", model.ErrInvalid)
	case r.Hostname == "":
		return fmt.Errorf("%w: resource hostname is empty", model.ErrInvalid)
	case !validStatuses[r.Status]:
		return fmt.Errorf("%w: resource status %q", model.ErrInvalid, r.Status)
	}
	return nil
}

// classify maps SQLite constraint violations to model.ErrConflict; other
// errors pass through unchanged.
func classify(err error) error {
	var se *sqlite.Error
	if !errors.As(err, &se) {
		return err
	}
	switch se.Code() {
	case sqlite3.SQLITE_CONSTRAINT_UNIQUE, sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY:
		return fmt.Errorf("%w: %v", model.ErrConflict, err)
	}
	return err
}
