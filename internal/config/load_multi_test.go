package config

import (
	"errors"
	"strings"
	"testing"
)

// loadYAML loads body (prefixed with a root credential) with an empty env.
func loadYAML(t *testing.T, body string) error {
	t.Helper()
	path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\n"+body)
	_, err := load(path, env(), nil)
	return err
}

func TestLoad_ReportsEveryProblemInOneError(t *testing.T) {
	err := loadYAML(t, `
bogus: 1
log_level: loud
db:
  retention: 720h
docker:
  ssh:
    host: user@h
    key: /k
sync:
  foo: 1
retry:
  attempts: 0
`)

	wantFields(t, err,
		// removed keys, sorted
		"db.retention", "docker.ssh.host", "docker.ssh.key",
		// unknown keys, sorted
		"bogus", "sync.foo",
		// validation, in check order
		"log_level", "retry.attempts")
	msg := err.Error()
	if !strings.HasPrefix(msg, "config: 7 problems:\n  db.retention: ") {
		t.Errorf("unexpected header or order:\n%s", msg)
	}
	if strings.Count(msg, "\n") != 7 {
		t.Errorf("want one problem per line:\n%s", msg)
	}
}

func TestLoad_RemovedKeyIsNotAlsoUnknown(t *testing.T) {
	err := loadYAML(t, "db:\n  retention: 720h\n  vacuum_interval: 24h\n  bogus: 1\n")

	wantFields(t, err, "db.retention", "db.vacuum_interval", "db.bogus")
	if got := strings.Count(err.Error(), "unknown key"); got != 1 {
		t.Errorf("only db.bogus is unknown, got %d unknown-key lines:\n%v", got, err)
	}
}

func TestLoad_RemovedSectionIsNotAlsoUnknown(t *testing.T) {
	err := loadYAML(t, "docker:\n  ssh:\n    host: h\n")

	wantFields(t, err, "docker.ssh.host")
}

func TestLoad_BareRemovedSectionReportsEachRemovedKey(t *testing.T) {
	err := loadYAML(t, "docker:\n  ssh: {}\n")

	wantFields(t, err,
		"docker.ssh.host", "docker.ssh.key", "docker.ssh.key_passphrase", "docker.ssh.known_hosts")
}

func TestLoad_RemovedKeyInEnvAndFileIsReportedOnce(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", "cloudflare:\n  api_token: t\ndb:\n  retention: 1h\n")

	_, err := load(path, env(envName("db.retention"), "1h", envName("docker.ssh.key"), "x"), nil)

	wantFields(t, err, "db.retention", "docker.ssh.key")
}

func TestLoad_UnknownKeysAcrossSectionsAreAllReported(t *testing.T) {
	path := writeFile(t, t.TempDir(), "c.yaml", `
zeta: 1
sync:
  foo: 1
  bar: 2
cloudflare:
  api_token: t
  credentials:
    c:
      api_tokn: x
`)
	_, err := load(path, env(), nil)

	// The typo'd entry is still decoded, so validation sees it (no api_token).
	wantFields(t, err, "cloudflare.credentials.c.api_tokn", "sync.bar", "sync.foo", "zeta",
		"cloudflare.credentials.c.api_token")
}

func TestLoad_TypeErrorsAreAllReportedAndSkipValidation(t *testing.T) {
	err := loadYAML(t, `
bogus: 1
db:
  retention: 1h
log_level: loud
retry:
  attempts: abc
sync:
  interval: 30
`)

	// log_level is only a validation problem: the config is untrusted, so
	// Validate does not run.
	wantFields(t, err, "db.retention", "bogus", "retry.attempts", "sync.interval")
}

func TestLoad_UnknownKeysStillRunValidation(t *testing.T) {
	err := loadYAML(t, "bogus: 1\nlog_level: loud\n")

	wantFields(t, err, "bogus", "log_level")
}

func TestLoad_EnvDecodeErrorsAreAllReported(t *testing.T) {
	_, err := load("", env(tokenEnv, "t",
		"LABELGATE_SYNC_INTERVAL", "soon",
		"LABELGATE_API_ENABLED", "perhaps",
		"LABELGATE_RETRY_BACKOFF", "fast"), nil)

	wantFields(t, err, "api.enabled", "retry.backoff", "sync.interval")
}

func TestLoad_SingleProblemIsABareValidationError(t *testing.T) {
	for name, body := range map[string]string{
		"removed":    "db:\n  retention: 1h\n",
		"unknown":    "bogus: 1\n",
		"type error": "retry:\n  attempts: abc\n",
		"validation": "log_level: loud\n",
	} {
		t.Run(name, func(t *testing.T) {
			err := loadYAML(t, body)

			if _, ok := err.(*ValidationError); !ok {
				t.Fatalf("want bare *ValidationError, got %T: %v", err, err)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatal("errors.As must find the ValidationError")
			}
		})
	}
}

func TestLoad_SeveralProblemsStillUnwrapToValidationError(t *testing.T) {
	err := loadYAML(t, "bogus: 1\ndb:\n  retention: 1h\n")

	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Field != "db.retention" {
		t.Fatalf("errors.As must find the first problem, got %+v from %v", ve, err)
	}
}

func TestLoad_UnknownKeyInMapEntryDoesNotHideTheEntryFromValidation(t *testing.T) {
	err := loadYAML(t, `  tunnels:
    sec:
      account_id: a
      tunnel_id: t
      acount_id: typo
default_tunnel: sec
`)

	// default_tunnel resolves: the sec entry survives the unknown key.
	wantFields(t, err, "cloudflare.tunnels.sec.acount_id")
}

func TestDecodeProblems_NonDecodeErrorBecomesConfigProblem(t *testing.T) {
	unknown, failures := decodeProblems(errors.New("something odd"))

	if len(unknown) != 0 {
		t.Errorf("unknown = %v", unknown)
	}
	wantFields(t, failures.err(), "config")
	if failures[0].Message != "something odd" {
		t.Errorf("message = %q", failures[0].Message)
	}
}
