// API client for Labelgate dashboard.
// In dev mode, Vite proxy forwards /api to the Go backend.
// In production, the Go binary serves both /api and /dashboard.

import { liveStatus } from './liveStatus';
import { tokenStore } from './token';

const API_BASE = '/api';

export class ApiError extends Error {
  readonly status: number;

  constructor(status: number, statusText: string) {
    super(`API error: ${status} ${statusText}`);
    this.name = 'ApiError';
    this.status = status;
  }
}

// Thrown when the server has api.token set and the request carried no token
// or a wrong one. The app reacts by asking the user for a token.
export class UnauthorizedError extends ApiError {
  constructor(statusText: string) {
    super(401, statusText);
    this.name = 'UnauthorizedError';
  }
}

function buildURL(path: string, params?: Record<string, string>): string {
  const query = new URLSearchParams(
    Object.entries(params ?? {}).filter(([, v]) => v !== ''),
  ).toString();
  return `${API_BASE}${path}${query ? `?${query}` : ''}`;
}

function headersFor(token: string | null): Record<string, string> {
  return token
    ? { Accept: 'application/json', Authorization: `Bearer ${token}` }
    : { Accept: 'application/json' };
}

function toError(res: Response): ApiError {
  return res.status === 401 ? new UnauthorizedError(res.statusText) : new ApiError(res.status, res.statusText);
}

export async function fetchAPI<T>(path: string, params?: Record<string, string>): Promise<T> {
  let res: Response;
  try {
    res = await fetch(buildURL(path, params), { headers: headersFor(tokenStore.read()) });
  } catch (err) {
    liveStatus.reportFailure();
    throw err;
  }
  if (!res.ok) {
    liveStatus.reportFailure();
    throw toError(res);
  }
  const data: T = await res.json();
  liveStatus.reportSuccess();
  return data;
}

// verifyToken checks a candidate token against an authenticated endpoint
// without touching the stored token.
export async function verifyToken(token: string): Promise<boolean> {
  const res = await fetch(buildURL('/version'), { headers: headersFor(token.trim()) });
  if (res.ok) return true;
  if (res.status === 401) return false;
  throw toError(res);
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
  label_prefix: string;
}

// Mirrors storage.ResourceStatus. pending_cleanup is defined server-side but
// not currently assigned; orphaned covers both cleanup on and off.
export type ResourceStatus = 'active' | 'orphaned' | 'pending_cleanup' | 'deleted' | 'error';

// Mirrors storage.ResourceType.
export type StorageResourceType = 'dns' | 'tunnel_ingress' | 'access_app';

// Mirrors storage.ManagedResource; omitempty fields are optional.
export interface ManagedResource {
  id: string;
  resource_type: StorageResourceType;
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
  access_app_name?: string;
  access_policy_name?: string;
  access_decision?: string;
  container_id?: string;
  container_name?: string;
  service_name: string;
  agent_id?: string;
  credential?: string;
  status: ResourceStatus;
  cleanup_enabled: boolean;
  last_error?: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface ResourceListResponse {
  resources: ManagedResource[];
  total: number;
}

// Mirrors agentResponse in internal/api/agents.go.
export interface AgentInfo {
  id: string;
  name?: string;
  connected: boolean;
  last_seen?: string;
  public_ip?: string;
  default_tunnel?: string;
  status: 'active' | 'disconnected' | 'removed';
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

export function fetchDNS() {
  return fetchAPI<ResourceListResponse>('/resources/dns');
}

export function fetchTunnels() {
  return fetchAPI<ResourceListResponse>('/resources/tunnels');
}

export function fetchAccess() {
  return fetchAPI<ResourceListResponse>('/resources/access');
}

export function fetchAgents() {
  return fetchAPI<AgentListResponse>('/agents');
}
