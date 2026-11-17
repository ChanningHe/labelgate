package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	"github.com/channinghe/labelgate/internal/model"
	"github.com/channinghe/labelgate/internal/store"
)

const maxHostnameLen = 253

var validStatuses = map[model.Status]bool{
	model.StatusPending:        true,
	model.StatusActive:         true,
	model.StatusError:          true,
	model.StatusOrphaned:       true,
	model.StatusPendingCleanup: true,
}

// paramError is a client mistake; its message is safe to show.
type paramError string

func (e paramError) Error() string { return string(e) }

// parseResourceQuery turns URL parameters into a store query.
// Invalid values are rejected instead of being silently ignored.
func parseResourceQuery(kind model.Kind, v url.Values) (store.ResourceQuery, error) {
	q := store.ResourceQuery{Kind: kind, Limit: store.DefaultLimit}

	if s := model.Status(v.Get("status")); s != "" {
		if !validStatuses[s] {
			return q, paramError("invalid status")
		}
		q.Status = s
	}
	if v.Has("agent_id") {
		id := v.Get("agent_id")
		if id != model.LocalAgentID && !model.ValidAgentID(id) {
			return q, paramError("invalid agent_id")
		}
		q.AgentID, q.HasAgentID = id, true
	}
	if h := v.Get("hostname"); h != "" {
		if len(h) > maxHostnameLen {
			return q, paramError("invalid hostname")
		}
		q.Hostname = model.NormalizeHostname(h)
	}
	if v.Has("limit") {
		n, err := strconv.Atoi(v.Get("limit"))
		if err != nil || n < 1 || n > store.MaxLimit {
			return q, paramError("invalid limit")
		}
		q.Limit = n
	}
	if v.Has("offset") {
		n, err := strconv.Atoi(v.Get("offset"))
		if err != nil || n < 0 {
			return q, paramError("invalid offset")
		}
		q.Offset = n
	}
	return q, nil
}

// resourceHandler lists resources of one kind with filters and pagination.
func (s *Server) resourceHandler(kind model.Kind) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q, err := parseResourceQuery(kind, r.URL.Query())
		if err != nil {
			var pe paramError
			if errors.As(err, &pe) {
				writeError(w, http.StatusBadRequest, pe.Error())
				return
			}
			s.internalError(w, r, err)
			return
		}
		page, err := s.deps.Store.ListResources(r.Context(), q)
		if err != nil {
			s.internalError(w, r, err)
			return
		}
		out := ResourceListDTO{Resources: make([]ResourceDTO, 0, len(page.Resources)), Total: page.Total}
		for _, res := range page.Resources {
			out.Resources = append(out.Resources, toResourceDTO(res))
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// toResourceDTO maps a stored row to the dashboard's ManagedResource shape.
// Only fields of the row's kind are set (the DTO uses omitempty).
func toResourceDTO(r model.Resource) ResourceDTO {
	d := ResourceDTO{
		ID:             strconv.FormatInt(r.ID, 10),
		ResourceType:   string(r.Kind),
		CFID:           r.CFID,
		ZoneID:         r.ZoneID,
		Hostname:       r.Hostname,
		TunnelID:       r.TunnelID,
		AccountID:      r.AccountID,
		ContainerID:    r.Owner.ContainerID,
		ContainerName:  r.Owner.ContainerName,
		ServiceName:    r.Owner.Service,
		AgentID:        r.Owner.AgentID,
		Credential:     r.Credential,
		Status:         string(r.Status),
		CleanupEnabled: r.Cleanup,
		LastError:      r.LastError,
		CreatedAt:      r.CreatedAt,
		UpdatedAt:      r.UpdatedAt,
	}
	switch r.Kind {
	case model.KindDNS:
		d.RecordType = string(r.RecordType)
		d.Content = r.Content
		d.Proxied = r.Proxied
		d.TTL = r.TTL
	case model.KindTunnel:
		d.Service = r.Service
		d.Path = r.Path
	case model.KindAccess:
		d.AccessAppID = r.CFID
		d.AccessAppName = r.AppName
		d.AccessPolicyName = r.PolicyName
		d.AccessDecision = r.Decision
	}
	return d
}
