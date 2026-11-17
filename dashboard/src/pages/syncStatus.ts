import type { OverviewData } from '../api/client';

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
