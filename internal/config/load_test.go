package config

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// env builds an injected environment lookup.
func env(kv ...string) envLookup {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const tokenEnv = "LABELGATE_CLOUDFLARE_API_TOKEN"

// mustLoad loads with an injected env and a single explicit file ("" = none).
func mustLoad(t *testing.T, file string, e envLookup) Config {
	t.Helper()
	cfg, err := load(file, e, nil)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return cfg
}

func wantValidationError(t *testing.T, err error, field, msgSub string) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want *ValidationError, got %T: %v", err, err)
	}
	if ve.Field != field || !strings.Contains(ve.Message, msgSub) {
		t.Fatalf("got field %q msg %q; want field %q msg containing %q", ve.Field, ve.Message, field, msgSub)
	}
}

func TestLoad_DefaultsFromEnvOnly(t *testing.T) {
	cfg := mustLoad(t, "", env(tokenEnv, "tok"))
	want := Defaults()
	want.Cloudflare.APIToken = "tok"
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("got %+v\nwant %+v", cfg, want)
	}
}

func TestLoad_NoCredentialFailsValidation(t *testing.T) {
	_, err := load("", env(), nil)
	wantValidationError(t, err, "cloudflare.api_token", "no Cloudflare API token is configured")
}

func TestLoad_FileFormats(t *testing.T) {
	files := map[string]string{
		"labelgate.yaml": "log_level: debug\ncloudflare:\n  api_token: tok\nsync:\n  remove_delay: 45m\n",
		"labelgate.yml":  "log_level: debug\ncloudflare:\n  api_token: tok\nsync:\n  remove_delay: 45m\n",
		"labelgate.json": `{"log_level":"debug","cloudflare":{"api_token":"tok"},"sync":{"remove_delay":"45m"}}`,
		"labelgate.toml": "log_level = \"debug\"\n[cloudflare]\napi_token = \"tok\"\n[sync]\nremove_delay = \"45m\"\n",
		"UPPER.YAML":     "log_level: debug\ncloudflare:\n  api_token: tok\nsync:\n  remove_delay: 45m\n",
	}
	for name, content := range files {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), name, content)
			cfg := mustLoad(t, path, env())
			if cfg.LogLevel != "debug" || cfg.Cloudflare.APIToken != "tok" || cfg.Sync.RemoveDelay != 45*time.Minute {
				t.Errorf("got %+v", cfg)
			}
			if cfg.LogFormat != "text" {
				t.Errorf("defaults must survive: %q", cfg.LogFormat)
			}
		})
	}
}

func TestLoad_UnsupportedExtensionAndBadSyntax(t *testing.T) {
	dir := t.TempDir()
	_, err := load(writeFile(t, dir, "labelgate.conf", "x"), env(), nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported extension") {
		t.Errorf("got %v", err)
	}
	_, err = load(writeFile(t, dir, "bad.yaml", "log_level: [unclosed"), env(), nil)
	if err == nil || !strings.Contains(err.Error(), "bad.yaml") {
		t.Errorf("syntax error must name the file, got %v", err)
	}
}

func TestLoad_FileResolution(t *testing.T) {
	const body = "cloudflare:\n  api_token: tok\nlabel_prefix: "
	dirA, dirB := t.TempDir(), t.TempDir()
	writeFile(t, dirA, "labelgate.yaml", body+"froma\n")
	writeFile(t, dirA, "labelgate.toml", "label_prefix = \"fromatoml\"\n")
	writeFile(t, dirB, "labelgate.json", `{"cloudflare":{"api_token":"tok"},"label_prefix":"fromb"}`)
	explicit := writeFile(t, t.TempDir(), "x.yaml", body+"explicit\n")
	viaEnv := writeFile(t, t.TempDir(), "e.yaml", body+"viaenv\n")

	tests := []struct {
		name   string
		path   string
		env    envLookup
		dirs   []string
		want   string
		wantEr string
	}{
		{"first dir wins, yaml before toml", "", env(), []string{dirA, dirB}, "froma", ""},
		{"second dir used when first empty", "", env(), []string{t.TempDir(), dirB}, "fromb", ""},
		{"explicit beats env and search", explicit, env(ConfigEnv, viaEnv), []string{dirA}, "explicit", ""},
		{"env beats search", "", env(ConfigEnv, viaEnv), []string{dirA}, "viaenv", ""},
		{"explicit missing is an error", filepath.Join(dirA, "nope.yaml"), env(), []string{dirA}, "", "no such file"},
		{"env missing is an error", "", env(ConfigEnv, filepath.Join(dirA, "nope.yaml")), []string{dirA}, "", "no such file"},
		{"empty env ignored", "", env(ConfigEnv, "", tokenEnv, "tok"), []string{t.TempDir()}, "labelgate", ""},
		{"no file anywhere is fine", "", env(tokenEnv, "tok"), []string{t.TempDir(), "/nonexistent-dir"}, "labelgate", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := load(tt.path, tt.env, tt.dirs)
			if tt.wantEr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantEr) {
					t.Fatalf("want error %q, got %v", tt.wantEr, err)
				}
				return
			}
			if err != nil || cfg.LabelPrefix != tt.want {
				t.Fatalf("got %q, %v; want %q", cfg.LabelPrefix, err, tt.want)
			}
		})
	}
}

func TestLoad_DirectoryNamedLikeConfigIsSkipped(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "labelgate.yaml"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := load("", env(tokenEnv, "t"), []string{dir}); err != nil {
		t.Fatalf("a directory must not be treated as a config file: %v", err)
	}
}

func TestLoad_EnvBeatsFileBeatsDefaults(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", `
log_level: warn
log_format: json
cloudflare:
  api_token: file-token
sync:
  interval: 7m
`)
	cfg := mustLoad(t, path, env("LABELGATE_LOG_LEVEL", "error", "LABELGATE_SYNC_REMOVE_DELAY", "1h"))
	if cfg.LogLevel != "error" {
		t.Errorf("env must beat file: %q", cfg.LogLevel)
	}
	if cfg.LogFormat != "json" || cfg.Sync.Interval != 7*time.Minute || cfg.Cloudflare.APIToken != "file-token" {
		t.Errorf("file values lost: %+v", cfg)
	}
	if cfg.Sync.RemoveDelay != time.Hour {
		t.Errorf("env duration: %v", cfg.Sync.RemoveDelay)
	}
	if cfg.Sync.AgentGrace != 24*time.Hour {
		t.Errorf("default lost: %v", cfg.Sync.AgentGrace)
	}
}

func TestLoad_EmptyEnvValueIsIgnored(t *testing.T) {
	cfg := mustLoad(t, "", env(tokenEnv, "tok", "LABELGATE_LOG_LEVEL", "", "LABELGATE_SYNC_INTERVAL", ""))
	if cfg.LogLevel != "info" || cfg.Sync.Interval != 5*time.Minute {
		t.Errorf("got %+v", cfg)
	}
}

func TestLoad_EmptyRemovedKeyEnvValueIsIgnored(t *testing.T) {
	e := env(tokenEnv, "tok")
	for key := range RemovedKeys {
		base := e
		name := envName(key)
		e = func(k string) (string, bool) {
			if k == name {
				return "", true
			}
			return base(k)
		}
	}
	if _, err := load("", e, nil); err != nil {
		t.Fatalf("empty removed-key env vars must count as unset: %v", err)
	}
}

func TestLoad_CloudflareFlatEnvCoexistsWithFileMaps(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", `
cloudflare:
  credentials:
    corp:
      api_token: corp-token
      zones: ["corp.io", "*.corp.io"]
  tunnels:
    sec:
      account_id: acct2
      tunnel_id: tun2
      credential: corp
`)
	cfg := mustLoad(t, path, env(
		"LABELGATE_CLOUDFLARE_API_TOKEN", "root",
		"LABELGATE_CLOUDFLARE_ACCOUNT_ID", "acct",
		"LABELGATE_CLOUDFLARE_TUNNEL_ID", "tun",
	))
	cf := cfg.Cloudflare
	if cf.APIToken != "root" || cf.AccountID != "acct" || cf.TunnelID != "tun" {
		t.Errorf("flat env values: %+v", cf)
	}
	if cf.Credentials["corp"].APIToken != "corp-token" || len(cf.Credentials["corp"].Zones) != 2 {
		t.Errorf("credentials: %+v", cf.Credentials)
	}
	if cf.Tunnels["sec"] != (TunnelConfig{AccountID: "acct2", TunnelID: "tun2", Credential: "corp"}) {
		t.Errorf("tunnels: %+v", cf.Tunnels)
	}
}

func TestLoad_AgentSection(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", `
cloudflare:
  api_token: tok
  tunnel_id: t1
  account_id: a1
agent:
  enabled: true
  accept_token: "s0s1s2s3s4s5s6s7s8s9sasbscsdsesf"
  accept_hostnames: ["*.example.com"]
  agents:
    web-1:
      token: "x0x1x2x3x4x5x6x7x8x9xaxbxcxdxexf"
      name: Web One
      default_tunnel: default
      tunnels: [default]
      hostnames: ["web.example.com"]
      services: ["web", "10.0.0.0/8"]
      connect_to: wss://web-1:28113/ws
`)
	cfg := mustLoad(t, path, env("LABELGATE_AGENT_ACCEPT_TUNNELS", "default, t1 ,", "LABELGATE_AGENT_ACCEPT_SERVICES", "web, 10.0.0.0/8"))
	a := cfg.Agent
	if !a.Enabled || a.AcceptToken != "s0s1s2s3s4s5s6s7s8s9sasbscsdsesf" || !reflect.DeepEqual(a.AcceptHostnames, []string{"*.example.com"}) {
		t.Errorf("agent: %+v", a)
	}
	if !reflect.DeepEqual(a.AcceptTunnels, []string{"default", "t1"}) {
		t.Errorf("list env must be split and trimmed: %q", a.AcceptTunnels)
	}
	if !reflect.DeepEqual(a.AcceptServices, []string{"web", "10.0.0.0/8"}) {
		t.Errorf("accept_services from env: %q", a.AcceptServices)
	}
	e := a.Agents["web-1"]
	want := AgentEntry{
		Token: "x0x1x2x3x4x5x6x7x8x9xaxbxcxdxexf", Name: "Web One", DefaultTunnel: "default", Tunnels: []string{"default"},
		Hostnames: []string{"web.example.com"}, Services: []string{"web", "10.0.0.0/8"}, ConnectTo: "wss://web-1:28113/ws",
	}
	if !reflect.DeepEqual(e, want) {
		t.Errorf("entry: %+v", e)
	}
}

// Agent IDs may contain dots; as map keys they must not be split into paths.
func TestLoad_DottedAgentIDs(t *testing.T) {
	const tok = "x0x1x2x3x4x5x6x7x8x9xaxbxcxdxexf"
	files := map[string]string{
		"labelgate.yaml": "agent:\n  enabled: true\n  agents:\n    nas.lan:\n      token: \"" + tok + "\"\n      hostnames: [\"*.lan.example.com\"]\n    web-1:\n      token: \"" + tok + "\"\n",
		"labelgate.toml": "[agent]\nenabled = true\n[agent.agents.\"nas.lan\"]\ntoken = \"" + tok + "\"\nhostnames = [\"*.lan.example.com\"]\n[agent.agents.web-1]\ntoken = \"" + tok + "\"\n",
		"labelgate.json": `{"agent":{"enabled":true,"agents":{"nas.lan":{"token":"` + tok + `","hostnames":["*.lan.example.com"]},"web-1":{"token":"` + tok + `"}}}}`,
	}
	for name, content := range files {
		t.Run(name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), name, content)
			cfg := mustLoad(t, path, env(tokenEnv, "tok", "LABELGATE_AGENT_LISTEN", ":9091", "LABELGATE_SYNC_REMOVE_DELAY", "1m"))
			e, ok := cfg.Agent.Agents["nas.lan"]
			if !ok || e.Token != tok || !reflect.DeepEqual(e.Hostnames, []string{"*.lan.example.com"}) || len(cfg.Agent.Agents) != 2 {
				t.Fatalf("agents %+v", cfg.Agent.Agents)
			}
			if cfg.Agent.Listen != ":9091" || cfg.Sync.RemoveDelay != time.Minute {
				t.Fatalf("env overlay lost: listen %q remove_delay %s", cfg.Agent.Listen, cfg.Sync.RemoveDelay)
			}
		})
	}
}

func TestLoad_AgentModeFromEnv(t *testing.T) {
	cfg := mustLoad(t, "", env(
		"LABELGATE_MODE", "agent",
		"LABELGATE_CONNECT_TOKEN", "t0t1t2t3t4t5t6t7t8t9tatbtctdtetf",
		"LABELGATE_CONNECT_ENDPOINT", "wss://main/ws",
		"LABELGATE_CONNECT_HEARTBEAT_INTERVAL", "45s",
		"LABELGATE_CONNECT_TLS_CA", "/ca.pem",
		"LABELGATE_CONNECT_TLS_INSECURE_SKIP_VERIFY", "true",
	))
	if cfg.Mode != ModeAgent || cfg.Connect.HeartbeatInterval != 45*time.Second ||
		cfg.Connect.TLS.CA != "/ca.pem" || !cfg.Connect.TLS.InsecureSkipVerify {
		t.Errorf("got %+v", cfg.Connect)
	}
}

// fieldByKey resolves a dotted mapstructure path in cfg.
func fieldByKey(cfg Config, key string) reflect.Value {
	v := reflect.ValueOf(cfg)
	for _, part := range strings.Split(key, ".") {
		t := v.Type()
		for i := 0; i < t.NumField(); i++ {
			if tag, _, _ := strings.Cut(t.Field(i).Tag.Get("mapstructure"), ","); tag == part {
				v = v.Field(i)
				break
			}
		}
	}
	return v
}

// sample returns an env string and the value it must decode to, by field type.
func sample(t reflect.Type) (string, any) {
	switch {
	case t == durationType:
		return "7s", 7 * time.Second
	case t.Kind() == reflect.Bool:
		return "true", true
	case t.Kind() == reflect.Int:
		return "7", 7
	case t.Kind() == reflect.Float64:
		return "2.5", 2.5
	case t.Kind() == reflect.Slice:
		return "a, b", []string{"a", "b"}
	}
	return "x", "x"
}

// Every scalar/list key must be bound to its LABELGATE_* variable.
func TestLoad_EveryKeyHasEnvBinding(t *testing.T) {
	keys := leafKeys(reflect.TypeFor[Config](), "")
	if len(keys) < 40 {
		t.Fatalf("suspiciously few keys: %d", len(keys))
	}
	seen := map[string]string{}
	for _, key := range keys {
		name := envName(key)
		if other, dup := seen[name]; dup {
			t.Fatalf("env name %s shared by %s and %s", name, other, key)
		}
		seen[name] = key
		t.Run(key, func(t *testing.T) {
			ft := fieldByKey(Defaults(), key).Type()
			raw, want := sample(ft)
			cfg, unknown, failures := decode(newViper(), env(name, raw))
			if len(unknown)+len(failures) != 0 {
				t.Fatalf("decode %s=%s: %v %v", name, raw, unknown, failures)
			}
			got := fieldByKey(cfg, key).Interface()
			if ft.Kind() == reflect.String && ft != reflect.TypeFor[string]() {
				got = reflect.ValueOf(got).String() // Mode / ConnectMode
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("%s=%s decoded to %#v, want %#v", name, raw, got, want)
			}
		})
	}
	if _, ok := seen["LABELGATE_CLOUDFLARE_API_TOKEN"]; !ok {
		t.Error("cloudflare.api_token must be bound")
	}
	for _, mapKey := range []string{"cloudflare.credentials", "cloudflare.tunnels", "agent.agents"} {
		if _, ok := seen[envName(mapKey)]; ok {
			t.Errorf("%s is file-only and must not have an env binding", mapKey)
		}
	}
}

func TestLoad_UnknownKeys(t *testing.T) {
	tests := []struct {
		name, yaml, field string
	}{
		{"top level typo", "cloudflare:\n  api_token: t\nlog_levl: debug\n", "log_levl"},
		{"nested typo", "cloudflare:\n  api_token: t\n  api_tokenn: x\n", "cloudflare.api_tokenn"},
		{"unknown section", "cloudflare:\n  api_token: t\nwebhook:\n  url: x\n", "webhook"},
		{"typo inside map entry", "cloudflare:\n  api_token: t\n  credentials:\n    c:\n      api_tokn: x\n", "cloudflare.credentials.c.api_tokn"},
		{"typo in agent entry", "cloudflare:\n  api_token: t\nagent:\n  agents:\n    a:\n      tokn: x\n", "agent.agents.a.tokn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "c.yaml", tt.yaml)
			_, err := load(path, env(), nil)
			wantValidationError(t, err, tt.field, "unknown key")
		})
	}
}

func TestLoad_RemovedKeys(t *testing.T) {
	files := map[string]string{
		"docker.ssh.host":           "docker:\n  ssh:\n    host: user@h\n",
		"docker.ssh.key":            "docker:\n  ssh:\n    key: /k\n",
		"docker.ssh.key_passphrase": "docker:\n  ssh:\n    key_passphrase: p\n",
		"docker.ssh.known_hosts":    "docker:\n  ssh:\n    known_hosts: /h\n",
		"db.retention":              "db:\n  retention: 720h\n",
		"db.vacuum_interval":        "db:\n  vacuum_interval: 24h\n",
	}
	if len(files) != len(RemovedKeys) {
		t.Fatalf("test must cover every RemovedKeys entry (%d vs %d)", len(files), len(RemovedKeys))
	}
	for key, body := range files {
		t.Run("file "+key, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\n"+body)
			_, err := load(path, env(), nil)
			wantValidationError(t, err, key, "")
			if !strings.Contains(err.Error(), RemovedKeys[key]) {
				t.Errorf("message must carry the removal reason: %v", err)
			}
		})
		t.Run("env "+key, func(t *testing.T) {
			_, err := load("", env(tokenEnv, "t", envName(key), "x"), nil)
			wantValidationError(t, err, key, "")
		})
	}
	t.Run("empty ssh section", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\ndocker:\n  ssh: {}\n")
		_, err := load(path, env(), nil)
		wantValidationError(t, err, "docker.ssh.host", "ssh://")
	})
	t.Run("sibling keys in a kept section are fine", func(t *testing.T) {
		path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\ndb:\n  path: /tmp/x.db\n")
		if _, err := load(path, env(), nil); err != nil {
			t.Fatal(err)
		}
	})
}

func TestLoad_DecodeErrors(t *testing.T) {
	tests := []struct {
		name, yaml string
		env        envLookup
		field      string
		msg        string
	}{
		{"int type error", "retry:\n  attempts: abc\n", env(), "retry.attempts", "cannot parse"},
		{"bare number duration", "sync:\n  interval: 30\n", env(), "sync.interval", "must be a string"},
		{"bad duration string", "", env("LABELGATE_SYNC_INTERVAL", "soon"), "sync.interval", "invalid duration"},
		{"duration env without unit", "", env("LABELGATE_RETRY_DELAY", "5"), "retry.delay", "invalid duration"},
		{"bad bool env", "", env("LABELGATE_API_ENABLED", "perhaps"), "api.enabled", ""},
		{"bad float env", "", env("LABELGATE_RETRY_BACKOFF", "fast"), "retry.backoff", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := ""
			if tt.yaml != "" {
				path = writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\n"+tt.yaml)
			}
			e := tt.env
			if path == "" {
				base := e
				e = func(k string) (string, bool) {
					if k == tokenEnv {
						return "t", true
					}
					return base(k)
				}
			}
			_, err := load(path, e, nil)
			wantValidationError(t, err, tt.field, tt.msg)
		})
	}
}

func TestLoad_ReturnsValidateErrorsUnchanged(t *testing.T) {
	tests := []struct{ env, val, field string }{
		{"LABELGATE_MODE", "worker", "mode"},
		{"LABELGATE_LOG_LEVEL", "loud", "log_level"},
		{"LABELGATE_LOG_FORMAT", "yaml", "log_format"},
		{"LABELGATE_SYNC_INTERVAL", "1s", "sync.interval"},
		{"LABELGATE_RETRY_BACKOFF", "0.5", "retry.backoff"},
		{"LABELGATE_RETRY_ATTEMPTS", "0", "retry.attempts"},
		{"LABELGATE_DOCKER_ENDPOINT", "ssh://host", "docker.endpoint"},
		{"LABELGATE_API_BASE_PATH", "api", "api.base_path"},
	}
	for _, tt := range tests {
		t.Run(tt.env, func(t *testing.T) {
			_, err := load("", env(tokenEnv, "t", tt.env, tt.val), nil)
			wantValidationError(t, err, tt.field, "")
		})
	}
}

func TestLoad_NamedAgentEmptyTokenRejected(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\nagent:\n  agents:\n    web:\n      name: x\n")
	_, err := load(path, env(), nil)
	wantValidationError(t, err, "agent.agents.web.token", "must not be empty")
}

func TestLoad_ParallelLoadsAreIsolated(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prefix := "p" + string(rune('a'+i))
			cfg, err := load("", env(tokenEnv, "tok-"+prefix, "LABELGATE_LABEL_PREFIX", prefix), nil)
			if err != nil || cfg.LabelPrefix != prefix || cfg.Cloudflare.APIToken != "tok-"+prefix {
				t.Errorf("load %d: %+v, %v", i, cfg, err)
			}
		}()
	}
	wg.Wait()
}

func TestLoad_PublicEntryPoint(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: file\n")
	t.Setenv("LABELGATE_LOG_FORMAT", "json")
	t.Setenv("LABELGATE_CLOUDFLARE_ACCOUNT_ID", "acct")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogFormat != "json" || cfg.Cloudflare.APIToken != "file" || cfg.Cloudflare.AccountID != "acct" {
		t.Errorf("got %+v", cfg)
	}
	if got := os.Getenv("LABELGATE_CLOUDFLARE_API_TOKEN"); got != "" {
		t.Errorf("Load must not set env, found %q", got)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.yaml")); err == nil {
		t.Error("missing explicit file must fail")
	}
}
