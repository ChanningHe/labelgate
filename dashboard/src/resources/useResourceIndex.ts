import { useMemo } from 'react';
import type { ManagedResource } from '../api/client';
import { useResources } from '../hooks/useAPI';
import type { KindId } from './meta';

// Cross-kind lookups by hostname. A tunnel shows the Access app protecting its
// hostname, and an Access app shows which DNS or tunnel service it guards.
export interface ResourceIndex {
  byHostname: Record<KindId, ReadonlyMap<string, ManagedResource>>;
}

const toMap = (rows?: readonly ManagedResource[]) => new Map((rows ?? []).map((r) => [r.hostname, r]));

export function buildResourceIndex(lists: Partial<Record<KindId, readonly ManagedResource[]>>): ResourceIndex {
  return {
    byHostname: { dns: toMap(lists.dns), tunnel: toMap(lists.tunnel), access: toMap(lists.access) },
  };
}

export function useResourceIndex() {
  const dns = useResources('dns');
  const tunnel = useResources('tunnel');
  const access = useResources('access');

  const index = useMemo(
    () =>
      buildResourceIndex({
        dns: dns.data?.resources,
        tunnel: tunnel.data?.resources,
        access: access.data?.resources,
      }),
    [dns.data, tunnel.data, access.data],
  );

  return { index, lists: { dns, tunnel, access } };
}
