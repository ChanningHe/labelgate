package reconcile

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/model"
)

func TestSyncStatusListsEveryLabelProblem(t *testing.T) {
	h := newHarness(t)
	pl := model.ParsedLabels{Errors: []model.LabelError{
		{Key: "labelgate.dns.web.hostname", Message: "is required"},
		{Key: "labelgate.dns.web.type", Message: "unknown type"},
		{Key: "labelgate.tunnel.web.service", Message: "must be a URL"},
	}}
	h.src.set(ctr("bad", pl))

	st := h.cycle()

	if st.State != SyncError || st.ProblemCount != 3 || len(st.Problems) != 3 {
		t.Fatalf("status %+v", st)
	}
	for i, key := range []string{"labelgate.dns.web.hostname", "labelgate.dns.web.type", "labelgate.tunnel.web.service"} {
		p := st.Problems[i]
		if !strings.Contains(p.Subject, key) || !strings.Contains(p.Text, key) || !strings.Contains(p.Text, "container bad") {
			t.Errorf("problem %d = %+v, want %s", i, p, key)
		}
	}
	if !strings.Contains(st.Error, "3 problems (listed under Sync problems on the dashboard overview and in the labelgate log); first: ") {
		t.Errorf("error %q", st.Error)
	}
	if strings.Contains(st.Error, "resource list") {
		t.Errorf("error still points to the resource list: %q", st.Error)
	}
}

func TestSyncStatusBoundsProblems(t *testing.T) {
	issues := make([]problem, MaxStatusProblems+5)
	for i := range issues {
		issues[i] = problem{Subject: fmt.Sprint("s", i), Text: fmt.Sprint("t", i)}
	}
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	st := syncStatus(now, issues)

	if len(st.Problems) != MaxStatusProblems || st.ProblemCount != MaxStatusProblems+5 {
		t.Fatalf("len %d count %d", len(st.Problems), st.ProblemCount)
	}
	if st.Problems[0].Text != "t0" || !st.LastSync.Equal(now) {
		t.Errorf("status %+v", st.Problems[0])
	}
	issues[0].Text = "mutated"
	if st.Problems[0].Text != "t0" {
		t.Error("status shares the caller's slice")
	}
}

func TestSyncStatusSingleProblemAndSuccess(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	st := syncStatus(now, []problem{{Subject: "s", Text: "only"}})
	if st.State != SyncError || st.Error != "only" || st.ProblemCount != 1 || len(st.Problems) != 1 {
		t.Fatalf("single: %+v", st)
	}
	if st := syncStatus(now, nil); st.State != SyncSuccess || st.Error != "" || st.Problems != nil || st.ProblemCount != 0 {
		t.Fatalf("success: %+v", st)
	}
}
