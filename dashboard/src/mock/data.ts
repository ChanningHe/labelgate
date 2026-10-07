// Sample data served by the dev server when the Go API is unreachable.
// Loaded with a dynamic import in DEV builds only, so it never ships.

import type {
  AgentInfo,
  ManagedResource,
  OverviewData,
  ResourceCounts,
  ResourceListResponse,
  AgentListResponse,
} from '../api/client';

const MINUTE_MS = 60_000;
const HOUR_MIN = 60;
const DAY_MIN = 24 * HOUR_MIN;
const loadedAt = Date.now();
const minutesAgo = (m: number) => new Date(loadedAt - m * MINUTE_MS).toISOString();

type Seed = Omit<ManagedResource, 'resource_type' | 'created_at' | 'updated_at'> & { age: number; touched: number };

const seed = (type: ManagedResource['resource_type']) => ({ age, touched, ...r }: Seed): ManagedResource => ({
  ...r,
  resource_type: type,
  created_at: minutesAgo(age),
  updated_at: minutesAgo(touched),
});

const dns: ManagedResource[] = ([
  { id: 'd1', hostname: 'home.example.com', record_type: 'A', content: '203.0.113.24', proxied: true, service_name: 'root', container_name: 'homepage', container_id: '4f2a9c1e7b3d5a60', status: 'active', cleanup_enabled: false, age: 12 * DAY_MIN, touched: 42 },
  { id: 'd2', hostname: 'www.example.com', record_type: 'CNAME', content: 'example.com', proxied: true, service_name: 'www', container_name: 'homepage', container_id: '4f2a9c1e7b3d5a60', status: 'active', cleanup_enabled: false, age: 12 * DAY_MIN, touched: 12 * DAY_MIN },
  { id: 'd3', hostname: 'vpn.example.com', record_type: 'A', content: '203.0.113.24', proxied: false, ttl: 300, service_name: 'vpn', container_name: 'wireguard', container_id: '91be03c4a2f7d1e8', status: 'active', cleanup_enabled: false, age: 30 * DAY_MIN, touched: 42 },
  { id: 'd4', hostname: 'example.com', record_type: 'MX', content: 'mail.provider.com', service_name: 'mail', container_name: 'mailrelay', container_id: '7d1f5e0b9a33c2b4', status: 'active', cleanup_enabled: false, age: 40 * DAY_MIN, touched: 40 * DAY_MIN },
  { id: 'd5', hostname: 'status.example.com', record_type: 'AAAA', content: '2001:db8::24', proxied: true, service_name: 'status', container_name: 'uptime-kuma', container_id: 'c02e7a51d8f6b3a9', agent_id: 'edge-sg', status: 'active', cleanup_enabled: true, age: 3 * DAY_MIN, touched: 5 * HOUR_MIN },
  { id: 'd6', hostname: 'legacy.example.com', record_type: 'A', content: '198.51.100.7', proxied: true, service_name: 'legacy', container_name: 'legacy-web', container_id: 'ab44c9e1f0207d3e', agent_id: 'nas-agent', status: 'orphaned', cleanup_enabled: false, age: 90 * DAY_MIN, touched: 2 * HOUR_MIN },
  { id: 'd7', hostname: 'nas.example.com', record_type: 'A', content: '203.0.113.24', proxied: true, service_name: 'nas', container_name: 'synology-proxy', container_id: 'e83a1c6b4d97f0a2', status: 'error', cleanup_enabled: false, last_error: 'A CNAME record for nas.example.com already exists and was not created by labelgate. Delete it in Cloudflare or choose another hostname.', age: 14, touched: 3 },
] satisfies Seed[]).map(seed('dns'));

const tunnel: ManagedResource[] = ([
  { id: 't1', hostname: 'app.example.com', service: 'http://webapp:80', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'web', container_name: 'webapp', container_id: '1c9d0f3a6e21b8c4', status: 'active', cleanup_enabled: false, age: 20 * DAY_MIN, touched: 20 * DAY_MIN },
  { id: 't2', hostname: 'api.example.com', service: 'http://app:3000', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'api', container_name: 'app', container_id: '5a7e2b0c9d14f6e3', status: 'active', cleanup_enabled: false, age: 20 * DAY_MIN, touched: 6 * HOUR_MIN },
  { id: 't3', hostname: 'grafana.example.com', service: 'http://grafana:3000', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'grafana', container_name: 'grafana', container_id: '0b3f9e7a1c58d2e6', status: 'active', cleanup_enabled: true, age: 8 * DAY_MIN, touched: 8 * DAY_MIN },
  { id: 't4', hostname: 'ssh.example.com', service: 'ssh://dev-server:22', tunnel_id: 'f4c2d9e8-1a7b-4c3d-8e5f-6a9b0c1d2e3f', service_name: 'ssh', container_name: 'dev-server', container_id: '8e6a4c2f0b97a1d5', agent_id: 'edge-sg', status: 'active', cleanup_enabled: false, age: 2 * DAY_MIN, touched: 2 * DAY_MIN },
  { id: 't5', hostname: 'example.com', path: '\\.(jpg|png|css|js)$', service: 'http://static:80', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'static', container_name: 'static', container_id: '3d8b1f6e0a42c9b7', status: 'active', cleanup_enabled: false, age: 15 * DAY_MIN, touched: 15 * DAY_MIN },
  { id: 't6', hostname: 'photos.example.com', service: 'http://immich:2283', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'photos', container_name: 'immich-server', container_id: '6f0c3a9d7e15b2a8', agent_id: 'nas-agent', status: 'orphaned', cleanup_enabled: true, age: 60 * DAY_MIN, touched: 12 },
  { id: 't7', hostname: 'jellyfin.example.com', service: 'http://jellyfin:8096', tunnel_id: 'b1e0a7c2-5d3f-4e8a-9c1b-2f6d8e0a4b7c', service_name: 'media', container_name: 'jellyfin', container_id: '2a5e8c1b4f63d0e9', credential: 'media', status: 'error', cleanup_enabled: false, last_error: 'Cloudflare returned 10000 Authentication error for credential "media". Give the API token the Cloudflare Tunnel: Edit permission.', age: 9, touched: 1 },
] satisfies Seed[]).map(seed('tunnel_ingress'));

const access: ManagedResource[] = ([
  { id: 'a1', hostname: 'grafana.example.com', access_policy_name: 'internal', access_app_name: 'labelgate-internal', access_decision: 'allow', access_app_id: '3f1c8a2e-77b4-4c1d-9e2a-0b6d5f4a1c93', service_name: 'grafana', container_name: 'grafana', container_id: '0b3f9e7a1c58d2e6', status: 'active', cleanup_enabled: true, age: 8 * DAY_MIN, touched: 8 * DAY_MIN },
  { id: 'a2', hostname: 'api.example.com', access_policy_name: 'machine', access_app_name: 'labelgate-machine', access_decision: 'service_auth', access_app_id: '0e7b3c5a-9d1f-4a26-b8e4-3c0f7a9d2e51', service_name: 'api', container_name: 'app', container_id: '5a7e2b0c9d14f6e3', status: 'active', cleanup_enabled: false, age: 20 * DAY_MIN, touched: 6 * HOUR_MIN },
  { id: 'a3', hostname: 'docs.example.com', access_policy_name: 'public', access_app_name: 'labelgate-public', access_decision: 'bypass', access_app_id: 'c55d0e91-3a2b-4f7e-8c1d-6b9a4e2f0d17', service_name: 'docs', container_name: 'docs', container_id: '4b2d6f8a0c31e7d9', status: 'active', cleanup_enabled: false, age: 25 * DAY_MIN, touched: 25 * DAY_MIN },
  { id: 'a4', hostname: 'old-admin.example.com', access_policy_name: 'office', access_app_name: 'Office admin', access_decision: 'allow', access_app_id: '7c2a9e0d-4b5f-4e18-a3d6-1f8b0c7e9a24', service_name: 'oldadmin', container_name: 'old-admin', container_id: '9d4f1b7c3e86a0b2', agent_id: 'nas-agent', status: 'orphaned', cleanup_enabled: false, age: 120 * DAY_MIN, touched: 2 * HOUR_MIN },
  { id: 'a5', hostname: 'secure.company.io', access_policy_name: 'secure', access_app_name: 'labelgate-secure', access_decision: 'allow', service_name: 'secure', container_name: 'vault-ui', container_id: 'a1e9c7b5d302f8e4', agent_id: 'edge-sg', status: 'error', cleanup_enabled: false, last_error: 'Policy "secure" uses decision allow but has no include rule. Add labelgate.access.secure.policy.include.<selector>.', age: 6, touched: 6 },
] satisfies Seed[]).map(seed('access_app'));

const agents: AgentInfo[] = [
  { id: 'edge-sg', name: 'edge-sg', connected: true, public_ip: '198.51.100.42', default_tunnel: 'edge', status: 'active', resource_count: 3, last_seen: minutesAgo(0.2), created_at: minutesAgo(30 * DAY_MIN) },
  { id: 'nas-agent', name: 'nas-agent', connected: false, public_ip: '192.0.2.88', default_tunnel: 'home', status: 'disconnected', resource_count: 3, last_seen: minutesAgo(2 * HOUR_MIN + 4), created_at: minutesAgo(90 * DAY_MIN) },
];

const counts = (rows: ManagedResource[]): ResourceCounts => ({
  total: rows.length,
  active: rows.filter((r) => r.status === 'active').length,
  orphaned: rows.filter((r) => r.status === 'orphaned').length,
  error: rows.filter((r) => r.status === 'error').length,
});

const list = (resources: ManagedResource[]): ResourceListResponse => ({ resources, total: resources.length });

export interface MockData {
  overview: OverviewData;
  dns: ResourceListResponse;
  tunnels: ResourceListResponse;
  access: ResourceListResponse;
  agents: AgentListResponse;
}

export const MOCK: MockData = {
  overview: {
    resources: { dns: counts(dns), tunnel_ingress: counts(tunnel), access_app: counts(access) },
    agents: {
      total: agents.length,
      connected: agents.filter((a) => a.connected).length,
      disconnected: agents.filter((a) => !a.connected).length,
    },
    sync: { last_sync: minutesAgo(0.1), status: 'success', error: '' },
    cloudflare: { reachable: true, last_check: minutesAgo(0.3) },
    version: 'dev',
    uptime: '3d 4h 12m',
    started_at: minutesAgo(3 * DAY_MIN + 4 * HOUR_MIN + 12),
    label_prefix: 'labelgate',
  },
  dns: list(dns),
  tunnels: list(tunnel),
  access: list(access),
  agents: { agents, total: agents.length },
};
