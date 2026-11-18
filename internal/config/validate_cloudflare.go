package config

import "strings"

func checkCloudflare(p *ValidationErrors, cfg Config) {
	cf := cfg.Cloudflare
	rootTokenMissing := false
	if cf.TunnelID != "" {
		if cf.AccountID == "" {
			p.addf("cloudflare.account_id", "required when cloudflare.tunnel_id is set (find it on any zone overview page in the Cloudflare dashboard)")
		}
		if cf.APIToken == "" {
			p.addf("cloudflare.api_token", "required because cloudflare.tunnel_id is set: the tunnel is managed with this token")
			rootTokenMissing = true
		}
	}
	checkCredentials(p, cf.Credentials)
	checkTunnels(p, cf, rootTokenMissing)
	p.add(checkDefaultTunnel(cfg))
	// Both rules below concern cloudflare.api_token: report it once.
	if !rootTokenMissing && cf.APIToken == "" && len(cf.Credentials) == 0 && !cfg.SkipCredentialValidation {
		p.addf("cloudflare.api_token",
			"no Cloudflare API token is configured: set this, or add an entry under cloudflare.credentials in the config file")
	}
}

func checkName(field, name string) *ValidationError {
	if name == RootCredential || name == RootTunnel {
		return fail(field, "name %q is reserved for the root entry", name)
	}
	if !resourceNamePat.MatchString(name) {
		return fail(field, "name %q is invalid: use lower-case letters, digits, hyphens or underscores, starting with a letter or digit", name)
	}
	return nil
}

func checkCredentials(p *ValidationErrors, creds map[string]CredentialConfig) {
	for _, name := range sortedKeys(creds) {
		field := "cloudflare.credentials." + name
		c := creds[name]
		p.add(checkName(field, name))
		if strings.TrimSpace(c.APIToken) == "" {
			p.addf(field+".api_token", "must not be empty: paste the Cloudflare API token for this credential")
		}
		for _, z := range c.Zones {
			if !validGlob(z) {
				p.addf(field+".zones", "invalid zone pattern %q (use a zone name such as example.com or a glob such as *.example.com)", z)
				break
			}
		}
	}
}

// checkTunnels validates cloudflare.tunnels. rootTokenMissing means the root
// credential was already reported as missing, so tunnels that fall back to it
// are not reported again.
func checkTunnels(p *ValidationErrors, cf CloudflareConfig, rootTokenMissing bool) {
	for _, name := range sortedKeys(cf.Tunnels) {
		field := "cloudflare.tunnels." + name
		t := cf.Tunnels[name]
		p.add(checkName(field, name))
		if strings.TrimSpace(t.AccountID) == "" {
			p.addf(field+".account_id", "must not be empty")
		}
		if strings.TrimSpace(t.TunnelID) == "" {
			p.addf(field+".tunnel_id", "must not be empty")
		}
		p.add(checkTunnelCredential(field+".credential", t.Credential, cf, rootTokenMissing))
	}
}

func checkTunnelCredential(field, ref string, cf CloudflareConfig, rootTokenMissing bool) *ValidationError {
	if ref != "" && ref != RootCredential {
		if _, ok := cf.Credentials[ref]; !ok {
			return fail(field, "credential %q is not defined; add cloudflare.credentials.%s or use default (cloudflare.api_token)", ref, ref)
		}
		return nil
	}
	if cf.APIToken == "" && !rootTokenMissing {
		return fail(field, "uses the default credential, but %s is empty; set it or name a credential from cloudflare.credentials", KeyWithEnv("cloudflare.api_token"))
	}
	return nil
}

// checkDefaultTunnel validates default_tunnel: "" or "default" (root tunnel)
// or the name of an entry in cloudflare.tunnels.
func checkDefaultTunnel(cfg Config) *ValidationError {
	ref := cfg.DefaultTunnel
	switch {
	case ref == "":
		return nil
	case ref == RootTunnel:
		if cfg.Cloudflare.TunnelID == "" {
			return fail("default_tunnel", "%q means the tunnel set by %s, which is empty; set it or name a tunnel from cloudflare.tunnels", ref, KeyWithEnv("cloudflare.tunnel_id"))
		}
	default:
		if _, ok := cfg.Cloudflare.Tunnels[ref]; !ok {
			return fail("default_tunnel", "unknown tunnel %q (use default or a name from cloudflare.tunnels)", ref)
		}
	}
	return nil
}

// resolvableTunnel reports whether ref names the root tunnel, a configured
// tunnel, or the ID of either.
func resolvableTunnel(cf CloudflareConfig, ref string) bool {
	if ref == RootTunnel {
		return cf.TunnelID != ""
	}
	if cf.TunnelID != "" && ref == cf.TunnelID {
		return true
	}
	if _, ok := cf.Tunnels[ref]; ok {
		return true
	}
	for _, t := range cf.Tunnels {
		if t.TunnelID == ref {
			return true
		}
	}
	return false
}

func checkTunnelRefs(cf CloudflareConfig, field string, refs []string) *ValidationError {
	for _, ref := range refs {
		if !resolvableTunnel(cf, ref) {
			return fail(field, "unknown tunnel %q (use default for cloudflare.tunnel_id, or a name or tunnel ID from cloudflare.tunnels)", ref)
		}
	}
	return nil
}
