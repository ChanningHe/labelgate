package agent

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/channinghe/labelgate/internal/logchange"
)

// ipHint tells what the public IP is for and how to fix a failing lookup.
const ipHint = "records with target auto need it; check this host's outbound internet access or set the record's target to an IP"

// ipv6Note qualifies the IPv6 case: many hosts legitimately have no IPv6.
const ipv6Note = " (ignore this if the host has no IPv6 and no AAAA records use target auto)"

// logIPProblems logs public IP detection failures when they start or change
// and recoveries once, instead of on every heartbeat. A lookup cut short by
// shutdown says nothing about the network and is ignored.
func (a *Agent) logIPProblems(ctx context.Context, log *slog.Logger, err4, err6 error) {
	if ctx.Err() != nil {
		return
	}
	active := map[string]string{}
	if err4 != nil {
		active[ipFamily(false)] = err4.Error()
	}
	if err6 != nil {
		active[ipFamily(true)] = err6.Error()
	}
	a.ipMu.Lock()
	events := a.ipProblems.Update(a.opts.Now(), active)
	a.ipMu.Unlock()
	for _, ev := range events {
		if ev.Kind == logchange.Resolved {
			log.Info("public " + ev.Key + " detection recovered")
			continue
		}
		msg := fmt.Sprintf("cannot detect this host's public %s address: %s: %s", ev.Key, ipHint, ev.Text)
		if ev.Key == ipFamily(true) {
			msg += ipv6Note
		}
		log.Warn(msg)
	}
}
