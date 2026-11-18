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

// ipTrackers remember public IP detection failures per address family.
// IPv4 failures repeat hourly at WARN; IPv6 failures are logged at INFO and
// only when they start or change, because many hosts have no IPv6 at all.
type ipTrackers struct {
	v4, v6 *logchange.Tracker
}

func newIPTrackers() ipTrackers {
	return ipTrackers{v4: logchange.New(logchange.DefaultRepeat), v6: logchange.New(0)}
}

// logIPProblems logs public IP detection failures when they start or change
// and recoveries once, instead of on every heartbeat. A lookup cut short by
// shutdown says nothing about the network and is ignored.
func (a *Agent) logIPProblems(ctx context.Context, log *slog.Logger, err4, err6 error) {
	if ctx.Err() != nil {
		return
	}
	now := a.opts.Now()
	a.ipMu.Lock()
	ev4 := a.ipProblems.v4.Update(now, ipActive(false, err4))
	ev6 := a.ipProblems.v6.Update(now, ipActive(true, err6))
	a.ipMu.Unlock()
	logIPEvents(log, ev4, slog.LevelWarn, "")
	logIPEvents(log, ev6, slog.LevelInfo, ipv6Note)
}

// ipActive is the logchange input for one family: empty when err is nil.
func ipActive(v6 bool, err error) map[string]string {
	if err == nil {
		return nil
	}
	return map[string]string{ipFamily(v6): err.Error()}
}

// logIPEvents logs failures at level and recoveries at INFO.
func logIPEvents(log *slog.Logger, events []logchange.Event, level slog.Level, note string) {
	for _, ev := range events {
		if ev.Kind == logchange.Resolved {
			log.Info("public " + ev.Key + " detection recovered")
			continue
		}
		msg := fmt.Sprintf("cannot detect this host's public %s address: %s: %s%s", ev.Key, ipHint, ev.Text, note)
		log.Log(context.Background(), level, msg)
	}
}
