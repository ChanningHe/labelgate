package agent

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"regexp"

	"github.com/channinghe/labelgate/internal/model"
)

const (
	maxVersionLen  = 64
	maxLabelKeyLen = 1024
)

// containerName follows Docker's container name grammar (no leading slash).
var containerName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,254}$`)

// validateReport checks an agent report; a non-empty code rejects it.
func validateReport(r Report) (ErrorCode, string) {
	if len(r.Containers) > MaxContainers {
		return ErrTooLarge, fmt.Sprintf("too many containers (max %d)", MaxContainers)
	}
	if r.PublicIP != "" {
		if a, err := netip.ParseAddr(r.PublicIP); err != nil || !a.Is4() {
			return ErrBadRequest, "public_ip is not an IPv4 address"
		}
	}
	if r.PublicIPv6 != "" {
		if a, err := netip.ParseAddr(r.PublicIPv6); err != nil || !a.Is6() || a.Is4In6() {
			return ErrBadRequest, "public_ipv6 is not an IPv6 address"
		}
	}
	if !r.DockerOK {
		return "", "" // containers are ignored
	}
	seen := make(map[string]struct{}, len(r.Containers))
	for _, c := range r.Containers {
		if !containerName.MatchString(c.Name) {
			return ErrBadRequest, "invalid container name"
		}
		if _, dup := seen[c.Name]; dup {
			return ErrBadRequest, "duplicate container name"
		}
		seen[c.Name] = struct{}{}
		for k := range c.Labels {
			if k == "" || len(k) > maxLabelKeyLen {
				return ErrBadRequest, "invalid label key"
			}
		}
	}
	return "", ""
}

// handleReport validates and stores a report, acks it and triggers OnChange.
// It returns false when the session must end.
func (h *Hub) handleReport(ctx context.Context, s *hubSession, env Envelope, log *slog.Logger) bool {
	var rep Report
	if err := decodePayload(env, &rep); err != nil {
		s.conn.fail(ErrBadRequest, "malformed report")
		return false
	}
	if code, msg := validateReport(rep); code != "" {
		log.Warn("rejected agent report", "code", string(code), "reason", msg)
		s.conn.fail(code, msg)
		return false
	}
	now := h.now() // server clock only; agents never supply timestamps
	entry := h.opts.Server.Agents[s.id]
	rec := model.AgentRecord{
		ID:            s.id,
		Name:          entry.Name,
		DefaultTunnel: entry.DefaultTunnel,
		PublicIP:      rep.PublicIP,
		PublicIPv6:    rep.PublicIPv6,
		LastSeen:      now,
	}
	var snap *model.Snapshot
	if rep.DockerOK {
		snap = &model.Snapshot{
			AgentID: s.id, Complete: true, Containers: rep.Containers,
			PublicIP: rep.PublicIP, PublicIPv6: rep.PublicIPv6, TakenAt: now,
		}
	} else {
		log.Warn("agent cannot list its containers; keeping its previous containers until it can (fix Docker access on the agent host)",
			"docker_error", truncate(rep.DockerError, maxDockerErrorLen))
	}
	changed, err := h.opts.Store.RecordAgentReport(ctx, rec, snap)
	if err != nil {
		log.Error("store agent report", "error", err) // not acked; the next report retries
		return true
	}
	if err := s.conn.send(MsgAck, Ack{Seq: rep.Seq}); err != nil {
		return false
	}
	if changed || !s.reported {
		h.opts.OnChange()
	}
	s.reported = true
	return true
}
