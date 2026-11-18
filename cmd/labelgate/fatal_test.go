package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/channinghe/labelgate/internal/config"
)

func TestFormatFatal(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"single line", errors.New("open store: disk full"), "labelgate: open store: disk full\n"},
		{
			"joined errors get one indented line each",
			errors.Join(errors.New(`credential "a": denied`), errors.New(`tunnel "b": not found`)),
			"labelgate: credential \"a\": denied\n  tunnel \"b\": not found\n",
		},
		{
			"already indented problems keep their indent",
			errors.New("config: 2 problems:\n  api.token: empty\n  mode: invalid"),
			"labelgate: config: 2 problems:\n  api.token: empty\n  mode: invalid\n",
		},
		{
			"wrapped multi-line error",
			errors.New("cloudflare credential validation failed: " + errors.Join(errors.New("x"), errors.New("y")).Error()),
			"labelgate: cloudflare credential validation failed: x\n  y\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatFatal(tt.err); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRunFatalStartupErrorPrintsOneProblemPerLine(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.LogFormat = "text"
	h.app.load = func(string) (config.Config, error) { return cfg, nil }
	h.cf.verifyErr = errors.Join(errors.New(`credential "default": token expired`), errors.New(`tunnel "web": not found`))
	var errOut syncBuffer

	if got := h.app.run(nil, &syncBuffer{}, &errOut); got != exitFatal {
		t.Fatalf("exit = %d", got)
	}

	out := errOut.String()
	if !strings.Contains(out, "\n  tunnel \"web\": not found\n") || !strings.Contains(out, `credential "default": token expired`+"\n") {
		t.Fatalf("one problem per line expected, got:\n%s", out)
	}
	if strings.Contains(out, `\n`) || strings.Contains(out, "labelgate failed") {
		t.Fatalf("the error must not be escaped into one quoted line:\n%s", out)
	}
	if !strings.HasPrefix(lastLineBlock(out), "labelgate: cloudflare credential validation failed") {
		t.Fatalf("the error block must start with the labelgate: prefix:\n%s", out)
	}
}

func TestRunFatalStartupErrorStaysStructuredInJSONLogs(t *testing.T) {
	h := newHarness(t)
	cfg := mainConfig()
	cfg.LogFormat = "json"
	h.app.load = func(string) (config.Config, error) { return cfg, nil }
	h.cf.verifyErr = errBoom
	var errOut syncBuffer

	if got := h.app.run(nil, &syncBuffer{}, &errOut); got != exitFatal {
		t.Fatalf("exit = %d", got)
	}

	if !strings.Contains(errOut.String(), `"msg":"labelgate failed"`) {
		t.Fatalf("JSON logs need a structured record:\n%s", errOut.String())
	}
}

// lastLineBlock returns the output from the last "labelgate: " prefix on.
func lastLineBlock(out string) string {
	i := strings.LastIndex(out, "labelgate: ")
	if i < 0 {
		return ""
	}
	return out[i:]
}
