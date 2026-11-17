package agent

import (
	"bytes"
	"testing"
)

func TestMACBindsRoleModeAndNonces(t *testing.T) {
	na := bytes.Repeat([]byte{1}, NonceSize)
	nm := bytes.Repeat([]byte{2}, NonceSize)
	mac := MAC("secret", RoleMain, ModeOutbound, "host-1", na, nm)
	if !VerifyMAC("secret", RoleMain, ModeOutbound, "host-1", na, nm, mac) {
		t.Fatal("valid MAC rejected")
	}
	reject := map[string]bool{
		"token": VerifyMAC("other", RoleMain, ModeOutbound, "host-1", na, nm, mac),
		"role":  VerifyMAC("secret", RoleAgent, ModeOutbound, "host-1", na, nm, mac),
		"mode":  VerifyMAC("secret", RoleMain, ModeInbound, "host-1", na, nm, mac),
		"id":    VerifyMAC("secret", RoleMain, ModeOutbound, "host-2", na, nm, mac),
		"nonce": VerifyMAC("secret", RoleMain, ModeOutbound, "host-1", nm, na, mac),
		"short": VerifyMAC("secret", RoleMain, ModeOutbound, "host-1", na[:8], nm, mac),
		"empty": VerifyMAC("", RoleMain, ModeOutbound, "host-1", na, nm, MAC("", RoleMain, ModeOutbound, "host-1", na, nm)),
	}
	for name, ok := range reject {
		if ok {
			t.Errorf("MAC verified with wrong %s", name)
		}
	}
}
