package main

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
)

// cfServer emulates the subset of the Cloudflare v4 API that the real
// cloudflare.Client uses: token verify, zones (paged), DNS records CRUD,
// tunnel get + configurations GET/PUT (raw JSON, unknown fields kept) and
// Access apps, reusable policies and tags. Objects are generic JSON maps per
// collection ("zones:<token>", "dns:<zone>", "apps:<acct>", "policies:<acct>",
// "tags:<acct>"). Adapted from internal/cloudflare/fakeserver_test.go.
type cfServer struct {
	srv *httptest.Server

	mu         sync.Mutex
	tokens     map[string]bool
	coll       map[string][]map[string]any
	tunnels    map[string]bool
	tunnelCfg  map[string]json.RawMessage
	maxPerPage int
	requests   []cfRequest
	seq        int
}

type cfRequest struct {
	Method, Path string
	Body         []byte
}

func newCFServer(t *testing.T, token string) *cfServer {
	t.Helper()
	f := &cfServer{
		tokens:     map[string]bool{token: true},
		coll:       map[string][]map[string]any{},
		tunnels:    map[string]bool{},
		tunnelCfg:  map[string]json.RawMessage{},
		maxPerPage: 2, // small pages: every list call exercises auto-paging
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *cfServer) addZone(token, id, name, account string) {
	f.seed("zones:"+token, map[string]any{"id": id, "name": name, "account": map[string]any{"id": account}})
}

func (f *cfServer) setTunnelConfig(id, cfg string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tunnels[id] = true
	if cfg != "" {
		f.tunnelCfg[id] = json.RawMessage(cfg)
	}
}

func (f *cfServer) tunnelConfig(id string) json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.tunnelCfg[id]
}

// seed stores obj (assigning an ID if missing) and returns it.
func (f *cfServer) seed(collection string, obj map[string]any) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.seedLocked(collection, obj)
}

func (f *cfServer) seedLocked(collection string, obj map[string]any) map[string]any {
	if _, ok := obj["id"]; !ok {
		f.seq++
		obj["id"] = fmt.Sprintf("cf-%04d", f.seq)
	}
	f.coll[collection] = append(f.coll[collection], obj)
	return obj
}

// objects returns deep copies of a collection.
func (f *cfServer) objects(collection string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]map[string]any, 0, len(f.coll[collection]))
	for _, o := range f.coll[collection] {
		out = append(out, decodeJSONObject(mustMarshal(o)))
	}
	return out
}

// find returns the objects of collection whose key equals value.
func (f *cfServer) find(collection, key string, value any) []map[string]any {
	var out []map[string]any
	for _, o := range f.objects(collection) {
		if o[key] == value {
			out = append(out, o)
		}
	}
	return out
}

func (f *cfServer) reqs() []cfRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]cfRequest(nil), f.requests...)
}

// mutations returns every non-GET request (POST/PUT/PATCH/DELETE).
func (f *cfServer) mutations() []cfRequest {
	var out []cfRequest
	for _, r := range f.reqs() {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			out = append(out, r)
		}
	}
	return out
}

// count returns the number of requests with method whose path contains part.
func (f *cfServer) count(method, part string) int {
	n := 0
	for _, r := range f.reqs() {
		if r.Method == method && strings.Contains(r.Path, part) {
			n++
		}
	}
	return n
}

func (f *cfServer) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	f.mu.Lock()
	f.requests = append(f.requests, cfRequest{Method: r.Method, Path: r.URL.Path, Body: body})
	valid := f.tokens[token]
	f.mu.Unlock()
	if !valid {
		writeCFRaw(w, http.StatusUnauthorized, cfErrorJSON(10000, "Authentication error"))
		return
	}
	f.route(w, r, token, body)
}

func (f *cfServer) route(w http.ResponseWriter, r *http.Request, token string, body []byte) {
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case r.URL.Path == "/user/tokens/verify":
		writeCFResult(w, map[string]any{"id": "tokid", "status": "active"}, nil)
	case r.URL.Path == "/zones" && r.Method == http.MethodGet:
		f.list(w, r, "zones:"+token)
	case len(parts) >= 3 && parts[0] == "zones" && parts[2] == "dns_records":
		f.dns(w, r, parts, body)
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "cfd_tunnel":
		f.tunnel(w, r, parts, body)
	case len(parts) >= 4 && parts[0] == "accounts" && parts[2] == "access":
		f.access(w, r, parts, body)
	default:
		writeCFRaw(w, http.StatusNotFound, cfErrorJSON(7003, "no route"))
	}
}

func (f *cfServer) list(w http.ResponseWriter, r *http.Request, collection string) {
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	per, _ := strconv.Atoi(q.Get("per_page"))
	page, per = max(page, 1), min(max(per, 1), f.maxPerPage)
	all := f.objects(collection)
	start := min((page-1)*per, len(all))
	end := min(start+per, len(all))
	info := map[string]any{
		"page": page, "per_page": per, "count": end - start,
		"total_count": len(all), "total_pages": (len(all) + per - 1) / per,
	}
	writeCFResult(w, all[start:end], info)
}

func (f *cfServer) dns(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	collection := "dns:" + parts[1]
	switch {
	case r.Method == http.MethodGet && len(parts) == 3:
		f.list(w, r, collection)
	case r.Method == http.MethodPost && len(parts) == 3:
		obj := decodeJSONObject(body)
		f.mu.Lock()
		defer f.mu.Unlock()
		if code, msg := dnsClash(f.coll[collection], obj); code != 0 {
			writeCFRaw(w, http.StatusBadRequest, cfErrorJSON(code, msg))
			return
		}
		writeCFResult(w, f.seedLocked(collection, obj), nil)
	case len(parts) == 4:
		f.object(w, r, collection, parts[3], body, 81044)
	default:
		writeCFRaw(w, http.StatusMethodNotAllowed, cfErrorJSON(7001, "method"))
	}
}

// dnsClash mirrors Cloudflare's create-time rejections.
func dnsClash(existing []map[string]any, rec map[string]any) (int, string) {
	for _, ex := range existing {
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

// object handles GET/PUT/PATCH/DELETE on one stored object.
func (f *cfServer) object(w http.ResponseWriter, r *http.Request, collection, id string, body []byte, notFoundCode int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	objs := f.coll[collection]
	for i, o := range objs {
		if o["id"] != id {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			writeCFResult(w, o, nil)
		case http.MethodPut, http.MethodPatch:
			n := decodeJSONObject(body)
			n["id"] = id
			objs[i] = n
			writeCFResult(w, n, nil)
		case http.MethodDelete:
			f.coll[collection] = append(objs[:i:i], objs[i+1:]...)
			writeCFResult(w, map[string]any{"id": id}, nil)
		default:
			writeCFRaw(w, http.StatusMethodNotAllowed, cfErrorJSON(7001, "method"))
		}
		return
	}
	writeCFRaw(w, http.StatusNotFound, cfErrorJSON(notFoundCode, "not found"))
}

func (f *cfServer) tunnel(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	id := parts[3]
	f.mu.Lock()
	known := f.tunnels[id]
	cfg := f.tunnelCfg[id]
	f.mu.Unlock()
	if !known {
		writeCFRaw(w, http.StatusNotFound, cfErrorJSON(1003, "tunnel not found"))
		return
	}
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		writeCFResult(w, map[string]any{"id": id, "name": "e2e"}, nil)
	case len(parts) == 5 && r.Method == http.MethodGet:
		writeTunnelConfig(w, id, cfg)
	case len(parts) == 5 && r.Method == http.MethodPut:
		var in struct {
			Config json.RawMessage `json:"config"`
		}
		if err := json.Unmarshal(body, &in); err != nil {
			writeCFRaw(w, http.StatusBadRequest, cfErrorJSON(1001, err.Error()))
			return
		}
		f.mu.Lock()
		f.tunnelCfg[id] = in.Config
		f.mu.Unlock()
		writeTunnelConfig(w, id, in.Config)
	default:
		writeCFRaw(w, http.StatusMethodNotAllowed, cfErrorJSON(7001, "method"))
	}
}

func writeTunnelConfig(w http.ResponseWriter, id string, cfg json.RawMessage) {
	if cfg == nil {
		cfg = json.RawMessage("null")
	}
	res := fmt.Sprintf(`{"success":true,"errors":[],"messages":[],"result":{"tunnel_id":%q,"version":3,"source":"cloudflare","config":%s}}`, id, cfg)
	writeCFRaw(w, http.StatusOK, res)
}

func (f *cfServer) access(w http.ResponseWriter, r *http.Request, parts []string, body []byte) {
	collection := parts[3] + ":" + parts[1] // apps:<acct>, policies:<acct>, tags:<acct>
	switch {
	case len(parts) == 4 && r.Method == http.MethodGet:
		f.list(w, r, collection)
	case len(parts) == 4 && r.Method == http.MethodPost:
		obj := decodeJSONObject(body)
		if parts[3] == "tags" {
			obj["id"] = obj["name"]
		}
		writeCFResult(w, f.seed(collection, obj), nil)
	case len(parts) == 5:
		f.object(w, r, collection, parts[4], body, 12130)
	default:
		writeCFRaw(w, http.StatusMethodNotAllowed, cfErrorJSON(7001, "method"))
	}
}

func cfErrorJSON(code int, msg string) string {
	return fmt.Sprintf(`{"success":false,"errors":[{"code":%d,"message":%q}],"messages":[],"result":null}`, code, msg)
}

func writeCFRaw(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, body)
}

func writeCFResult(w http.ResponseWriter, result any, info map[string]any) {
	env := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": result}
	if info != nil {
		env["result_info"] = info
	}
	writeCFRaw(w, http.StatusOK, string(mustMarshal(env)))
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func decodeJSONObject(body []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(body, &m)
	if m == nil {
		m = map[string]any{}
	}
	return m
}
