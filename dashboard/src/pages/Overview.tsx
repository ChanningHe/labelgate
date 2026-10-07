import type { CSSProperties } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { Skeleton } from '@mantine/core';
import { IconArrowRight, IconServer, type Icon } from '@tabler/icons-react';
import type { AgentInfo, OverviewData } from '../api/client';
import { KindIcon, StatusPill } from '../components/Badges';
import { PageHeader } from '../components/PageHeader';
import { useAgents, useOverview } from '../hooks/useAPI';
import { useCountUp } from '../hooks/useCountUp';
import { useNow } from '../hooks/useNow';
import { recipesFor } from '../labels/catalog';
import { CodeBlock } from '../labels/CodeBlock';
import { collectAttention, describeProblem, type AttentionItem } from '../resources/attention';
import { KIND_IDS, KIND_META } from '../resources/meta';
import { useResourceIndex } from '../resources/useResourceIndex';
import { formatRelative, formatVersion } from '../utils/format';
import classes from './Overview.module.css';

const OK = 'var(--lg-ok)';
const WARN = 'var(--lg-warn)';
const ERR = 'var(--lg-err)';
const QUICK_START = recipesFor('tunnel')[0];

const tone = (color: string, extra?: CSSProperties) => ({ '--tone': color, ...extra }) as CSSProperties;

/* ---------- status strip ---------- */

interface StripItemProps {
  label: string;
  value: string;
  detail: string;
  color?: string;
  isMono?: boolean;
}

function StripItem({ label, value, detail, color, isMono }: StripItemProps) {
  return (
    <div className={classes.stripItem}>
      <span className={classes.stripLabel}>{label}</span>
      <span className={`${classes.stripValue} ${isMono ? 'lg-mono' : ''}`} style={color ? tone(color) : undefined}>
        {color && <span className={classes.stripDot} />}
        {value}
      </span>
      <span className={classes.stripDetail} title={detail}>
        {detail}
      </span>
    </div>
  );
}

function agentDetail(data: OverviewData, agents: readonly AgentInfo[], now: number): string {
  if (data.agents.total === 0) return 'Only the local Docker host';
  const offline = agents.find((a) => !a.connected);
  if (!offline) return 'All reporting';
  return `${offline.name || offline.id} last seen ${formatRelative(offline.last_seen, now)}`;
}

function StatusStrip({ data, agents }: { data: OverviewData; agents: readonly AgentInfo[] }) {
  const now = useNow();
  const isSyncOk = data.sync.status === 'success';
  const { connected, total } = data.agents;

  return (
    <div className={`lg-panel ${classes.strip}`}>
      <StripItem
        label="Last sync"
        value={isSyncOk ? 'Succeeded' : 'Failed'}
        color={isSyncOk ? OK : ERR}
        detail={isSyncOk ? formatRelative(data.sync.last_sync, now) : data.sync.error}
      />
      <StripItem
        label="Cloudflare API"
        value={data.cloudflare.reachable ? 'Reachable' : 'Unreachable'}
        color={data.cloudflare.reachable ? OK : ERR}
        detail={`checked ${formatRelative(data.cloudflare.last_check, now)}`}
      />
      <StripItem
        label="Agents"
        value={total === 0 ? 'None' : `${connected} of ${total} connected`}
        color={connected === total ? OK : WARN}
        detail={agentDetail(data, agents, now)}
      />
      <StripItem
        label="Labelgate"
        value={formatVersion(data.version)}
        isMono
        detail={`up ${data.uptime || '—'} · prefix ${data.label_prefix || 'labelgate'}`}
      />
    </div>
  );
}

/* ---------- tiles ---------- */

interface Segment {
  label: string;
  value: number;
  color: string;
}

interface StatTileProps {
  title: string;
  to: string;
  icon: Icon;
  color: string;
  total: number;
  segments: Segment[];
  order: number;
}

function StatTile({ title, to, icon, color, total, segments, order }: StatTileProps) {
  const shown = useCountUp(total);
  return (
    <Link to={to} className={`lg-panel ${classes.tile}`} style={{ '--kind': color, '--i': order } as CSSProperties}>
      <div className={classes.tileTop}>
        <KindIcon icon={icon} color={color} />
        {title}
        <IconArrowRight size={16} className={classes.tileGo} />
      </div>
      <div className={classes.tileNumber} aria-label={`${total} ${title}`}>
        {shown}
      </div>
      <div className={classes.bar} aria-hidden="true">
        {segments
          .filter((s) => s.value > 0)
          .map((s) => (
            <span key={s.label} style={tone(s.color, { flex: s.value })} />
          ))}
      </div>
      <div className={classes.legend}>
        {segments.map((s) => (
          <span key={s.label} style={tone(s.color)}>
            <i />
            <b>{s.value}</b> {s.label}
          </span>
        ))}
      </div>
    </Link>
  );
}

const TILE_TITLES = { dns: 'DNS records', tunnel: 'Tunnel ingress', access: 'Access apps' } as const;

function Tiles({ data }: { data: OverviewData }) {
  return (
    <div className={classes.tiles}>
      {KIND_IDS.map((id, i) => {
        const meta = KIND_META[id];
        const counts = data.resources[meta.overviewKey];
        return (
          <StatTile
            key={id}
            title={TILE_TITLES[id]}
            to={meta.path}
            icon={meta.icon}
            color={meta.color}
            total={counts.total}
            order={i}
            segments={[
              { label: 'active', value: counts.active, color: OK },
              { label: 'orphaned', value: counts.orphaned, color: WARN },
              { label: 'error', value: counts.error, color: ERR },
            ]}
          />
        );
      })}
      <StatTile
        title="Agents"
        to="/agents"
        icon={IconServer}
        color="var(--lg-accent)"
        total={data.agents.total}
        order={KIND_IDS.length}
        segments={[
          { label: 'connected', value: data.agents.connected, color: OK },
          { label: 'offline', value: data.agents.disconnected, color: ERR },
        ]}
      />
    </div>
  );
}

/* ---------- attention ---------- */

function AttentionList({ items }: { items: AttentionItem[] }) {
  const navigate = useNavigate();
  const now = useNow();
  const hasErrors = items.some((x) => x.resource.status === 'error');

  return (
    <section className="lg-panel" aria-label="Needs attention">
      <div className="lg-panel-head">
        <h2>Needs attention</h2>
        <span className={classes.badge} data-error={hasErrors}>
          {items.length}
        </span>
      </div>
      {items.length === 0 ? (
        <div className={classes.calm}>Everything is in sync.</div>
      ) : (
        <ul className={classes.attention}>
          {items.map(({ kind, resource }) => {
            const meta = KIND_META[kind];
            return (
              <li key={`${kind}-${resource.id}`}>
                <button
                  type="button"
                  className={classes.attentionItem}
                  style={tone(resource.status === 'error' ? ERR : WARN)}
                  onClick={() => navigate(`${meta.path}?id=${encodeURIComponent(resource.id)}`)}
                >
                  <span className={classes.stripe} />
                  <KindIcon icon={meta.icon} color={meta.color} />
                  <span style={{ minWidth: 0 }}>
                    <span className={classes.attentionHost}>
                      {resource.hostname}
                      <StatusPill status={resource.status} />
                    </span>
                    <p className={classes.attentionText}>{describeProblem(resource, meta.noun)}</p>
                  </span>
                  <span className={classes.when}>{formatRelative(resource.updated_at, now)}</span>
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </section>
  );
}

function QuickStart() {
  return (
    <section className="lg-panel" aria-label="Expose a container">
      <div className="lg-panel-head">
        <h2>Expose a container</h2>
        <KindIcon icon={KIND_META.tunnel.icon} color={KIND_META.tunnel.color} />
      </div>
      <div className={classes.quick}>
        <p>
          Add two labels to any service on the cloudflared network. Labelgate creates the ingress rule and Cloudflare adds
          the DNS record.
        </p>
        <CodeBlock lines={QUICK_START.lines} image={QUICK_START.image} />
        <Link className={classes.link} to="/labels?tab=tunnel">
          Browse all label recipes <IconArrowRight size={14} />
        </Link>
      </div>
    </section>
  );
}

/* ---------- page ---------- */

export function Overview() {
  const { data } = useOverview();
  const { data: agentList } = useAgents();
  const { lists } = useResourceIndex();
  const attention = collectAttention({
    dns: lists.dns.data?.resources,
    tunnel: lists.tunnel.data?.resources,
    access: lists.access.data?.resources,
  });

  return (
    <>
      <PageHeader
        title="Overview"
        description="Container labels in, Cloudflare resources out. Data refreshes every 10 seconds."
      />
      {data ? (
        <div className={classes.stack}>
          <StatusStrip data={data} agents={agentList?.agents ?? []} />
          <Tiles data={data} />
          <div className={classes.columns}>
            <AttentionList items={attention} />
            <QuickStart />
          </div>
        </div>
      ) : (
        <div className={classes.stack}>
          <Skeleton height={68} radius="md" />
          <div className={classes.tiles}>
            {[0, 1, 2, 3].map((i) => (
              <Skeleton key={i} height={150} radius="md" />
            ))}
          </div>
        </div>
      )}
    </>
  );
}
