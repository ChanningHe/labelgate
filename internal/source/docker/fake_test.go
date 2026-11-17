package docker

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var versionPrefix = regexp.MustCompile(`^/v[0-9.]+`)

// fakeEngine serves the subset of the Docker Engine API the source uses.
type fakeEngine struct {
	*httptest.Server

	mu         sync.Mutex
	containers []map[string]any
	listErr    int // HTTP status to fail /containers/json with (0 = ok)
	eventsErr  int // HTTP status to fail /events with (0 = ok)
	subs       []*eventSub
	subCount   int // total /events subscriptions ever made
	lastList   string
	lastEvents string
}

type eventSub struct {
	ch   chan string
	done chan struct{}
}

func newFakeEngine(t *testing.T) *fakeEngine {
	t.Helper()
	f := &fakeEngine{}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func newFakeEngineUnix(t *testing.T) (*fakeEngine, string) {
	t.Helper()
	dir, err := os.MkdirTemp("", "lgd")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeEngine{}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.Listener = ln
	f.Start()
	t.Cleanup(f.Close)
	return f, sock
}

func (f *fakeEngine) serve(w http.ResponseWriter, r *http.Request) {
	path := versionPrefix.ReplaceAllString(r.URL.Path, "")
	switch path {
	case "/_ping":
		w.Header().Set("Api-Version", "1.45")
		w.Header().Set("OSType", "linux")
		w.WriteHeader(http.StatusOK)
	case "/containers/json":
		f.mu.Lock()
		f.lastList = r.URL.RawQuery
		status, list := f.listErr, f.containers
		f.mu.Unlock()
		if status != 0 {
			http.Error(w, `{"message":"daemon unavailable"}`, status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	case "/events":
		f.serveEvents(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeEngine) serveEvents(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.lastEvents = r.URL.RawQuery
	status := f.eventsErr
	f.mu.Unlock()
	if status != 0 {
		http.Error(w, `{"message":"no events"}`, status)
		return
	}
	sub := &eventSub{ch: make(chan string, 16), done: make(chan struct{})}
	f.mu.Lock()
	f.subs = append(f.subs, sub)
	f.subCount++
	f.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	for {
		select {
		case line := <-sub.ch:
			_, _ = w.Write([]byte(line + "\n"))
			w.(http.Flusher).Flush()
		case <-sub.done:
			return
		case <-r.Context().Done():
			return
		}
	}
}

func (f *fakeEngine) setContainers(c ...map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers = c
}

func (f *fakeEngine) setListErr(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listErr = status
}

func (f *fakeEngine) setEventsErr(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.eventsErr = status
}

func (f *fakeEngine) subscriptions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subCount
}

func (f *fakeEngine) queries() (list, events string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastList, f.lastEvents
}

// emit sends a container event to every live subscriber.
func (f *fakeEngine) emit(action string) {
	line, _ := json.Marshal(map[string]any{
		"Type": "container", "Action": action, "status": action,
		"Actor": map[string]any{"ID": "abc", "Attributes": map[string]string{"name": "web"}},
		"time":  time.Now().Unix(), "timeNano": time.Now().UnixNano(),
	})
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		select {
		case s.ch <- string(line):
		default:
		}
	}
}

// dropStreams ends every live event stream (simulates a daemon restart / network drop).
func (f *fakeEngine) dropStreams() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.subs {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}
	f.subs = nil
}

// summary builds one /containers/json entry.
func summary(id, name, state string, labels map[string]string, nets map[string]map[string]string) map[string]any {
	networks := map[string]any{}
	for n, ep := range nets {
		e := map[string]any{}
		for k, v := range ep {
			e[k] = v
		}
		networks[n] = e
	}
	names := []string{}
	if name != "" {
		names = append(names, "/"+name)
	}
	return map[string]any{
		"Id": id, "Names": names, "Image": "img", "State": state, "Labels": labels,
		"NetworkSettings": map[string]any{"Networks": networks},
	}
}

// pki is a throwaway self-signed certificate usable as server and client cert.
type pki struct {
	certPEM, keyPEM []byte
	tlsCert         tls.Certificate
	pool            *x509.CertPool
}

func newPKI(t *testing.T) pki {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "labelgate-test"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	p := pki{
		certPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		keyPEM:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
		pool:    x509.NewCertPool(),
	}
	if p.tlsCert, err = tls.X509KeyPair(p.certPEM, p.keyPEM); err != nil {
		t.Fatal(err)
	}
	p.pool.AppendCertsFromPEM(p.certPEM)
	return p
}

// write stores the PEM files in dir and returns their paths.
func (p pki) write(t *testing.T) (ca, cert, key string) {
	t.Helper()
	dir := t.TempDir()
	ca, cert, key = filepath.Join(dir, "ca.pem"), filepath.Join(dir, "cert.pem"), filepath.Join(dir, "key.pem")
	for path, data := range map[string][]byte{ca: p.certPEM, cert: p.certPEM, key: p.keyPEM} {
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return ca, cert, key
}

// newFakeEngineTLS serves the fake engine over TLS; if requireClient, a client certificate is demanded.
func newFakeEngineTLS(t *testing.T, p pki, requireClient bool) *fakeEngine {
	t.Helper()
	f := &fakeEngine{}
	f.Server = httptest.NewUnstartedServer(http.HandlerFunc(f.serve))
	f.TLS = &tls.Config{Certificates: []tls.Certificate{p.tlsCert}, MinVersion: tls.VersionTLS12}
	if requireClient {
		f.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		f.TLS.ClientCAs = p.pool
	}
	f.StartTLS()
	t.Cleanup(f.Close)
	return f
}

func tcpEndpoint(f *fakeEngine) string {
	return "tcp://" + strings.TrimPrefix(strings.TrimPrefix(f.URL, "https://"), "http://")
}
