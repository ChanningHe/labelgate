import { describe, expect, test, vi } from 'vitest';
import { createLiveStatus } from './liveStatus';

describe('live status', () => {
  test('starts with no successful sync and not failing', () => {
    const live = createLiveStatus(() => 1000);

    expect(live.getSnapshot()).toEqual({ lastSuccessAt: null, isFailing: false });
  });

  test('records the time of the latest success and clears failure', () => {
    let now = 1000;
    const live = createLiveStatus(() => now);
    live.reportFailure();

    now = 5000;
    live.reportSuccess();

    expect(live.getSnapshot()).toEqual({ lastSuccessAt: 5000, isFailing: false });
  });

  test('keeps the last success time when a request fails', () => {
    const live = createLiveStatus(() => 2000);
    live.reportSuccess();

    live.reportFailure();

    expect(live.getSnapshot()).toEqual({ lastSuccessAt: 2000, isFailing: true });
  });

  test('notifies subscribers on change and stops after unsubscribe', () => {
    const live = createLiveStatus(() => 1);
    const listener = vi.fn();
    const unsubscribe = live.subscribe(listener);

    live.reportSuccess();
    unsubscribe();
    live.reportFailure();

    expect(listener).toHaveBeenCalledTimes(1);
  });

  test('returns a stable snapshot between changes', () => {
    const live = createLiveStatus(() => 1);
    live.reportSuccess();

    expect(live.getSnapshot()).toBe(live.getSnapshot());
  });
});
