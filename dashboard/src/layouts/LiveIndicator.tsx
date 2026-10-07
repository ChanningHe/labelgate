import { useSyncExternalStore, type CSSProperties } from 'react';
import { liveStatus } from '../api/liveStatus';
import { useNow } from '../hooks/useNow';
import { formatRelative } from '../utils/format';
import classes from './AppLayout.module.css';

// Shows whether the dashboard is receiving data. The ring pings on every
// successful response, which happens on each SWR poll.
export function LiveIndicator() {
  const { lastSuccessAt, isFailing } = useSyncExternalStore(liveStatus.subscribe, liveStatus.getSnapshot);
  const now = useNow();

  const label = isFailing ? 'Reconnecting' : lastSuccessAt ? 'Live' : 'Connecting';
  const state = isFailing ? 'var(--lg-err)' : lastSuccessAt ? 'var(--lg-ok)' : 'var(--lg-faint)';
  const detail = lastSuccessAt
    ? `synced ${formatRelative(new Date(lastSuccessAt).toISOString(), now)}`
    : 'waiting for server';

  return (
    <div className={classes.live} role="status" aria-live="polite" style={{ '--state': state } as CSSProperties}>
      <span className={classes.liveDot}>
        <span key={lastSuccessAt ?? 0} className={classes.liveRing} data-ping={lastSuccessAt ? 'true' : undefined} />
      </span>
      <div>
        <span className={classes.liveLabel}>{label}</span>
        <span className={classes.liveDetail}>{detail}</span>
      </div>
    </div>
  );
}
