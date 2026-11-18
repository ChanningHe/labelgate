package agent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/channinghe/labelgate/internal/config"
)

// Config sections whose TLS and listen keys an error message points at.
const (
	sectionAgent   = "agent"   // main instance: agent.*
	sectionConnect = "connect" // agent: connect.*
)

// dialTarget names the config keys behind one outgoing connection, for hints.
type dialTarget struct {
	url     string // the dialed URL; its scheme and path refine the hints
	urlKey  string // e.g. "connect.endpoint (env LABELGATE_CONNECT_ENDPOINT)"
	section string // TLS settings: sectionAgent or sectionConnect
	peer    string // "the main instance" or "the agent"
}

// statusError is a WebSocket upgrade the peer answered with a non-101 status.
type statusError struct {
	err    error
	status int
}

func (e *statusError) Error() string { return fmt.Sprintf("%v (HTTP %d)", e.err, e.status) }
func (e *statusError) Unwrap() error { return e.err }

// withHint appends "; hint" to err; an empty hint returns err unchanged.
func withHint(err error, hint string) error {
	if hint == "" {
		return err
	}
	return fmt.Errorf("%w; %s", err, hint)
}

// explainDial appends how to fix a failed dial, when the cause is known.
func explainDial(err error, t dialTarget) error { return withHint(err, dialHint(err, t)) }

// explainAuth appends, on the main instance, how to fix a failed agent
// authentication; other handshake errors are returned unchanged.
func (h *Hub) explainAuth(err error, mode Mode) error {
	var f *authFailure
	if !errors.As(err, &f) {
		return err
	}
	_, named := h.opts.Server.Agents[f.agentID]
	return withHint(err, mainAuthHint(f, mode, named))
}

// dialHint returns how to fix a failed dial, or "".
func dialHint(err error, t dialTarget) string {
	msg := err.Error()
	var se *statusError
	switch {
	case isUntrustedCert(err):
		return fmt.Sprintf("%s's TLS certificate is not trusted: set %s to the CA that signed it, or set %s=true to skip the check (not recommended)",
			t.peer, config.KeyWithEnv(t.section+".tls.ca"), config.KeyWithEnv(t.section+".tls.insecure_skip_verify"))
	case isHostnameMismatch(err):
		return fmt.Sprintf("%s's TLS certificate does not list the host in %s: use a host name the certificate is issued for", t.peer, t.urlKey)
	case errors.As(err, &se):
		return statusHint(se.status, t)
	case strings.Contains(msg, "first record does not look like a TLS handshake"):
		return fmt.Sprintf("%s does not use TLS: use ws:// in %s, or give it a certificate (%s.tls.cert and %s.tls.key)",
			t.peer, t.urlKey, peerSection(t.section), peerSection(t.section))
	case strings.Contains(msg, "malformed HTTP response") && t.isPlain():
		return expectsTLSHint(t)
	case strings.Contains(msg, "connection refused"):
		return fmt.Sprintf("nothing listens there: check the host and port in %s, that %s is running and that the port is published%s",
			t.urlKey, t.peer, enabledHint(t.section))
	case strings.Contains(msg, "no such host"):
		return fmt.Sprintf("the host name in %s does not resolve", t.urlKey)
	}
	return ""
}

// statusHint explains an HTTP status answering the WebSocket upgrade.
func statusHint(status int, t dialTarget) string {
	switch {
	case status == http.StatusBadRequest && t.isPlain():
		// Go's TLS server and nginx answer a plain request on a TLS port with 400.
		return expectsTLSHint(t)
	case status == http.StatusNotFound && !strings.HasSuffix(t.path(), PathWS):
		return fmt.Sprintf("nothing serves agent connections at that path: %s must end in %s", t.urlKey, PathWS)
	case status == http.StatusNotFound:
		return notListenerHint(t)
	case status == http.StatusBadGateway || status == http.StatusGatewayTimeout:
		return proxyHint(t)
	case status == http.StatusServiceUnavailable:
		return proxyHint(t) + fmt.Sprintf(", or %s has too many pending connections", t.peer)
	}
	return fmt.Sprintf("the address does not belong to labelgate's agent port: check the host, port and path in %s", t.urlKey)
}

func expectsTLSHint(t dialTarget) string {
	return fmt.Sprintf("%s likely expects TLS: use wss:// in %s", t.peer, t.urlKey)
}

func proxyHint(t dialTarget) string {
	return fmt.Sprintf("the reverse proxy in front of %s cannot reach it: check that %s is running and the proxy's upstream address", t.peer, t.peer)
}

// notListenerHint explains a 404 on the right path: another HTTP server,
// typically labelgate's own dashboard/API port, answered.
func notListenerHint(t dialTarget) string {
	if t.section == sectionConnect {
		return fmt.Sprintf("this port is not labelgate's agent listener (it may be the main instance's dashboard/API port, default 28111): "+
			"use the port of %s on the main instance (default 28112) in %s", config.KeyWithEnv("agent.listen"), t.urlKey)
	}
	return fmt.Sprintf("this port is not labelgate's agent listener: use the port of %s on the agent in %s",
		config.KeyWithEnv("connect.listen"), t.urlKey)
}

// isPlain reports whether the target URL uses ws:// (no TLS).
func (t dialTarget) isPlain() bool {
	u, err := url.Parse(t.url)
	return err == nil && u.Scheme == "ws"
}

// path is the path of the target URL, or "" if it cannot be parsed.
func (t dialTarget) path() string {
	u, err := url.Parse(t.url)
	if err != nil {
		return ""
	}
	return u.Path
}

// peerSection is the config section of the other side of a connection.
func peerSection(section string) string {
	if section == sectionConnect {
		return sectionAgent
	}
	return sectionConnect
}

// enabledHint reminds agents that the main instance only listens when enabled.
func enabledHint(section string) string {
	if section != sectionConnect {
		return ""
	}
	return " (the main instance needs agent.enabled=true, env LABELGATE_AGENT_ENABLED)"
}

func isUntrustedCert(err error) bool {
	var uerr x509.UnknownAuthorityError
	var verr *tls.CertificateVerificationError
	var herr x509.HostnameError
	return errors.As(err, &uerr) || (errors.As(err, &verr) && !errors.As(err, &herr))
}

func isHostnameMismatch(err error) bool {
	var herr x509.HostnameError
	return errors.As(err, &herr)
}

// agentAuthHint explains, on the agent, why the main instance could not be
// authenticated or refused this agent. Token values are never included.
func agentAuthHint(agentID string, err error) string {
	var pe *PeerError
	switch {
	case errors.As(err, &pe):
		return peerErrorHint(agentID, pe.Code)
	case errors.Is(err, errAuth):
		return tokenMismatchHint(agentID)
	}
	return ""
}

// peerErrorHint explains an error code the main instance sent to this agent.
func peerErrorHint(agentID string, code ErrorCode) string {
	switch code {
	case ErrDuplicateAgent:
		return fmt.Sprintf("another agent with ID %q is already connected: give each agent its own %s",
			agentID, config.KeyWithEnv("connect.agent_id"))
	case ErrUnsupportedVersion:
		return "the main instance runs an incompatible labelgate release: run the same release on both"
	case ErrUnauthorized:
		return tokenMismatchHint(agentID)
	}
	return ""
}

// tokenMismatchHint names the two keys that must hold the same token.
func tokenMismatchHint(agentID string) string {
	return fmt.Sprintf("tokens do not match: %s on this agent must equal agent.agents.%s.token in the main instance config "+
		"(or agent.accept_token there if %q is not listed under agent.agents)", config.KeyWithEnv("connect.token"), agentID, agentID)
}

// mainAuthHint explains, on the main instance, a failed agent authentication.
// named reports whether agent.agents.<id> exists. Token values are never included.
func mainAuthHint(f *authFailure, mode Mode, named bool) string {
	switch {
	case !f.known && mode == ModeInbound:
		return fmt.Sprintf("agent reports ID %q, which is not the agent.agents entry this connection was dialed for: "+
			"set %s on the agent to that entry's ID", f.agentID, config.KeyWithEnv("connect.agent_id"))
	case !f.known:
		return fmt.Sprintf("agent %q is not configured: add agent.agents.%s with a token to the main instance config, "+
			"or set %s to the agent's connect.token", f.agentID, f.agentID, config.KeyWithEnv("agent.accept_token"))
	}
	key := config.KeyWithEnv("agent.accept_token")
	if named {
		key = "agent.agents." + f.agentID + ".token"
	}
	what := "sent a wrong token proof"
	if f.cause != nil {
		what = "closed the connection during authentication, most likely because it rejected this instance's token"
	}
	return fmt.Sprintf("agent %q %s: connect.token on the agent must equal %s on this main instance", f.agentID, what, key)
}

// authFailure is a failed agent authentication as the main instance sees
// it, after the agent's hello was read.
type authFailure struct {
	agentID string
	known   bool  // a token for agentID is configured here
	cause   error // the read error when the agent hung up; nil = wrong MAC
}

func (e *authFailure) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("agent %q: %v", e.agentID, e.cause)
	}
	return fmt.Sprintf("agent %q: %v", e.agentID, errAuth)
}

func (e *authFailure) Unwrap() error {
	if e.cause != nil {
		return e.cause
	}
	return errAuth
}
