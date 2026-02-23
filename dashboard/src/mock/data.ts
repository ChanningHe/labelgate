// Mock data for UI development. Will be replaced with real API calls.

export interface ResourceBase {
  id: string;
  hostname: string;
  status: 'active' | 'orphaned' | 'pending_cleanup' | 'deleted' | 'error';
  container_id: string;
  container_name: string;
  service_name: string;
  agent_id: string;
  cleanup_enabled: boolean;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface DNSResource extends ResourceBase {
  resource_type: 'dns';
  zone_id: string;
  record_type: string;
  content: string;
  proxied: boolean;
  ttl: number;
}

export interface TunnelResource extends ResourceBase {
  resource_type: 'tunnel_ingress';
  tunnel_id: string;
  service: string;
  path: string;
}

export interface AccessResource extends ResourceBase {
  resource_type: 'access_app';
  access_app_id: string;
  account_id: string;
  app_name: string;
  policies: string[];
}

export interface AgentInfo {
  id: string;
  name: string;
  connected: boolean;
  last_seen: string;
  public_ip: string;
  default_tunnel: string;
  status: 'active' | 'disconnected' | 'removed';
  resource_count: number;
  created_at: string;
}

export interface OverviewData {
  resources: {
    dns: { total: number; active: number; orphaned: number; error: number };
    tunnel_ingress: { total: number; active: number; orphaned: number; error: number };
    access_app: { total: number; active: number; orphaned: number; error: number };
  };
  agents: { total: number; connected: number; disconnected: number };
  sync: { last_sync: string; status: 'success' | 'error'; error: string };
  cloudflare: { reachable: boolean; last_check: string };
  version: string;
  uptime: string;
  started_at: string;
}

// --- Mock data ---

export const mockOverview: OverviewData = {
  resources: {
    dns: { total: 9, active: 6, orphaned: 2, error: 1 },
    tunnel_ingress: { total: 6, active: 5, orphaned: 0, error: 1 },
    access_app: { total: 4, active: 3, orphaned: 0, error: 1 },
  },
  agents: { total: 3, connected: 2, disconnected: 1 },
  sync: {
    last_sync: '2026-02-08T14:32:00Z',
    status: 'success',
    error: '',
  },
  cloudflare: {
    reachable: true,
    last_check: '2026-02-08T14:32:00Z',
  },
  version: '0.1.0',
  uptime: '3d 5h 20m',
  started_at: '2026-02-05T09:12:00Z',
};

export const mockDNSResources: DNSResource[] = [
  {
    id: 'dns-001',
    hostname: 'app.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.10',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'web',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2026-02-01T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'dns-002',
    hostname: 'api.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.10',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'api',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2026-02-01T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'dns-003',
    hostname: 'docs.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'CNAME',
    content: 'app.example.com',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-def456',
    container_name: 'docsite',
    service_name: 'docs',
    agent_id: '',
    cleanup_enabled: true,
    created_at: '2026-02-02T08:00:00Z',
    updated_at: '2026-02-08T12:00:00Z',
  },
  {
    id: 'dns-004',
    hostname: 'mail.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'MX',
    content: 'mail.provider.com',
    proxied: false,
    ttl: 3600,
    container_id: 'ctr-ghi789',
    container_name: 'mailserver',
    service_name: 'mail',
    agent_id: 'agent-host-1',
    cleanup_enabled: false,
    created_at: '2026-01-15T06:00:00Z',
    updated_at: '2026-02-07T18:00:00Z',
  },
  {
    id: 'dns-005',
    hostname: 'legacy.example.com',
    status: 'orphaned',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.50',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-old001',
    container_name: 'legacy-app',
    service_name: 'legacy',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2025-12-01T10:00:00Z',
    updated_at: '2026-01-20T14:00:00Z',
  },
  {
    id: 'dns-006',
    hostname: 'staging.example.com',
    status: 'orphaned',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.60',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-old002',
    container_name: 'staging-app',
    service_name: 'staging',
    agent_id: 'agent-host-2',
    cleanup_enabled: true,
    created_at: '2026-01-10T10:00:00Z',
    updated_at: '2026-02-01T10:00:00Z',
  },
  {
    id: 'dns-007',
    hostname: 'grafana.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.10',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-mon001',
    container_name: 'monitoring',
    service_name: 'grafana',
    agent_id: 'agent-host-1',
    cleanup_enabled: false,
    created_at: '2026-02-03T08:00:00Z',
    updated_at: '2026-02-08T10:00:00Z',
  },
  {
    id: 'dns-008',
    hostname: 'prometheus.example.com',
    status: 'active',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.10',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-mon001',
    container_name: 'monitoring',
    service_name: 'prometheus',
    agent_id: 'agent-host-1',
    cleanup_enabled: false,
    created_at: '2026-02-03T08:00:00Z',
    updated_at: '2026-02-08T10:00:00Z',
  },
  {
    id: 'dns-009',
    hostname: 'broken.example.com',
    status: 'error',
    resource_type: 'dns',
    zone_id: 'zone-abc123',
    record_type: 'A',
    content: '203.0.113.99',
    proxied: true,
    ttl: 1,
    container_id: 'ctr-broken01',
    container_name: 'broken-app',
    service_name: 'web',
    agent_id: '',
    cleanup_enabled: false,
    last_error: 'failed to create DNS record: Cloudflare API error: authentication token expired',
    created_at: '2026-02-08T12:00:00Z',
    updated_at: '2026-02-08T14:30:00Z',
  },
];

export const mockTunnelResources: TunnelResource[] = [
  {
    id: 'tun-001',
    hostname: 'app.example.com',
    status: 'active',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-default-001',
    service: 'http://webapp:8080',
    path: '',
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'web',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2026-02-01T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'tun-002',
    hostname: 'api.example.com',
    status: 'active',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-default-001',
    service: 'http://webapp:3000',
    path: '',
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'api',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2026-02-01T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'tun-003',
    hostname: 'grafana.example.com',
    status: 'active',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-default-001',
    service: 'http://grafana:3000',
    path: '',
    container_id: 'ctr-mon001',
    container_name: 'monitoring',
    service_name: 'grafana',
    agent_id: 'agent-host-1',
    cleanup_enabled: false,
    created_at: '2026-02-03T08:00:00Z',
    updated_at: '2026-02-08T10:00:00Z',
  },
  {
    id: 'tun-004',
    hostname: 'ssh.example.com',
    status: 'active',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-secondary-001',
    service: 'ssh://localhost:22',
    path: '',
    container_id: 'ctr-ssh001',
    container_name: 'ssh-gateway',
    service_name: 'ssh',
    agent_id: 'agent-host-2',
    cleanup_enabled: false,
    created_at: '2026-02-05T12:00:00Z',
    updated_at: '2026-02-08T08:00:00Z',
  },
  {
    id: 'tun-005',
    hostname: 'app.example.com',
    status: 'active',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-default-001',
    service: 'http://webapp:8080',
    path: '/static/.*',
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'static',
    agent_id: '',
    cleanup_enabled: true,
    created_at: '2026-02-06T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'tun-006',
    hostname: 'internal.example.com',
    status: 'error',
    resource_type: 'tunnel_ingress',
    tunnel_id: 'tun-default-001',
    service: 'http://internal-svc:9090',
    path: '',
    container_id: 'ctr-int001',
    container_name: 'internal-service',
    service_name: 'internal',
    agent_id: 'agent-host-2',
    cleanup_enabled: false,
    last_error: 'failed to update tunnel configuration: tunnel credential not found: default',
    created_at: '2026-02-08T11:00:00Z',
    updated_at: '2026-02-08T14:25:00Z',
  },
];

export const mockAccessResources: AccessResource[] = [
  {
    id: 'acc-001',
    hostname: 'grafana.example.com',
    status: 'active',
    resource_type: 'access_app',
    access_app_id: 'cf-app-001',
    account_id: 'acct-001',
    app_name: 'Grafana',
    policies: ['Allow: admin@example.com', 'Allow: *@example.com'],
    container_id: 'ctr-mon001',
    container_name: 'monitoring',
    service_name: 'grafana',
    agent_id: 'agent-host-1',
    cleanup_enabled: false,
    created_at: '2026-02-03T08:00:00Z',
    updated_at: '2026-02-08T10:00:00Z',
  },
  {
    id: 'acc-002',
    hostname: 'ssh.example.com',
    status: 'active',
    resource_type: 'access_app',
    access_app_id: 'cf-app-002',
    account_id: 'acct-001',
    app_name: 'SSH Gateway',
    policies: ['Allow: admin@example.com'],
    container_id: 'ctr-ssh001',
    container_name: 'ssh-gateway',
    service_name: 'ssh',
    agent_id: 'agent-host-2',
    cleanup_enabled: false,
    created_at: '2026-02-05T12:00:00Z',
    updated_at: '2026-02-08T08:00:00Z',
  },
  {
    id: 'acc-003',
    hostname: 'api.example.com',
    status: 'active',
    resource_type: 'access_app',
    access_app_id: 'cf-app-003',
    account_id: 'acct-001',
    app_name: 'API Service Auth',
    policies: ['Service Auth: api-token-xyz'],
    container_id: 'ctr-abc123',
    container_name: 'webapp',
    service_name: 'api',
    agent_id: '',
    cleanup_enabled: false,
    created_at: '2026-02-01T10:00:00Z',
    updated_at: '2026-02-08T14:00:00Z',
  },
  {
    id: 'acc-004',
    hostname: 'admin.example.com',
    status: 'error',
    resource_type: 'access_app',
    access_app_id: '',
    account_id: 'acct-001',
    app_name: 'Admin Panel',
    policies: ['Allow: admin@example.com'],
    container_id: 'ctr-admin01',
    container_name: 'admin-panel',
    service_name: 'admin',
    agent_id: '',
    cleanup_enabled: false,
    last_error: 'access application "Admin" (ID: cf-app-existing) already exists for hostname admin.example.com, not managed by labelgate — refusing to overwrite',
    created_at: '2026-02-08T13:00:00Z',
    updated_at: '2026-02-08T14:28:00Z',
  },
];

export const mockAgents: AgentInfo[] = [
  {
    id: 'agent-host-1',
    name: 'Docker Host 1',
    connected: true,
    last_seen: '2026-02-08T14:32:00Z',
    public_ip: '203.0.113.10',
    default_tunnel: 'default',
    status: 'active',
    resource_count: 4,
    created_at: '2026-01-01T00:00:00Z',
  },
  {
    id: 'agent-host-2',
    name: 'Docker Host 2',
    connected: true,
    last_seen: '2026-02-08T14:31:55Z',
    public_ip: '203.0.113.20',
    default_tunnel: 'secondary',
    status: 'active',
    resource_count: 2,
    created_at: '2026-01-15T00:00:00Z',
  },
  {
    id: 'agent-host-3',
    name: 'Docker Host 3 (Staging)',
    connected: false,
    last_seen: '2026-02-07T18:00:00Z',
    public_ip: '203.0.113.30',
    default_tunnel: 'default',
    status: 'disconnected',
    resource_count: 1,
    created_at: '2026-02-01T00:00:00Z',
  },
];
