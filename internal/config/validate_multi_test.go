package config

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fieldsOf returns the Field of every problem in err, in order.
func fieldsOf(t *testing.T, err error) []string {
	t.Helper()
	if err == nil {
		return nil
	}
	var multi ValidationErrors
	if errors.As(err, &multi) {
		fields := make([]string, len(multi))
		for i, ve := range multi {
			fields[i] = ve.Field
		}
		return fields
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError or ValidationErrors, got %T: %v", err, err)
	}
	return []string{ve.Field}
}

func wantFields(t *testing.T, err error, want ...string) {
	t.Helper()
	if got := fieldsOf(t, err); !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %v, want %v\nerror: %v", got, want, err)
	}
}

func TestValidate_ReportsEveryIndependentProblem(t *testing.T) {
	cfg := validMain()
	cfg.LabelPrefix = "Bad Prefix"
	cfg.LogLevel = "loud"
	cfg.Retry.Attempts = 0
	cfg.Docker.PollInterval = 0
	cfg.Sync.Interval = time.Second
	cfg.DB.Path = ""
	cfg.API.BasePath = "api"
	cfg.API.Address = "nope"

	err := Validate(cfg)

	wantFields(t, err,
		"label_prefix", "log_level", "retry.attempts", "docker.poll_interval",
		"sync.interval", "db.path", "api.address", "api.base_path")
}

func TestValidate_StopsAtFirstProblemOfOneField(t *testing.T) {
	cfg := validMain()
	cfg.API.BasePath = "/dashboard/" // both "must not end with /" and "reserved"
	cfg.Cloudflare.Credentials = map[string]CredentialConfig{
		"corp": {APIToken: agentToken, Zones: []string{"[bad", "a b"}},
	}

	wantFields(t, Validate(cfg), "api.base_path", "cloudflare.credentials.corp.zones")
}

func TestValidate_ReportsEveryEntryOfAMap(t *testing.T) {
	cfg := validMain()
	cfg.Agent.Agents = map[string]AgentEntry{"b": {}, "a": {}, "Bad ID": {Token: agentToken}}
	cfg.Cloudflare.Tunnels = map[string]TunnelConfig{"x": {}, "y": {AccountID: "a"}}

	wantFields(t, Validate(cfg),
		"cloudflare.tunnels.x.account_id", "cloudflare.tunnels.x.tunnel_id",
		"cloudflare.tunnels.y.tunnel_id",
		"agent.agents.Bad ID", "agent.agents.a.token", "agent.agents.b.token")
}

func TestValidate_MaxDelayCheckSkippedWhenDelayInvalid(t *testing.T) {
	cfg := validMain()
	cfg.Retry.Delay = 0
	cfg.Retry.MaxDelay = 0 // would be "< delay" only if delay were meaningful
	wantFields(t, Validate(cfg), "retry.delay")

	cfg.Retry.Delay = time.Minute
	cfg.Retry.MaxDelay = time.Second
	wantFields(t, Validate(cfg), "retry.max_delay")
}

func TestValidate_RootCredentialProblemIsReportedOnce(t *testing.T) {
	cfg := validMain()
	cfg.Cloudflare.APIToken = ""
	cfg.Cloudflare.Tunnels = map[string]TunnelConfig{"sec": {AccountID: "a", TunnelID: "t"}}

	// tunnel_id needs the root token; the "at least one credential" rule and
	// the per-tunnel root-credential rule would only repeat that.
	wantFields(t, Validate(cfg), "cloudflare.api_token")
}

func TestValidate_EmptyAgentTokenSkipsStrengthChecks(t *testing.T) {
	cfg := validMain()
	cfg.Agent.Agents = map[string]AgentEntry{"web": {Token: " "}}
	wantFields(t, Validate(cfg), "agent.agents.web.token")

	agent := validAgent()
	agent.Connect.Token = ""
	wantFields(t, Validate(agent), "connect.token")
}

func TestValidate_InvalidModeSkipsModeSpecificChecks(t *testing.T) {
	cfg := Defaults() // no credential: would fail in main mode
	cfg.Mode = "worker"
	wantFields(t, Validate(cfg), "mode")

	agent := validAgent()
	agent.Connect.Mode = "sideways"
	agent.Connect.Endpoint = ""
	wantFields(t, Validate(agent), "connect.mode")
}

func TestValidate_AgentModeReportsEveryProblem(t *testing.T) {
	cfg := validAgent()
	cfg.Connect.AgentID = "Bad_ID!"
	cfg.Connect.HeartbeatInterval = 0
	cfg.Connect.TLS.Key = "k"
	cfg.Connect.Endpoint = "https://main/ws"
	cfg.Docker.Endpoint = "http://x"

	wantFields(t, Validate(cfg),
		"docker.endpoint", "connect.agent_id", "connect.heartbeat_interval",
		"connect.tls.cert", "connect.endpoint")
}

func TestValidate_SingleProblemIsAValidationError(t *testing.T) {
	cfg := validMain()
	cfg.LogLevel = "loud"

	err := Validate(cfg)

	ve, ok := err.(*ValidationError)
	if !ok {
		t.Fatalf("single problem must be a bare *ValidationError, got %T", err)
	}
	if ve.Field != "log_level" {
		t.Errorf("field = %q", ve.Field)
	}
}

func TestValidate_SeveralProblemsUnwrapToEveryValidationError(t *testing.T) {
	cfg := validMain()
	cfg.LogLevel = "loud"
	cfg.LogFormat = "xml"

	err := Validate(cfg)

	var first *ValidationError
	if !errors.As(err, &first) || first.Field != "log_level" {
		t.Fatalf("errors.As must find the first problem, got %v", first)
	}
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != 2 {
		t.Fatalf("want Unwrap() []error with 2 entries, got %T", err)
	}
	for _, e := range joined.Unwrap() {
		if _, ok := e.(*ValidationError); !ok {
			t.Errorf("element is %T, want *ValidationError", e)
		}
	}
}

func TestValidationErrors_ErrorListsOneProblemPerLine(t *testing.T) {
	err := ValidationErrors{
		{Field: "a.b", Message: "bad"},
		{Field: "c", Message: "worse"},
	}
	want := "config: 2 problems:\n  a.b: bad\n  c: worse"
	if err.Error() != want {
		t.Errorf("got %q\nwant %q", err.Error(), want)
	}
	if strings.Count(err.Error(), "\n") != 2 {
		t.Error("expected one problem per line")
	}
}
