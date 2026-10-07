import type { CSSProperties, ReactNode } from 'react';
import type { Icon } from '@tabler/icons-react';
import classes from './Badges.module.css';

type Tone = 'ok' | 'warn' | 'err' | 'off' | 'tunnel';

const TONE_VAR: Record<Tone, string> = {
  ok: 'var(--lg-ok)',
  warn: 'var(--lg-warn)',
  err: 'var(--lg-err)',
  off: 'var(--lg-off)',
  tunnel: 'var(--lg-tunnel)',
};

const STATUS: Record<string, { label: string; tone: Tone; isLive?: boolean }> = {
  active: { label: 'Active', tone: 'ok' },
  orphaned: { label: 'Orphaned', tone: 'warn' },
  pending_cleanup: { label: 'Cleanup pending', tone: 'warn' },
  deleted: { label: 'Deleted', tone: 'off' },
  error: { label: 'Error', tone: 'err' },
  connected: { label: 'Connected', tone: 'ok', isLive: true },
  disconnected: { label: 'Disconnected', tone: 'err' },
  removed: { label: 'Removed', tone: 'off' },
};

const DECISION_TONE: Record<string, Tone> = {
  allow: 'ok',
  block: 'err',
  bypass: 'warn',
  service_auth: 'tunnel',
};

const toneStyle = (tone: Tone) => ({ '--tone': TONE_VAR[tone] }) as CSSProperties;

export function StatusPill({ status }: { status: string }) {
  const config = STATUS[status] ?? { label: status, tone: 'off' as Tone };
  return (
    <span className={classes.pill} style={toneStyle(config.tone)}>
      <span className={classes.dot} data-live={config.isLive} />
      {config.label}
    </span>
  );
}

export function DecisionPill({ decision }: { decision?: string }) {
  if (!decision) return <span>—</span>;
  return (
    <span className={classes.pill} style={toneStyle(DECISION_TONE[decision] ?? 'off')}>
      {decision}
    </span>
  );
}

export function Chip({ children, color }: { children: ReactNode; color?: string }) {
  return (
    <span className={classes.chip} style={color ? ({ '--tone': color } as CSSProperties) : undefined}>
      {children}
    </span>
  );
}

interface KindIconProps {
  icon: Icon;
  color: string;
  size?: number;
}

const ICON_RATIO = 0.58;

export function KindIcon({ icon: KIcon, color, size = 26 }: KindIconProps) {
  return (
    <span className={classes.kindIcon} style={{ '--kind': color, '--size': `${size}px` } as CSSProperties}>
      <KIcon size={Math.round(size * ICON_RATIO)} stroke={1.75} />
    </span>
  );
}
