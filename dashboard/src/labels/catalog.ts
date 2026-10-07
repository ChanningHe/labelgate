// Copy-ready label recipes and the property reference shown on the Labels page
// and in each resource page's examples drawer. Keep in sync with pkg/labels
// and docsite/content/docs/en/labels/*.mdx.

import type { LabelLine, LabelType } from './format';

export interface LabelRecipe {
  id: string;
  kind: LabelType;
  title: string;
  description: string;
  image: string;
  lines: LabelLine[];
}

export interface LabelProperty {
  name: string;
  description: string;
  defaultValue?: string;
  isRequired?: boolean;
}

// Mirrors serviceNamePattern and reservedNames in pkg/labels/parser.go.
export const SERVICE_NAME_PATTERN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/;
export const RESERVED_SERVICE_NAMES: readonly string[] = [
  'hostname', 'service', 'type', 'target', 'proxied', 'ttl', 'cleanup', 'credential', 'tunnel',
  'path', 'origin', 'access', 'default', 'priority', 'weight', 'port', 'flags', 'tag', 'comment',
];

type Entry = [key: string, value: string] | string;

const lines = (...entries: Entry[]): LabelLine[] =>
  entries.map((e) => (typeof e === 'string' ? { comment: e } : { key: e[0], value: e[1] }));

const recipe = (
  id: string,
  kind: LabelType,
  title: string,
  description: string,
  image: string,
  ...entries: Entry[]
): LabelRecipe => ({ id, kind, title, description, image, lines: lines(...entries) });

export const RECIPES: readonly LabelRecipe[] = [
  recipe('dns-auto', 'dns', 'Point a hostname at this host',
    "Proxied A record. target: auto looks up the Docker host's public IP and updates the record when it changes.",
    'nginx:alpine',
    ['dns.web.hostname', 'home.example.com'], ['dns.web.target', 'auto']),
  recipe('dns-only', 'dns', 'DNS only, no proxy',
    'Grey-cloud record with a fixed TTL, for traffic Cloudflare cannot proxy such as WireGuard or game servers.',
    'lscr.io/linuxserver/wireguard',
    ['dns.vpn.hostname', 'vpn.example.com'], ['dns.vpn.target', 'auto'], ['dns.vpn.proxied', 'false'], ['dns.vpn.ttl', '300']),
  recipe('dns-cname', 'dns', 'Alias with CNAME',
    'Point one name at another. Cloudflare flattens it at the zone apex.',
    'nginx:alpine',
    ['dns.www.hostname', 'www.example.com'], ['dns.www.type', 'CNAME'], ['dns.www.target', 'example.com']),
  recipe('dns-mx', 'dns', 'Mail exchange',
    'MX records need a priority. Lower values are tried first.',
    'mailrelay:latest',
    ['dns.mail.hostname', 'example.com'], ['dns.mail.type', 'MX'], ['dns.mail.target', 'mail.provider.com'], ['dns.mail.priority', '10']),
  recipe('dns-txt', 'dns', 'SPF or verification text',
    'TXT values are sent as written. Quote them in compose so YAML keeps the spaces.',
    'mailrelay:latest',
    ['dns.spf.hostname', 'example.com'], ['dns.spf.type', 'TXT'], ['dns.spf.target', 'v=spf1 include:_spf.google.com ~all']),
  recipe('dns-srv', 'dns', 'Service record (SRV)',
    'SRV records need priority, weight and port. The hostname carries the _service._proto prefix.',
    'prosody/prosody',
    ['dns.xmpp.hostname', '_xmpp._tcp.example.com'], ['dns.xmpp.type', 'SRV'], ['dns.xmpp.target', 'xmpp.example.com'],
    ['dns.xmpp.priority', '10'], ['dns.xmpp.weight', '5'], ['dns.xmpp.port', '5222']),
  recipe('dns-preview', 'dns', 'Short-lived preview',
    'cleanup: true deletes the record once the container has been gone for sync.remove_delay. The comment shows in the Cloudflare dashboard.',
    'preview:pr-128',
    ['dns.preview.hostname', 'pr-128.example.com'], ['dns.preview.target', 'auto'], ['dns.preview.cleanup', 'true'],
    ['dns.preview.comment', 'PR #128 preview']),

  recipe('tun-basic', 'tunnel', 'Expose a web app',
    'A public hostname and the address cloudflared uses to reach the container. Cloudflare creates the tunnel CNAME for you.',
    'nginx:alpine',
    ['tunnel.web.hostname', 'app.example.com'], ['tunnel.web.service', 'http://webapp:80']),
  recipe('tun-multi', 'tunnel', 'Two hostnames, one container',
    'Each service name is its own ingress rule, so one container can serve a site and an API.',
    'myapp:latest',
    ['tunnel.web.hostname', 'www.example.com'], ['tunnel.web.service', 'http://app:80'],
    ['tunnel.api.hostname', 'api.example.com'], ['tunnel.api.service', 'http://app:3000']),
  recipe('tun-ssh', 'tunnel', 'SSH through the tunnel',
    'Clients connect with cloudflared access ssh. Pair it with an Access policy.',
    'linuxserver/openssh-server',
    ['tunnel.ssh.hostname', 'ssh.example.com'], ['tunnel.ssh.service', 'ssh://dev-server:22']),
  recipe('tun-path', 'tunnel', 'Route by path',
    'path is a regular expression matched against the request path. Only static assets reach this container.',
    'nginx:alpine',
    ['tunnel.static.hostname', 'example.com'], ['tunnel.static.service', 'http://static:80'],
    ['tunnel.static.path', '\\.(jpg|png|css|js)$']),
  recipe('tun-https', 'tunnel', 'HTTPS origin, self-signed cert',
    'Skips certificate checks between cloudflared and the container. Prefer origin_server_name or ca_pool when you have them.',
    'synology-proxy:latest',
    ['tunnel.nas.hostname', 'nas.example.com'], ['tunnel.nas.service', 'https://nas:5001'],
    ['tunnel.nas.origin.no_tls_verify', 'true']),
  recipe('tun-named', 'tunnel', 'Another tunnel and account',
    'tunnel and credential pick names defined in config.yaml instead of the defaults.',
    'internal-app:latest',
    ['tunnel.app.hostname', 'internal.company.io'], ['tunnel.app.service', 'http://internal:8080'],
    ['tunnel.app.tunnel', 'secondary'], ['tunnel.app.credential', 'company']),

  recipe('acc-emails', 'access', 'Allow a list of people',
    'Define the policy once, then attach it to a service with access.',
    'admin:latest',
    ['access.team.policy.decision', 'allow'], ['access.team.policy.include.emails', 'alice@example.com,bob@example.com'],
    ['access.team.session_duration', '12h'],
    '# Attach the policy to a service',
    ['tunnel.admin.hostname', 'admin.example.com'], ['tunnel.admin.service', 'http://admin:8080'], ['tunnel.admin.access', 'team']),
  recipe('acc-domain', 'access', 'Everyone at your company',
    'Any address ending in the domain can sign in.',
    'myapp:latest',
    ['access.company.policy.decision', 'allow'], ['access.company.policy.include.emails_ending_in', '@company.io'],
    ['tunnel.app.hostname', 'app.company.io'], ['tunnel.app.service', 'http://app:80'], ['tunnel.app.access', 'company']),
  recipe('acc-ip', 'access', 'Office network only',
    'Requests from these ranges are allowed. Separate ranges with commas.',
    'internal:latest',
    ['access.office.policy.decision', 'allow'], ['access.office.policy.include.ip_ranges', '203.0.113.0/24,198.51.100.0/24'],
    ['tunnel.internal.hostname', 'internal.example.com'], ['tunnel.internal.service', 'http://internal:3000'],
    ['tunnel.internal.access', 'office']),
  recipe('acc-bypass', 'access', 'Public page, no login',
    'bypass skips authentication. everyone takes no value.',
    'docs:latest',
    ['access.public.policy.decision', 'bypass'], ['access.public.policy.include.everyone', ''],
    ['tunnel.docs.hostname', 'docs.example.com'], ['tunnel.docs.service', 'http://docs:80'], ['tunnel.docs.access', 'public']),
  recipe('acc-token', 'access', 'Machine access with a service token',
    'service_auth accepts a Cloudflare service token or mTLS certificate instead of a login.',
    'api:latest',
    ['access.machine.policy.decision', 'service_auth'], ['access.machine.policy.include.service_token', 'your-service-token-id'],
    ['tunnel.api.hostname', 'api.example.com'], ['tunnel.api.service', 'http://api:8080'], ['tunnel.api.access', 'machine']),
  recipe('acc-combined', 'access', 'Include, require and exclude',
    'Company emails, only from the US, minus two contractors. Include is OR, require is AND, exclude is NOT.',
    'vault-ui:latest',
    ['access.secure.policy.decision', 'allow'], ['access.secure.policy.include.emails_ending_in', '@company.io'],
    ['access.secure.policy.require.country', 'US'],
    ['access.secure.policy.exclude.emails', 'contractor1@company.io,contractor2@company.io'],
    ['tunnel.secure.hostname', 'secure.company.io'], ['tunnel.secure.service', 'https://secure:443'],
    ['tunnel.secure.access', 'secure']),
];

const prop = (name: string, description: string, defaultValue?: string, isRequired = false): LabelProperty => ({
  name,
  description,
  defaultValue,
  isRequired,
});

export const LABEL_PROPERTIES: Record<LabelType, readonly LabelProperty[]> = {
  dns: [
    prop('hostname', 'Full domain name for the record', undefined, true),
    prop('type', 'A, AAAA, CNAME, TXT, MX, SRV, CAA', 'A'),
    prop('target', 'Record value. auto = host public IP, container = container IP', 'auto'),
    prop('proxied', 'Orange cloud on or off', 'true'),
    prop('ttl', 'Seconds. Ignored while proxied', 'auto'),
    prop('priority', 'Required for MX and SRV'),
    prop('weight / port', 'Required for SRV'),
    prop('flags / tag', 'CAA flags and tag'),
    prop('comment', 'Shown in the Cloudflare dashboard'),
    prop('credential', 'Credential name from config', 'default'),
    prop('cleanup', 'Delete after the container is gone', 'false'),
    prop('access', 'Access policy name to apply'),
  ],
  tunnel: [
    prop('hostname', 'Public hostname', undefined, true),
    prop('service', 'protocol://host:port the tunnel forwards to', undefined, true),
    prop('path', 'Regex matched against the request path'),
    prop('tunnel', 'Tunnel name from config', 'default'),
    prop('credential', 'Credential name from config', 'default'),
    prop('cleanup', 'Delete the ingress rule after the container is gone', 'false'),
    prop('access', 'Access policy name to apply'),
    prop('origin.*', 'connect_timeout, no_tls_verify, http_host_header, …'),
  ],
  access: [
    prop('policy.decision', 'allow, block, bypass, service_auth', 'allow'),
    prop('policy.include.<selector>', 'Any rule matches (OR)'),
    prop('policy.require.<selector>', 'Every rule must match (AND)'),
    prop('policy.exclude.<selector>', 'Matching requests are left out (NOT)'),
    prop('policy.name', 'Policy name in the Cloudflare dashboard'),
    prop('app_name', 'Access application name', 'labelgate-<name>'),
    prop('session_duration', 'How long a login lasts', '24h'),
  ],
};

export const PROPERTY_VALUES: Record<LabelType, { title: string; values: readonly string[] }> = {
  dns: { title: 'Special targets', values: ['auto', 'container', '<ipv4>', '<ipv6>', '<hostname>'] },
  tunnel: {
    title: 'Service protocols',
    values: ['http://', 'https://', 'ssh://', 'rdp://', 'tcp://', 'udp://', 'unix:', 'hello_world', 'http_status:404'],
  },
  access: {
    title: 'Rule selectors',
    values: ['emails', 'emails_ending_in', 'ip_ranges', 'country', 'everyone', 'service_token', 'access_groups', 'certificate', 'login_methods'],
  },
};

export const recipesFor = (kind: LabelType): LabelRecipe[] => RECIPES.filter((r) => r.kind === kind);
