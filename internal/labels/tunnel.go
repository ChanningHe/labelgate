package labels

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/channinghe/labelgate/internal/model"
)

const originPrefix = "origin."

// tunnelDefaultProps are the properties allowed in <prefix>.tunnel.default.*.
var tunnelDefaultProps = map[string]bool{"tunnel": true, "credential": true, "cleanup": true}

// networkSchemes are tunnel service schemes of the form scheme://host[:port].
var networkSchemes = map[string]bool{"http": true, "https": true, "tcp": true, "ssh": true, "rdp": true, "udp": true}

// unixSchemes are tunnel service schemes of the form scheme:/path.
var unixSchemes = map[string]bool{"unix": true, "unix+tls": true}

const (
	statusPrefix = "http_status:"
	httpStatusLo = 100
	httpStatusHi = 599
)

var tunnelHandlers = map[string]func(*model.TunnelSpec, string) error{
	"hostname":   func(s *model.TunnelSpec, v string) error { s.Hostname = model.NormalizeHostname(v); return nil },
	"service":    func(s *model.TunnelSpec, v string) error { s.URL = strings.TrimSpace(v); return nil },
	"tunnel":     func(s *model.TunnelSpec, v string) error { s.Tunnel = strings.TrimSpace(v); return nil },
	"path":       func(s *model.TunnelSpec, v string) error { s.Path = strings.TrimSpace(v); return nil },
	"credential": func(s *model.TunnelSpec, v string) error { s.Credential = strings.TrimSpace(v); return nil },
	"cleanup":    func(s *model.TunnelSpec, v string) (err error) { s.Cleanup, err = parseBool(v); return },
	"access":     func(s *model.TunnelSpec, v string) (err error) { s.Access, err = accessRef(v); return },
}

var originHandlers = map[string]func(*model.OriginRequest, string) error{
	"connect_timeout":    func(o *model.OriginRequest, v string) (err error) { o.ConnectTimeout, err = parseSeconds(v); return },
	"tls_timeout":        func(o *model.OriginRequest, v string) (err error) { o.TLSTimeout, err = parseSeconds(v); return },
	"tcp_keepalive":      func(o *model.OriginRequest, v string) (err error) { o.TCPKeepAlive, err = parseSeconds(v); return },
	"keep_alive_timeout": func(o *model.OriginRequest, v string) (err error) { o.KeepAliveTimeout, err = parseSeconds(v); return },
	"keep_alive_connections": func(o *model.OriginRequest, v string) (err error) {
		o.KeepAliveConnections, err = parseInt(v, 0, 1<<20)
		return
	},
	"no_tls_verify":      func(o *model.OriginRequest, v string) (err error) { o.NoTLSVerify, err = parseBool(v); return },
	"origin_server_name": func(o *model.OriginRequest, v string) error { o.OriginServerName = strings.TrimSpace(v); return nil },
	"ca_pool":            func(o *model.OriginRequest, v string) error { o.CAPool = strings.TrimSpace(v); return nil },
	"http_host_header":   func(o *model.OriginRequest, v string) error { o.HTTPHostHeader = strings.TrimSpace(v); return nil },
	"no_happy_eyeballs":  func(o *model.OriginRequest, v string) (err error) { o.NoHappyEyeballs, err = parseBool(v); return },
	"disable_chunked_encoding": func(o *model.OriginRequest, v string) (err error) {
		o.DisableChunkedEncoding, err = parseBool(v)
		return
	},
	"proxy_type": setProxyType,
}

func setProxyType(o *model.OriginRequest, v string) error {
	t := strings.ToLower(strings.TrimSpace(v))
	if t != "" && t != "socks" {
		return fmt.Errorf("invalid proxy_type %q (use socks or leave empty)", v)
	}
	o.ProxyType = t
	return nil
}

// parseTunnels turns the tunnel service groups into specs sorted by service name.
func (p *parser) parseTunnels(services map[string]props) []model.TunnelSpec {
	base := model.TunnelSpec{Cleanup: true}
	defaultsStem := p.stem(typeTunnel, defaultsName)
	defaultsOK := true
	if d, ok := services[defaultsName]; ok {
		defaultsOK = !p.applyTunnel(&base, defaultsStem, d, tunnelDefaultProps)
	}

	var out []model.TunnelSpec
	for _, name := range sortedKeys(services) {
		if name == defaultsName {
			continue
		}
		stem := p.stem(typeTunnel, name)
		if !defaultsOK {
			p.errs.add(stem, "service skipped: invalid %s settings", defaultsStem)
			continue
		}
		s := base
		s.Service = name
		if p.applyTunnel(&s, stem, services[name], nil) {
			continue
		}
		if p.validateTunnel(s, stem) {
			out = append(out, s)
		}
	}
	return out
}

// applyTunnel applies props to s; it reports whether any value was invalid.
func (p *parser) applyTunnel(s *model.TunnelSpec, stem string, in props, allowed map[string]bool) bool {
	allow := func(n string) bool {
		if allowed != nil {
			return allowed[n]
		}
		return true
	}
	return p.applyProps(stem, in, allow, func(name, v string) (bool, error) {
		if setting, ok := strings.CutPrefix(name, originPrefix); ok {
			h, known := originHandlers[setting]
			if !known {
				return false, nil
			}
			return true, h(&s.Origin, v)
		}
		h, ok := tunnelHandlers[name]
		if !ok {
			return false, nil
		}
		return true, h(s, v)
	})
}

// validateTunnel checks the required and structural fields of a tunnel service.
func (p *parser) validateTunnel(s model.TunnelSpec, stem string) bool {
	switch {
	case s.Hostname == "":
		p.errs.add(stem, "hostname is required")
	case !model.ValidHostname(s.Hostname):
		p.errs.add(stem+".hostname", "invalid hostname %q", s.Hostname)
	case s.URL == "":
		p.errs.add(stem, "service is required")
	default:
		if err := validateServiceURL(s.URL); err != nil {
			p.errs.add(stem+".service", "%v", err)
			return false
		}
		if _, err := regexp.Compile(s.Path); err != nil {
			p.errs.add(stem+".path", "invalid path regex: %v", err)
			return false
		}
		return true
	}
	return false
}

// validateServiceURL accepts the origin service forms Cloudflare tunnels support.
func validateServiceURL(raw string) error {
	if raw == "hello_world" || raw == "bastion" {
		return nil
	}
	if code, ok := strings.CutPrefix(raw, statusPrefix); ok {
		n, err := strconv.Atoi(code)
		if err != nil || n < httpStatusLo || n > httpStatusHi {
			return fmt.Errorf("invalid http_status code %q (use %d-%d)", code, httpStatusLo, httpStatusHi)
		}
		return nil
	}
	scheme, rest, ok := strings.Cut(raw, ":")
	scheme = strings.ToLower(scheme)
	switch {
	case !ok:
		return fmt.Errorf("invalid service %q (expected scheme://host:port)", raw)
	case unixSchemes[scheme]:
		if strings.Trim(rest, "/") == "" {
			return fmt.Errorf("service %q needs a socket path", raw)
		}
		return nil
	case networkSchemes[scheme]:
		return validateNetworkService(raw)
	}
	return fmt.Errorf("unsupported service scheme %q", scheme)
}

func validateNetworkService(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid service %q (expected scheme://host:port)", raw)
	}
	return nil
}
