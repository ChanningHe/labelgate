import useSWR from 'swr';
import {
  fetchOverview,
  fetchDNS,
  fetchTunnels,
  fetchAccess,
  fetchAgents,
  type OverviewData,
  type ResourceListResponse,
  type AgentListResponse,
} from '../api/client';

// Default polling interval: 10 seconds
const POLL_INTERVAL = 10_000;

export function useOverview() {
  return useSWR<OverviewData>('/api/overview', fetchOverview, {
    refreshInterval: POLL_INTERVAL,
    revalidateOnFocus: true,
  });
}

export function useDNS(params?: Record<string, string>) {
  const key = params
    ? `/api/resources/dns?${new URLSearchParams(params).toString()}`
    : '/api/resources/dns';
  return useSWR<ResourceListResponse>(key, () => fetchDNS(params), {
    refreshInterval: POLL_INTERVAL,
    revalidateOnFocus: true,
  });
}

export function useTunnels(params?: Record<string, string>) {
  const key = params
    ? `/api/resources/tunnels?${new URLSearchParams(params).toString()}`
    : '/api/resources/tunnels';
  return useSWR<ResourceListResponse>(key, () => fetchTunnels(params), {
    refreshInterval: POLL_INTERVAL,
    revalidateOnFocus: true,
  });
}

export function useAccess(params?: Record<string, string>) {
  const key = params
    ? `/api/resources/access?${new URLSearchParams(params).toString()}`
    : '/api/resources/access';
  return useSWR<ResourceListResponse>(key, () => fetchAccess(params), {
    refreshInterval: POLL_INTERVAL,
    revalidateOnFocus: true,
  });
}

export function useAgents() {
  return useSWR<AgentListResponse>('/api/agents', fetchAgents, {
    refreshInterval: POLL_INTERVAL,
    revalidateOnFocus: true,
  });
}
