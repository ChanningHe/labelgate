import type { OverviewData, SyncProblem } from '../api/client';

export type SyncTone = 'ok' | 'err' | 'off';

export interface SyncDisplay {
  label: string;
  tone: SyncTone;
}

/** Maps the overview sync status to a label and tone; pending is neutral. */
export function syncDisplay(status: OverviewData['sync']['status']): SyncDisplay {
  switch (status) {
    case 'success':
      return { label: 'Succeeded', tone: 'ok' };
    case 'pending':
      return { label: 'Pending', tone: 'off' };
    default:
      return { label: 'Failed', tone: 'err' };
  }
}

/** The server sends at most this many problems; the client never shows more. */
export const MAX_SHOWN_PROBLEMS = 200;

export interface SyncProblemList {
  items: SyncProblem[];
  /** Problems that exist but are not in items. */
  more: number;
}

/** The problems of a failed sync to list, and how many are left out. */
export function syncProblems(sync: OverviewData['sync']): SyncProblemList {
  if (sync.status !== 'error' || !sync.problems) return { items: [], more: 0 };
  const items = sync.problems.slice(0, MAX_SHOWN_PROBLEMS);
  const total = Math.max(sync.problems_total ?? 0, sync.problems.length);
  return { items, more: total - items.length };
}
