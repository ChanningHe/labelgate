package reconcile

import (
	"time"

	"github.com/channinghe/labelgate/internal/logchange"
)

// problemRepeatInterval is how often a problem that persists unchanged is
// logged again.
const problemRepeatInterval = logchange.DefaultRepeat

// Subjects of problems that are not tied to a resource or container.
const (
	subjectLocalDocker = "local Docker"
	subjectCycle       = "reconcile cycle"
)

// problem is one thing wrong in a cycle. Subject identifies it across
// cycles; Text is the full message shown to people and may change.
type problem struct {
	Subject string
	Text    string
}

// logProblems logs the problems that appeared, changed or were resolved
// since the previous cycle, plus the occasional reminder of persisting ones.
// A cycle that was not completed (complete == false) cannot resolve anything.
func (r *Reconciler) logProblems(now time.Time, issues []problem, complete bool) {
	active := make(map[string]string, len(issues))
	for _, p := range issues {
		active[p.Subject] = p.Text
	}
	update := r.problems.Update
	if !complete {
		update = r.problems.UpdatePartial
	}
	for _, ev := range update(now, active) {
		switch ev.Kind {
		case logchange.Raised:
			r.log.Warn("reconcile problem", "subject", ev.Key, "problem", ev.Text)
		case logchange.Repeated:
			r.log.Warn("reconcile problem", "subject", ev.Key, "problem", ev.Text, "still_present", true)
		case logchange.Resolved:
			r.log.Info("reconcile problem resolved", "subject", ev.Key, "problem", ev.Text)
		}
	}
}
