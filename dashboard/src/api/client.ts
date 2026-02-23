// API client for Labelgate dashboard.
// In dev mode, Vite proxy forwards /api to the Go backend.
// In production, the Go binary serves both /api and /dashboard.

const API_BASE = '/api';

// Token can be set for authenticated API access.
let authToken: string | null = null;

export function setAuthToken(token: string) {
  authToken = token;
}

async function fetchAPI<T>(path: string, params?: Record<string, string>): Promise<T> {
  const url = new URL(API_BASE + path, window.location.origin);
  if (params) {
    Object.entries(params).forEach(([k, v]) => {
      if (v) url.searchParams.set(k, v);
    });
  }

  const headers: Record<string, string> = {
    'Accept': 'application/json',
  };
  if (authToken) {
    headers['Authorization'] = `Bearer ${authToken}`;
  }

  const res = await fetch(url.toString(), { headers });
  if (!res.ok) {
    throw new Error(`API error: ${res.status} ${res.statusText}`);
  }
  return res.json();
}

// --- API Types ---

export interface ResourceCounts {
  total: number;
  active: number;
  orphaned: number;
  error: number;
}

export interface OverviewData {
  resources: {
    dns: ResourceCounts;
    tunnel_ingress: ResourceCounts;
    access_app: ResourceCounts;
  };
  agents: {
    total: number;
    connected: number;
    disconnected: number;
  };
  sync: {
    last_sync: string;
    status: 'success' | 'error';
    error: string;
  };
  cloudflare: {
    reachable: boolean;
    last_check: string;
  };
  version: string;
  uptime: string;
  started_at: string;
}

export interface ManagedResource {
  id: string;
  resource_type: string;
  cf_id?: string;
  zone_id?: string;
  hostname: string;
  record_type?: string;
  content?: string;
  proxied?: boolean;
  ttl?: number;
  tunnel_id?: string;
  service?: string;
  path?: string;
  access_app_id?: string;
  account_id?: string;
  container_id: string;
  container_name: string;
  service_name: string;
  agent_id: string;
  status: string;
  cleanup_enabled: boolean;
  last_error?: string;
  created_at: string;
  updated_at: string;
}

export interface ResourceListResponse {
  resources: ManagedResource[];
  total: number;
}

export interface AgentInfo {
  id: string;
  name: string;
  connected: boolean;
  last_seen: string | null;
  public_ip: string;
  default_tunnel: string;
  status: string;
  resource_count: number;
  created_at: string;
}

export interface AgentListResponse {
  agents: AgentInfo[];
  total: number;
}

// --- API Functions ---

export function fetchOverview() {
  return fetchAPI<OverviewData>('/overview');
}

export function fetchDNS(params?: Record<string, string>) {
  return fetchAPI<ResourceListResponse>('/resources/dns', params);
}

export function fetchTunnels(params?: Record<string, string>) {
  return fetchAPI<ResourceListResponse>('/resources/tunnels', params);
}

export function fetchAccess(params?: Record<string, string>) {
  return fetchAPI<ResourceListResponse>('/resources/access', params);
}

export function fetchAgents() {
  return fetchAPI<AgentListResponse>('/agents');
}
