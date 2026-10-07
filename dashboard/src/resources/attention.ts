import type { ManagedResource, ResourceStatus } from '../api/client';
import { KIND_IDS, type KindId } from './meta';

// Statuses that need a human to look at them, most urgent first.
const ATTENTION_RANK: Partial<Record<ResourceStatus, number>> = {
  error: 0,
  orphaned: 1,
  pending_cleanup: 1,
};

export interface AttentionItem {
  kind: KindId;
  resource: ManagedResource;
}

/** A sentence explaining why the resource is not healthy, or null when it is. */
export function describeProblem(r: ManagedResource, noun: string): string | null {
  if (r.status === 'error') {
    return r.last_error || `Cloudflare rejected this ${noun}. Check the labelgate logs for details.`;
  }
  if (r.status !== 'orphaned' && r.status !== 'pending_cleanup') return null;

  const who = r.container_name ? `Container ${r.container_name}` : 'The container';
  return r.cleanup_enabled
    ? `${who} is gone. This ${noun} will be deleted after sync.remove_delay.`
    : `${who} is gone. The ${noun} stays on Cloudflare because cleanup is off.`;
}

export function collectAttention(lists: Partial<Record<KindId, readonly ManagedResource[]>>): AttentionItem[] {
  return KIND_IDS.flatMap((kind) =>
    (lists[kind] ?? []).filter((r) => ATTENTION_RANK[r.status] !== undefined).map((resource) => ({ kind, resource })),
  ).sort(
    (a, b) =>
      (ATTENTION_RANK[a.resource.status] ?? 0) - (ATTENTION_RANK[b.resource.status] ?? 0) ||
      Date.parse(b.resource.updated_at) - Date.parse(a.resource.updated_at),
  );
}
