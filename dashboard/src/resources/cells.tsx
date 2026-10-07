import type { ReactNode } from 'react';
import { CopyButton, Tooltip } from '@mantine/core';
import { IconCheck, IconCloud, IconCloudFilled, IconCopy, IconExternalLink, IconShieldLock } from '@tabler/icons-react';
import { Chip } from '../components/Badges';
import { agentLabel } from './query';
import type { ManagedResource } from '../api/client';
import classes from './cells.module.css';

const COPIED_RESET_MS = 1200;

const stop = (e: { stopPropagation: () => void }) => e.stopPropagation();

export function CopyAction({ value, label, isVisible }: { value: string; label: string; isVisible?: boolean }) {
  return (
    <CopyButton value={value} timeout={COPIED_RESET_MS}>
      {({ copied, copy }) => (
        <Tooltip label={copied ? 'Copied' : label} withArrow openDelay={300}>
          <button
            type="button"
            className={classes.rowAction}
            data-copied={copied}
            data-visible={isVisible}
            aria-label={label}
            onClick={(e) => {
              stop(e);
              copy();
            }}
          >
            {copied ? <IconCheck size={14} /> : <IconCopy size={14} />}
          </button>
        </Tooltip>
      )}
    </CopyButton>
  );
}

export function OpenAction({ hostname }: { hostname: string }) {
  return (
    <Tooltip label="Open in a new tab" withArrow openDelay={300}>
      <a
        className={classes.rowAction}
        href={`https://${hostname}`}
        target="_blank"
        rel="noopener noreferrer"
        aria-label={`Open ${hostname}`}
        onClick={stop}
      >
        <IconExternalLink size={14} />
      </a>
    </Tooltip>
  );
}

export function HostnameCell({ hostname, extra }: { hostname: string; extra?: ReactNode }) {
  return (
    <span className={classes.host}>
      <span className={classes.hostName}>{hostname}</span>
      {extra}
      <CopyAction value={hostname} label="Copy hostname" />
    </span>
  );
}

export function Mono({ children, isDim, isTruncated }: { children: ReactNode; isDim?: boolean; isTruncated?: boolean }) {
  const cls = [classes.mono, isDim && classes.dim, isTruncated && classes.truncate].filter(Boolean).join(' ');
  return <span className={cls}>{children}</span>;
}

// Tunnel service URLs render with the scheme highlighted: http://webapp:80
export function ServiceURL({ service }: { service?: string }) {
  if (!service) return <span className={classes.dim}>—</span>;
  const match = /^([a-z_]+:\/\/|unix:)(.*)$/.exec(service);
  return (
    <span className={classes.mono}>
      {match ? (
        <>
          <span className={classes.protocol}>{match[1]}</span>
          {match[2]}
        </>
      ) : (
        service
      )}
    </span>
  );
}

export function ProxyStatus({ isProxied }: { isProxied?: boolean }) {
  return (
    <span className={classes.cloud} data-proxied={Boolean(isProxied)}>
      {isProxied ? <IconCloudFilled size={18} /> : <IconCloud size={18} stroke={1.75} />}
      {isProxied ? 'Proxied' : 'DNS only'}
    </span>
  );
}

export function AccessChip({ policy }: { policy?: string }) {
  if (!policy) return <span className={classes.dim}>Public</span>;
  return (
    <Chip color="var(--lg-access)">
      <IconShieldLock size={12} stroke={1.75} />
      {policy}
    </Chip>
  );
}

export function SourceCell({ resource }: { resource: ManagedResource }) {
  return (
    <span className={classes.source}>
      <b>{resource.container_name || '—'}</b> · {agentLabel(resource)}
    </span>
  );
}
