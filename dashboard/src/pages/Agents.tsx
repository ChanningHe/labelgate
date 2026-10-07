import { useState, type CSSProperties } from 'react';
import { ActionIcon, Skeleton, Tooltip } from '@mantine/core';
import { IconEye, IconEyeOff, IconServer } from '@tabler/icons-react';
import type { AgentInfo } from '../api/client';
import { KindIcon, StatusPill } from '../components/Badges';
import { PageHeader } from '../components/PageHeader';
import { useAgents } from '../hooks/useAPI';
import { useNow } from '../hooks/useNow';
import { formatRelative, maskIP } from '../utils/format';
import classes from './Agents.module.css';

const MULTI_HOST_DOCS = 'https://labelgate-docs.pages.dev/docs/examples/multi-host';
const SKELETON_CARDS = 3;

interface AgentCardProps {
  agent: AgentInfo;
  order: number;
  isRevealed: boolean;
  onToggleIP: () => void;
  now: number;
}

function AgentCard({ agent, order, isRevealed, onToggleIP, now }: AgentCardProps) {
  const name = agent.name || agent.id;
  const revealLabel = isRevealed ? 'Hide IP' : 'Show IP';

  return (
    <article className={`lg-panel ${classes.card}`} style={{ '--i': order } as CSSProperties}>
      <header className={classes.head}>
        <KindIcon icon={IconServer} color={agent.connected ? 'var(--lg-accent)' : 'var(--lg-off)'} />
        <div>
          <h3>{name}</h3>
          {name !== agent.id && <small>{agent.id}</small>}
        </div>
        <StatusPill status={agent.connected ? 'connected' : 'disconnected'} />
      </header>
      <dl className={classes.kv}>
        <dt>Public IP</dt>
        <dd>
          <span className={classes.ip}>
            {isRevealed ? agent.public_ip || '—' : maskIP(agent.public_ip)}
            {agent.public_ip && (
              <Tooltip label={revealLabel} withArrow>
                <ActionIcon variant="subtle" color="gray" size="sm" onClick={onToggleIP} aria-label={revealLabel}>
                  {isRevealed ? <IconEyeOff size={14} /> : <IconEye size={14} />}
                </ActionIcon>
              </Tooltip>
            )}
          </span>
        </dd>
        <dt>Default tunnel</dt>
        <dd className="lg-mono">{agent.default_tunnel || 'default'}</dd>
        <dt>Resources</dt>
        <dd className="lg-num">{agent.resource_count}</dd>
        <dt>Last seen</dt>
        <dd>{agent.connected ? 'Connected now' : formatRelative(agent.last_seen, now)}</dd>
        <dt>Registered</dt>
        <dd>{formatRelative(agent.created_at, now)}</dd>
      </dl>
    </article>
  );
}

function NoAgents() {
  return (
    <div className={`lg-panel ${classes.empty}`}>
      <KindIcon icon={IconServer} color="var(--lg-accent)" size={44} />
      <b>No agents yet</b>
      <span>Agents report containers from other Docker hosts to this server.</span>
      <a href={MULTI_HOST_DOCS} target="_blank" rel="noopener noreferrer">
        Set up a multi-host agent
      </a>
    </div>
  );
}

export function Agents() {
  const { data, isLoading } = useAgents();
  const [revealed, setRevealed] = useState<ReadonlySet<string>>(new Set());
  const now = useNow();
  const agents = data?.agents ?? [];

  const toggleIP = (id: string) =>
    setRevealed((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });

  return (
    <>
      <PageHeader
        title="Agents"
        description="Docker hosts that report containers to this labelgate server."
        eyebrow={{ label: 'System', icon: IconServer }}
      />
      {isLoading && !data ? (
        <div className={classes.grid}>
          {Array.from({ length: SKELETON_CARDS }, (_, i) => (
            <Skeleton key={i} height={220} radius="md" />
          ))}
        </div>
      ) : agents.length === 0 ? (
        <NoAgents />
      ) : (
        <div className={classes.grid}>
          {agents.map((agent, i) => (
            <AgentCard
              key={agent.id}
              agent={agent}
              order={i}
              isRevealed={revealed.has(agent.id)}
              onToggleIP={() => toggleIP(agent.id)}
              now={now}
            />
          ))}
        </div>
      )}
    </>
  );
}
