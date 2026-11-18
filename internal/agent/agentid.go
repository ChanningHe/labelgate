package agent

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

// agentIDSource says where the agent ID came from.
type agentIDSource string

const (
	idExplicit      agentIDSource = "explicit"   // connect.agent_id
	idFromMachineID agentIDSource = "machine-id" // /etc/machine-id
	idFromHostname  agentIDSource = "hostname"   // container hostname: not stable
)

const (
	machineIDPath   = "/etc/machine-id"
	machineIDPrefix = 12
	maxAgentIDLen   = 63
)

// resolveAgentID returns the explicit ID if set (it must be valid as given),
// else the first 12 hex chars of the machine ID, else the sanitized hostname.
// The source reports which of the three was used.
func resolveAgentID(explicit string, machineID, hostname func() (string, error)) (string, agentIDSource, error) {
	if explicit != "" {
		if !model.ValidAgentID(explicit) {
			return "", "", fmt.Errorf("agent: connect.agent_id %q is invalid: use up to 63 lower-case letters, digits, dots, hyphens or underscores, starting with a letter or digit", explicit)
		}
		return explicit, idExplicit, nil
	}
	if raw, err := machineID(); err == nil {
		id := strings.ToLower(strings.TrimSpace(raw))
		if len(id) >= machineIDPrefix && isHex(id[:machineIDPrefix]) {
			return id[:machineIDPrefix], idFromMachineID, nil
		}
	}
	if raw, err := hostname(); err == nil {
		if id := sanitizeAgentID(raw); model.ValidAgentID(id) {
			return id, idFromHostname, nil
		}
	}
	return "", "", errors.New("agent: cannot derive an agent ID from /etc/machine-id or the hostname; set connect.agent_id (env LABELGATE_CONNECT_AGENT_ID)")
}

// logAgentID logs the startup line with the resolved agent ID, where it came
// from and the connect mode, and warns when the ID is the
// hostname: a container hostname changes on every recreate.
func logAgentID(log *slog.Logger, id string, src agentIDSource, mode config.ConnectMode) {
	log.Info("Agent mode", "agent_id", id, "agent_id_source", string(src), "connect_mode", string(mode))
	if src != idFromHostname {
		return
	}
	log.Warn(fmt.Sprintf("agent ID %s comes from the container hostname, which changes when the container is recreated; "+
		"the main instance then sees a new agent and removes the old agent's resources after %s. "+
		"Fix: mount /etc/machine-id:/etc/machine-id:ro into the agent container, or set %s, which takes priority",
		id, config.KeyWithEnv("sync.agent_grace"), config.KeyWithEnv("connect.agent_id")))
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdef", r) {
			return false
		}
	}
	return true
}

// sanitizeAgentID lower-cases s, maps invalid characters to '-', strips
// leading separators and truncates to the maximum length.
func sanitizeAgentID(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := strings.TrimLeft(b.String(), "-_.")
	return truncate(id, maxAgentIDLen)
}

func readMachineID() (string, error) {
	b, err := os.ReadFile(machineIDPath)
	return string(b), err
}
