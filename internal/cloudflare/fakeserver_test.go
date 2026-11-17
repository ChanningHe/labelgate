package cloudflare

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channinghe/labelgate/internal/config"
)

// fakeCF emulates the subset of the Cloudflare v4 API the client uses.
// Objects are stored as generic JSON maps per collection ("dns:<zone>",
// "apps:<acct>", "policies:<acct>", "tags:<acct>", "zones:<token>").
type fakeCF struct {
	t   *testing.T
	srv *httptest.Server

	mu          sync.Mutex
	tokens      map[string]bool // valid tokens
	coll        map[string][]map[string]any
	tunnelCfg   map[string]json.RawMessage // tunnel ID -> config (nil = never configured)
	tunnels     map[string]bool            // known tunnel IDs
	maxPerPage  int                        // server-side page size cap
	noTotals    bool                       // omit total_pages/total_count
	injections  []injection
	requests    []recorded
	seq         int
	userVerify4 bool // user/tokens/verify answers 401 (account-owned token)
}

type recorded struct {
	Method, Path, Query, Token string
	Body                       []byte
}

type injection struct {
	match   func(r *http.Request) bool
	status  int
	body    string
	header  map[string]string
	hang    bool
	pending int
}

const testToken = "tok-root"

func newFakeCF(t *testing.T) *fakeCF {
	t.Helper()
	f := &fakeCF{
		t:          t,
		tokens:     map[string]bool{testToken: true},
		coll:       map[string][]map[string]any{},
		tunnelCfg:  map[string]json.RawMessage{},
		tunnels:    map[string]bool{},
		maxPerPage: 1000,
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// client builds a Client against the fake with fast retries.
func (f *fakeCF) client(t *testing.T, cfg config.CloudflareConfig, mutate ...func(*Options)) *Client {
	t.Helper()
	opts := Options{
		Cloudflare: cfg,
		Retry:      config.RetryConfig{Attempts: 3, Delay: time.Millisecond, MaxDelay: 5 * time.Millisecond, Backoff: 2},
		HTTPClient: f.srv.Client(),
		BaseURL:    f.srv.URL,
	}
	for _, m := range mutate {
		m(&opts)
	}
	c, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func rootCfg() config.CloudflareConfig {
	return config.CloudflareConfig{APIToken: testToken, AccountID: "acct1", TunnelID: "TUN-ROOT"}
}

func (f *fakeCF) addZone(token, id, name, account string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = true
	k := "zones:" + token
	f.coll[k] = append(f.coll[k], map[string]any{"id": id, "name": name, "account": map[string]any{"id": account}})
}

func (f *fakeCF) seed(collection string, obj map[string]any) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := obj["id"]; !ok {
		f.seq++
		obj["id"] = fmt.Sprintf("id-%04d", f.seq)
	}
	f.coll[collection] = append(f.coll[collection], obj)
	return obj
}

func (f *fakeCF) objects(collection string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any(nil), f.coll[collection]...)
}

// inject queues n canned responses for requests matching method and path prefix.
func (f *fakeCF) inject(method, pathPrefix string, n, status int, body string, header map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.injections = append(f.injections, injection{
		match: func(r *http.Request) bool {
			return r.Method == method && strings.HasPrefix(r.URL.Path, pathPrefix)
		},
		status: status, body: body, header: header, pending: n,
	})
}

// hang makes the next n matching requests block until the client gives up.
func (f *fakeCF) hang(method, pathPrefix string, n int) {
	f.inject(method, pathPrefix, n, 0, "", nil)
	f.mu.Lock()
	f.injections[len(f.injections)-1].hang = true
	f.mu.Unlock()
}

func (f *fakeCF) reqs() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

func (f *fakeCF) count(method, pathPrefix string) int {
	n := 0
	for _, r := range f.reqs() {
		if r.Method == method && strings.HasPrefix(r.Path, pathPrefix) {
			n++
		}
	}
	return n
}

func cfErrorBody(code int, msg string) string {
	return fmt.Sprintf(`{"success":false,"errors":[{"code":%d,"message":%q}],"messages":[],"result":null}`, code, msg)
}

func (f *fakeCF) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.requests = append(f.requests, recorded{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Token: token, Body: body})
	inj, ok := f.popInjection(r)
	valid := f.tokens[token]
	f.mu.Unlock()
	if ok {
		f.writeInjection(w, r, inj)
		return
	}
	if !valid {
		writeRaw(w, http.StatusUnauthorized, cfErrorBody(10000, "Authentication error"))
		return
	}
	f.route(w, r, token, body)
}

func (f *fakeCF) popInjection(r *http.Request) (injection, bool) {
	for i := range f.injections {
		in := &f.injections[i]
		if in.pending > 0 && in.match(r) {
			in.pending--
			return *in, true
		}
	}
	return injection{}, false
}

func (f *fakeCF) writeInjection(w http.ResponseWriter, r *http.Request, in injection) {
	if in.hang {
		<-r.Context().Done()
		return
	}
	for k, v := range in.header {
		w.Header().Set(k, v)
	}
	writeRaw(w, in.status, in.body)
}

func writeRaw(w http.ResponseWriter, status int, body string) {
	if strings.HasPrefix(body, "{") {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Set("Content-Type", "text/html")
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeResult(w http.ResponseWriter, status int, result any, info map[string]any) {
	env := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		env["result_info"] = info
	}
	b, _ := json.Marshal(env)
	writeRaw(w, status, string(b))
}

func (f *fakeCF) route(w http.ResponseWriter, r *http.Request, token string, body []byte) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/user/tokens/verify":
		f.verify(w, f.userVerify4)
	case len(parts) == 4 && parts[0] == "accounts" && parts[2] == "tokens" && parts[3] == "verify":
		f.verify(w, false)
	case r.URL.Path == "/zones":
		f.list(w, r, "zones:"+token)
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		f.dns(w, r, parts, body)
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "cfd_tunnel":
		f.tunnel(w, r, parts, body)
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "access":
		f.access(w, r, parts, body)
	default:
		writeRaw(w, http.StatusNotFound, cfErrorBody(7003, "no route"))
	}
}

func (f *fakeCF) verify(w http.ResponseWriter, deny bool) {
	if deny {
		writeRaw(w, http.StatusUnauthorized, cfErrorBody(1000, "Invalid API Token"))
		return
	}
	writeResult(w, http.StatusOK, map[string]any{"id": "tokid", "status": "active"}, nil)
}

func (f *fakeCF) list(w http.ResponseWriter, r *http.Request, collection string) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per_page"))
	page, per = max(page, 1), min(max(per, 1), f.maxPerPage)
	all := f.objects(collection)
	start := min((page-1)*per, len(all))
	end := min(start+per, len(all))
	info := map[string]any{"page": page, "per_page": per, "count": end - start}
	if !f.noTotals {
		info["total_count"] = len(all)
		info["total_pages"] = (len(all) + per - 1) / per
	}
	writeResult(w, http.StatusOK, all[start:end], info)
}

func (f *fakeCF) dns(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	collection := "dns:" + parts[1]
	switch {
	case r.Method == http.MethodGet && len(parts) == 3:
		f.list(w, r, collection)
	case r.Method == http.MethodPost && len(parts) == 3:
		obj := decodeObj(body)
		if code, msg := f.dnsConflict(collection, obj); code != 0 {
			writeRaw(w, http.StatusBadRequest, cfErrorBody(code, msg))
			return
		}
		writeResult(w, http.StatusOK, f.seed(collection, obj), nil)
	case len(parts) == 4:
		f.object(w, r, collection, parts[3], body, 81044)
	default:
		writeRaw(w, http.StatusMethodNotAllowed, cfErrorBody(7001, "method"))
	}
}

func (f *fakeCF) dnsConflict(collection string, rec map[string]any) (int, string) {
	for _, ex := range f.objects(collection) {
		if ex["name"] != rec["name"] {
			continue
		}
		if ex["type"] == "CNAME" || rec["type"] == "CNAME" {
			return 81053, "An A, AAAA, or CNAME record with that host already exists."
		}
		if ex["type"] == rec["type"] && ex["content"] == rec["content"] {
			return 81057, "Identical record already exists."
		}
	}
	return 0, ""
}

// object handles GET/PUT/DELETE on one stored object.
func (f *fakeCF) object(w http.ResponseWriter, r *http.Request, collection, id string, body []byte, notFoundCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	objs := f.coll[collection]
	for i, o := range objs {
		if o["id"] != id {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			writeResult(w, http.StatusOK, o, nil)
		case http.MethodPut:
			n := decodeObj(body)
			n["id"] = id
			objs[i] = n
			writeResult(w, http.StatusOK, n, nil)
		case http.MethodDelete:
			f.coll[collection] = append(objs[:i:i], objs[i+1:]...)
			writeResult(w, http.StatusOK, map[string]any{"id": id}, nil)
		}
		return
	}
	writeRaw(w, http.StatusNotFound, cfErrorBody(notFoundCode, "not found"))
}

func (f *fakeCF) tunnel(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	id := parts[3]
	f.mu.Lock()
	known := f.tunnels[id]
	cfg := f.tunnelCfg[id]
	f.mu.Unlock()
	if !known {
		writeRaw(w, http.StatusNotFound, cfErrorBody(1003, "tunnel not found"))
		return
	}
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		writeResult(w, http.StatusOK, map[string]any{"id": id, "name": "t"}, nil)
	case len(parts) == 5 && r.Method == http.MethodGet:
		f.writeTunnelConfig(w, id, cfg)
	case len(parts) == 5 && r.Method == http.MethodPut:
		var in struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			writeRaw(w, http.StatusBadRequest, cfErrorBody(1001, err.Error()))
			return
		}
		f.mu.Lock()
		f.tunnelCfg[id] = in.Config
		f.mu.Unlock()
		f.writeTunnelConfig(w, id, in.Config)
	default:
		writeRaw(w, http.StatusMethodNotAllowed, cfErrorBody(7001, "method"))
	}
}

func (f *fakeCF) writeTunnelConfig(w http.ResponseWriter, id string, cfg json.RawMessage) {
	if cfg == nil {
		cfg = json.RawMessage("null")
	}
	res := fmt.Sprintf(`{"success":true,"errors":[],"messages":[],"result":{"tunnel_id":%q,"version":3,"source":"cloudflare","config":%s}}`, id, cfg)
	writeRaw(w, http.StatusOK, res)
}

func (f *fakeCF) access(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	collection := parts[3] + ":" + parts[1] // apps:<acct>, policies:<acct>, tags:<acct>
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		f.list(w, r, collection)
	case len(parts) == 4 && r.Method == http.MethodPost:
		obj := decodeObj(body)
		if parts[3] == "tags" {
			obj["id"] = obj["name"]
		}
		writeResult(w, http.StatusOK, f.seed(collection, obj), nil)
	case len(parts) == 5:
		f.object(w, r, collection, parts[4], body, 12130)
	default:
		writeRaw(w, http.StatusMethodNotAllowed, cfErrorBody(7001, "method"))
	}
}

func (f *fakeCF) addToken(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = true
}

func (f *fakeCF) addTunnel(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnels[id] = true
}

func (f *fakeCF) setTunnelConfig(id, cfg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnels[id] = true
	f.tunnelCfg[id] = json.RawMessage(cfg)
}

func (f *fakeCF) tunnelConfig(id string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tunnelCfg[id]
}

// lastBody returns the JSON body of the last request matching method and path prefix.
func (f *fakeCF) lastBody(t *testing.T, method, pathPrefix string) map[string]any {
	t.Helper()
	reqs := f.reqs()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Method == method && strings.HasPrefix(reqs[i].Path, pathPrefix) {
			return decodeObj(reqs[i].Body)
		}
	}
	t.Fatalf("no %s %s request", method, pathPrefix)
	return nil
}

func decodeObj(body []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}
