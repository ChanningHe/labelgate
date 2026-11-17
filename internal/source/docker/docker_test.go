package docker

import (
	"context"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
	"github.com/channinghe/labelgate/internal/model"
)

var (
	quietLog  = slog.New(slog.NewTextHandler(io.Discard, nil))
	fastRetry = config.RetryConfig{Attempts: 3, Delay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Backoff: 2}
)

func newSource(t *testing.T, endpoint string, mutate func(*config.DockerConfig)) *Source {
	t.Helper()
	cfg := config.DockerConfig{Endpoint: endpoint, PollInterval: time.Hour}
	if mutate != nil {
		mutate(&cfg)
	}
	s, err := New(cfg, fastRetry, quietLog)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestEndpointScheme(t *testing.T) {
	tests := []struct {
		endpoint string
		want     string
		wantErr  string
	}{
		{"unix:///var/run/docker.sock", "unix", ""},
		{"tcp://10.0.0.5:2376", "tcp", ""},
		{"ssh://user@host", "", "ssh:// endpoints were removed"},
		{"http://host:2375", "", "unsupported endpoint scheme"},
		{"npipe:////./pipe/docker_engine", "", "unsupported endpoint scheme"},
		{"/var/run/docker.sock", "", "invalid endpoint"},
		{"unix://", "", "invalid endpoint"},
		{"", "", "invalid endpoint"},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			got, err := endpointScheme(tt.endpoint)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestNewRejectsSSHAndBadTLS(t *testing.T) {
	if _, err := New(config.DockerConfig{Endpoint: "ssh://u@h"}, fastRetry, nil); err == nil {
		t.Fatal("ssh:// must be rejected")
	}
	_, err := New(config.DockerConfig{Endpoint: "tcp://127.0.0.1:2376", TLS: config.TLSConfig{CA: "/nonexistent/ca.pem"}}, fastRetry, nil)
	if err == nil || !strings.Contains(err.Error(), "tls.ca") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewDefaultsEndpointAndLogger(t *testing.T) {
	s, err := New(config.DockerConfig{}, fastRetry, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.log == nil {
		t.Fatal("logger default missing")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotMapsAndFiltersContainers(t *testing.T) {
	f := newFakeEngine(t)
	f.setContainers(
		summary("id-b", "zeta", "running", map[string]string{"labelgate.dns.web.hostname": "z.example.com"},
			map[string]map[string]string{"bridge": {"IPAddress": "172.17.0.3"}}),
		summary("id-a", "alpha", "running", map[string]string{"x": "y"},
			map[string]map[string]string{
				"v4":   {"IPAddress": "10.0.0.2", "GlobalIPv6Address": "2001:db8::2"},
				"v6":   {"GlobalIPv6Address": "2001:db8::3"},
				"none": {},
			}),
		summary("id-c", "gone", "exited", nil, nil),
		summary("0123456789abcdef", "", "running", nil, nil),
	)
	src := newSource(t, tcpEndpoint(f), nil)
	fixed := time.Date(2026, 11, 17, 8, 0, 0, 0, time.FixedZone("x", 3600))
	src.now = func() time.Time { return fixed }

	snap, err := src.Snapshot(context.Background())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if snap.AgentID != model.LocalAgentID || !snap.Complete || !snap.TakenAt.Equal(fixed) || snap.TakenAt.Location() != time.UTC {
		t.Errorf("snapshot header = %+v", snap)
	}
	var names []string
	for _, c := range snap.Containers {
		names = append(names, c.Name)
	}
	if strings.Join(names, ",") != "0123456789ab,alpha,zeta" {
		t.Fatalf("names (sorted, exited dropped, unnamed -> short ID) = %v", names)
	}
	alpha := snap.Containers[1]
	if alpha.ID != "id-a" || alpha.State != "running" || alpha.Labels["x"] != "y" {
		t.Errorf("alpha = %+v", alpha)
	}
	wantNets := map[string]string{"v4": "10.0.0.2", "v6": "2001:db8::3"}
	if len(alpha.Networks) != 2 || alpha.Networks["v4"] != wantNets["v4"] || alpha.Networks["v6"] != wantNets["v6"] {
		t.Errorf("networks (IPv4 preferred, IPv6 fallback, empty omitted) = %v", alpha.Networks)
	}
	// Every IPv6 address is recorded, also on networks that have an IPv4.
	wantNets6 := map[string]string{"v4": "2001:db8::2", "v6": "2001:db8::3"}
	if len(alpha.Networks6) != 2 || alpha.Networks6["v4"] != wantNets6["v4"] || alpha.Networks6["v6"] != wantNets6["v6"] {
		t.Errorf("networks6 (all global IPv6, empty omitted) = %v", alpha.Networks6)
	}
	if zeta := snap.Containers[2]; zeta.Networks["bridge"] != "172.17.0.3" || len(zeta.Networks6) != 0 {
		t.Errorf("zeta = %+v", zeta)
	}
	if snap.Containers[0].Labels == nil {
		t.Error("labels must never be nil")
	}
}

func TestSnapshotFilterLabel(t *testing.T) {
	f := newFakeEngine(t)
	f.setContainers(
		summary("1", "has-label", "running", map[string]string{"labelgate.enable": "true"}, nil),
		summary("2", "wrong-value", "running", map[string]string{"labelgate.enable": "false"}, nil),
		summary("3", "no-label", "running", map[string]string{"other": "1"}, nil),
	)
	tests := []struct {
		filter string
		want   []string
	}{
		{"", []string{"has-label", "no-label", "wrong-value"}},
		{"labelgate.enable", []string{"has-label", "wrong-value"}},
		{"labelgate.enable=true", []string{"has-label"}},
		{"labelgate.enable=", []string{}},
		{"missing", []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			src := newSource(t, tcpEndpoint(f), func(c *config.DockerConfig) { c.FilterLabel = tt.filter })
			snap, err := src.Snapshot(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := []string{}
			for _, c := range snap.Containers {
				got = append(got, c.Name)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			list, _ := f.queries()
			q, _ := url.ParseQuery(list)
			if tt.filter == "" && q.Get("filters") != "" {
				t.Errorf("unexpected daemon-side filter %q", q.Get("filters"))
			}
			if tt.filter != "" && !strings.Contains(q.Get("filters"), tt.filter) {
				t.Errorf("daemon-side filter %q lacks %q", q.Get("filters"), tt.filter)
			}
		})
	}
}

func TestSnapshotErrorIsReturnedNotEmpty(t *testing.T) {
	f := newFakeEngine(t)
	f.setListErr(500)
	src := newSource(t, tcpEndpoint(f), nil)
	snap, err := src.Snapshot(context.Background())
	if err == nil || !strings.Contains(err.Error(), "list containers") {
		t.Fatalf("err = %v", err)
	}
	if snap.Complete || len(snap.Containers) != 0 {
		t.Fatalf("snapshot on error must be zero, got %+v", snap)
	}
}

func TestSnapshotUnreachableDaemon(t *testing.T) {
	f := newFakeEngine(t)
	endpoint := tcpEndpoint(f)
	f.Close()
	src := newSource(t, endpoint, nil)
	if _, err := src.Snapshot(context.Background()); err == nil {
		t.Fatal("expected error")
	}
}

func TestSnapshotOverUnixSocket(t *testing.T) {
	f, sock := newFakeEngineUnix(t)
	f.setContainers(summary("1", "web", "running", map[string]string{"a": "b"}, nil))
	src := newSource(t, "unix://"+sock, nil)
	snap, err := src.Snapshot(context.Background())
	if err != nil || len(snap.Containers) != 1 || snap.Containers[0].Name != "web" {
		t.Fatalf("snap=%+v err=%v", snap, err)
	}
}

func TestMatchesFilter(t *testing.T) {
	labels := map[string]string{"a": "1", "empty": ""}
	tests := []struct {
		filter string
		want   bool
	}{
		{"", true}, {"a", true}, {"a=1", true}, {"a=2", false}, {"b", false},
		{"empty", true}, {"empty=", true}, {"a=", false},
	}
	for _, tt := range tests {
		if got := matchesFilter(labels, tt.filter); got != tt.want {
			t.Errorf("matchesFilter(%q) = %v, want %v", tt.filter, got, tt.want)
		}
	}
}

func TestBackoff(t *testing.T) {
	tests := []struct {
		name    string
		retry   config.RetryConfig
		attempt int
		want    time.Duration
	}{
		{"first", config.RetryConfig{Delay: time.Second, MaxDelay: time.Minute, Backoff: 2}, 0, time.Second},
		{"third", config.RetryConfig{Delay: time.Second, MaxDelay: time.Minute, Backoff: 2}, 2, 4 * time.Second},
		{"capped", config.RetryConfig{Delay: time.Second, MaxDelay: 5 * time.Second, Backoff: 2}, 10, 5 * time.Second},
		{"zero config falls back", config.RetryConfig{}, 0, time.Second},
		{"zero config grows", config.RetryConfig{}, 3, 8 * time.Second},
		{"bad factor falls back", config.RetryConfig{Delay: time.Second, MaxDelay: time.Minute, Backoff: 0.5}, 1, 2 * time.Second},
		{"max below delay", config.RetryConfig{Delay: 10 * time.Second, MaxDelay: time.Second, Backoff: 2}, 3, 10 * time.Second},
		{"constant factor 1", config.RetryConfig{Delay: time.Second, MaxDelay: time.Minute, Backoff: 1}, 5, time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &Source{retry: tt.retry}
			if got := s.backoff(tt.attempt); got != tt.want {
				t.Fatalf("backoff(%d) = %v, want %v", tt.attempt, got, tt.want)
			}
		})
	}
}

// watcher runs Watch in the background and counts notifications.
type watcher struct {
	notifies atomic.Int64
	done     chan error
	cancel   context.CancelFunc
}

func startWatch(t *testing.T, src *Source, onNotify func()) *watcher {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	w := &watcher{done: make(chan error, 1), cancel: cancel}
	go func() {
		w.done <- src.Watch(ctx, func() {
			w.notifies.Add(1)
			if onNotify != nil {
				onNotify()
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-w.done })
	return w
}

func (w *watcher) count() int64 { return w.notifies.Load() }

func (w *watcher) stop(t *testing.T) error {
	t.Helper()
	w.cancel()
	select {
	case err := <-w.done:
		w.done <- err // let the cleanup drain too
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not return after cancel")
		return nil
	}
}

func TestWatchSubscribesBeforeFirstNotify(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), nil)
	var subsAtFirstNotify atomic.Int64
	subsAtFirstNotify.Store(-1)
	w := startWatch(t, src, func() {
		subsAtFirstNotify.CompareAndSwap(-1, int64(f.subscriptions()))
	})
	waitFor(t, "initial notify", func() bool { return w.count() >= 1 })
	if got := subsAtFirstNotify.Load(); got < 1 {
		t.Fatalf("first notify fired with %d active subscriptions; the watch must start before the list", got)
	}
}

func TestWatchEventFilterAndNotify(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), func(c *config.DockerConfig) { c.FilterLabel = "labelgate.enable" })
	w := startWatch(t, src, nil)
	waitFor(t, "initial notify", func() bool { return w.count() >= 1 })

	_, events := f.queries()
	q, _ := url.ParseQuery(events)
	filters := q.Get("filters")
	for _, want := range []string{`"type":{"container":true}`, `"start":true`, `"die":true`, `"destroy":true`, `"rename":true`, `"update":true`, `"label":{"labelgate.enable":true}`} {
		if !strings.Contains(filters, want) {
			t.Errorf("event filters %s lack %s", filters, want)
		}
	}

	before := w.count()
	f.emit("start")
	waitFor(t, "notify after event", func() bool { return w.count() > before })
	before = w.count()
	f.emit("die")
	waitFor(t, "notify after second event", func() bool { return w.count() > before })
}

func TestWatchReconnectsAndResyncsAfterStreamDrop(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), nil)
	w := startWatch(t, src, nil)
	waitFor(t, "first subscription", func() bool { return f.subscriptions() == 1 && w.count() >= 1 })

	before := w.count()
	f.dropStreams()

	waitFor(t, "second subscription", func() bool { return f.subscriptions() >= 2 })
	waitFor(t, "resync notify after reconnect", func() bool { return w.count() > before })

	// The new stream works.
	before = w.count()
	f.emit("start")
	waitFor(t, "event on the new stream", func() bool { return w.count() > before })
}

func TestWatchRetriesWhileDaemonRejectsEvents(t *testing.T) {
	f := newFakeEngine(t)
	f.setEventsErr(503)
	src := newSource(t, tcpEndpoint(f), nil)
	w := startWatch(t, src, nil)

	time.Sleep(30 * time.Millisecond) // several failed attempts at 1-5ms backoff
	if f.subscriptions() != 0 {
		t.Fatal("no stream should be established while the daemon rejects /events")
	}
	f.setEventsErr(0)
	waitFor(t, "subscription after recovery", func() bool { return f.subscriptions() >= 1 })
	before := w.count()
	f.emit("start")
	waitFor(t, "event after recovery", func() bool { return w.count() > before })
}

func TestWatchRetriesWhileDaemonUnreachable(t *testing.T) {
	f := newFakeEngine(t)
	endpoint := tcpEndpoint(f)
	f.Close()
	src := newSource(t, endpoint, nil)
	w := startWatch(t, src, nil)
	time.Sleep(30 * time.Millisecond)
	if err := w.stop(t); err != context.Canceled {
		t.Fatalf("Watch err = %v, want context.Canceled", err)
	}
}

func TestWatchPollTicks(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), func(c *config.DockerConfig) { c.PollInterval = 10 * time.Millisecond })
	w := startWatch(t, src, nil)
	waitFor(t, "several poll notifications", func() bool { return w.count() >= 4 })
}

func TestWatchCancelDuringBackoffReturnsPromptly(t *testing.T) {
	f := newFakeEngine(t)
	f.setEventsErr(503)
	src := newSource(t, tcpEndpoint(f), nil)
	src.retry = config.RetryConfig{Delay: time.Hour, MaxDelay: time.Hour, Backoff: 2}
	w := startWatch(t, src, nil)
	waitFor(t, "first failed attempt", func() bool { return w.count() >= 1 })
	start := time.Now()
	if err := w.stop(t); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not interrupt the backoff sleep")
	}
}

func TestWatchCancelWhileStreaming(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), nil)
	w := startWatch(t, src, nil)
	waitFor(t, "subscription", func() bool { return f.subscriptions() == 1 })
	if err := w.stop(t); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
}

func TestWatchResetsBackoffAfterHealthyStream(t *testing.T) {
	f := newFakeEngine(t)
	src := newSource(t, tcpEndpoint(f), nil)
	src.retry = config.RetryConfig{Delay: time.Millisecond, MaxDelay: 20 * time.Millisecond, Backoff: 2}
	w := startWatch(t, src, nil)
	waitFor(t, "subscription", func() bool { return f.subscriptions() == 1 })
	time.Sleep(30 * time.Millisecond) // stream lives longer than MaxDelay
	f.dropStreams()
	waitFor(t, "reconnect", func() bool { return f.subscriptions() >= 2 })
	_ = w
}

func TestTLSHTTPClient(t *testing.T) {
	p := newPKI(t)
	ca, cert, key := p.write(t)
	dir := t.TempDir()
	garbage := filepath.Join(dir, "garbage.pem")
	if err := os.WriteFile(garbage, []byte("not a cert"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		cfg     config.TLSConfig
		wantNil bool
		wantErr string
	}{
		{"unset is plain http", config.TLSConfig{}, true, ""},
		{"ca only", config.TLSConfig{CA: ca}, false, ""},
		{"ca and client cert", config.TLSConfig{CA: ca, Cert: cert, Key: key}, false, ""},
		{"insecure only", config.TLSConfig{InsecureSkipVerify: true}, false, ""},
		{"missing ca file", config.TLSConfig{CA: filepath.Join(dir, "nope.pem")}, false, "read tls.ca"},
		{"garbage ca", config.TLSConfig{CA: garbage}, false, "no valid certificate"},
		{"cert without key", config.TLSConfig{Cert: cert}, false, "together"},
		{"key without cert", config.TLSConfig{Key: key}, false, "together"},
		{"unreadable pair", config.TLSConfig{Cert: garbage, Key: key}, false, "client certificate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hc, err := tlsHTTPClient(tt.cfg, quietLog)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil || (hc == nil) != tt.wantNil {
				t.Fatalf("hc=%v err=%v", hc, err)
			}
		})
	}
}

func TestTLSEndToEnd(t *testing.T) {
	p := newPKI(t)
	ca, cert, key := p.write(t)
	plain := newFakeEngineTLS(t, p, false)
	mtls := newFakeEngineTLS(t, p, true)

	tests := []struct {
		name    string
		engine  *fakeEngine
		tls     config.TLSConfig
		wantErr bool
	}{
		{"verified with CA", plain, config.TLSConfig{CA: ca}, false},
		{"unknown CA is rejected by default", plain, config.TLSConfig{Cert: cert, Key: key}, true},
		{"insecure opt-in skips verification", plain, config.TLSConfig{InsecureSkipVerify: true}, false},
		{"mutual TLS with client certificate", mtls, config.TLSConfig{CA: ca, Cert: cert, Key: key}, false},
		{"mutual TLS without client certificate fails", mtls, config.TLSConfig{CA: ca}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.engine.setContainers(summary("1", "web", "running", nil, nil))
			src := newSource(t, tcpEndpoint(tt.engine), func(c *config.DockerConfig) { c.TLS = tt.tls })
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			snap, err := src.Snapshot(ctx)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected TLS failure")
				}
				return
			}
			if err != nil || len(snap.Containers) != 1 {
				t.Fatalf("snap=%+v err=%v", snap, err)
			}
		})
	}
}
