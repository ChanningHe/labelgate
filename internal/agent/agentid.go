package agent

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

const (
	machineIDPath   = "/etc/machine-id"
	machineIDPrefix = 12
	maxAgentIDLen   = 63
)

// resolveAgentID returns the explicit ID if set (it must be valid as given),
// else the first 12 hex chars of the machine ID, else the sanitized hostname.
func resolveAgentID(explicit string, machineID, hostname func() (string, error)) (string, error) {
	if explicit != "" {
		if !model.ValidAgentID(explicit) {
			return "", fmt.Errorf("agent: connect.agent_id %q is invalid (want ^[a-z0-9][a-z0-9_-]{0,62}$)", explicit)
		}
		return explicit, nil
	}
	if raw, err := machineID(); err == nil {
		id := strings.ToLower(strings.TrimSpace(raw))
		if len(id) >= machineIDPrefix && isHex(id[:machineIDPrefix]) {
			return id[:machineIDPrefix], nil
		}
	}
	if raw, err := hostname(); err == nil {
		if id := sanitizeAgentID(raw); model.ValidAgentID(id) {
			return id, nil
		}
	}
	return "", errors.New("agent: cannot derive agent id from machine-id or hostname; set connect.agent_id")
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
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	id := strings.TrimLeft(b.String(), "-_")
	return truncate(id, maxAgentIDLen)
}

func readMachineID() (string, error) {
	b, err := os.ReadFile(machineIDPath)
	return string(b), err
}
