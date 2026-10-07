// Tracks whether the dashboard is receiving data from the server.
// fetchAPI reports every outcome here; the sidebar's live indicator
// subscribes through useSyncExternalStore.

export interface LiveSnapshot {
  lastSuccessAt: number | null;
  isFailing: boolean;
}

export interface LiveStatus {
  getSnapshot(): LiveSnapshot;
  subscribe(listener: () => void): () => void;
  reportSuccess(): void;
  reportFailure(): void;
}

export function createLiveStatus(now: () => number = Date.now): LiveStatus {
  let snapshot: LiveSnapshot = { lastSuccessAt: null, isFailing: false };
  const listeners = new Set<() => void>();

  const publish = (next: LiveSnapshot) => {
    snapshot = next;
    listeners.forEach((l) => l());
  };

  return {
    getSnapshot: () => snapshot,
    subscribe(listener) {
      listeners.add(listener);
      return () => listeners.delete(listener);
    },
    reportSuccess: () => publish({ lastSuccessAt: now(), isFailing: false }),
    reportFailure: () => publish({ ...snapshot, isFailing: true }),
  };
}

export const liveStatus = createLiveStatus();
