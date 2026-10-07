import useSWR, { type SWRConfiguration } from 'swr';
import {
  UnauthorizedError,
  fetchAccess,
  fetchAgents,
  fetchDNS,
  fetchOverview,
  fetchTunnels,
  type AgentListResponse,
  type OverviewData,
  type ResourceListResponse,
} from '../api/client';
import type { MockData } from '../mock/data';
import type { KindId } from '../resources/meta';

const POLL_INTERVAL_MS = 10_000;
const POLLING: SWRConfiguration = { refreshInterval: POLL_INTERVAL_MS, revalidateOnFocus: true };

// `yarn dev` without a running Go backend serves sample data so the UI can be
// worked on. Production builds return the real fetcher and drop the mock chunk.
function withDevFallback<T>(fetcher: () => Promise<T>, pick: (mock: MockData) => T): () => Promise<T> {
  if (!import.meta.env.DEV) return fetcher;
  return async () => {
    try {
      return await fetcher();
    } catch (err) {
      if (err instanceof UnauthorizedError) throw err;
      const { MOCK } = await import('../mock/data');
      return pick(MOCK);
    }
  };
}

const overviewFetcher = withDevFallback(fetchOverview, (m) => m.overview);
const agentsFetcher = withDevFallback(fetchAgents, (m) => m.agents);

const RESOURCE_SOURCES: Record<KindId, { key: string; fetcher: () => Promise<ResourceListResponse> }> = {
  dns: { key: '/api/resources/dns', fetcher: withDevFallback(fetchDNS, (m) => m.dns) },
  tunnel: { key: '/api/resources/tunnels', fetcher: withDevFallback(fetchTunnels, (m) => m.tunnels) },
  access: { key: '/api/resources/access', fetcher: withDevFallback(fetchAccess, (m) => m.access) },
};

export function useOverview() {
  return useSWR<OverviewData>('/api/overview', overviewFetcher, POLLING);
}

export function useResources(kind: KindId) {
  const { key, fetcher } = RESOURCE_SOURCES[kind];
  return useSWR<ResourceListResponse>(key, fetcher, POLLING);
}

export function useAgents() {
  return useSWR<AgentListResponse>('/api/agents', agentsFetcher, POLLING);
}

// Matches labels.DefaultPrefix in pkg/labels; used until the server answers.
export const DEFAULT_LABEL_PREFIX = 'labelgate';

export function useLabelPrefix(): string {
  const { data } = useOverview();
  return data?.label_prefix || DEFAULT_LABEL_PREFIX;
}
