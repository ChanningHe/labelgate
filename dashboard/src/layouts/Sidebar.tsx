import { NavLink } from 'react-router-dom';
import type { CSSProperties, ReactNode } from 'react';
import { IconLayoutGrid, IconServer, IconTag, type Icon } from '@tabler/icons-react';
import { useOverview } from '../hooks/useAPI';
import type { OverviewData, ResourceCounts } from '../api/client';
import { KIND_IDS, KIND_META } from '../resources/meta';
import { formatVersion } from '../utils/format';
import { LiveIndicator } from './LiveIndicator';
import classes from './AppLayout.module.css';

interface NavItemProps {
  to: string;
  label: string;
  icon: Icon;
  color?: string;
  end?: boolean;
  badge?: ReactNode;
  onNavigate: () => void;
}

// NavLink sets aria-current="page" on the active item; the stylesheet keys off it.
function NavItem({ to, label, icon: ItemIcon, color, end, badge, onNavigate }: NavItemProps) {
  return (
    <NavLink
      to={to}
      end={end}
      onClick={onNavigate}
      className={classes.navItem}
      style={color ? ({ '--kind': color } as CSSProperties) : undefined}
    >
      <span className={classes.navIcon}>
        <ItemIcon size={16} stroke={1.75} />
      </span>
      {label}
      {badge}
    </NavLink>
  );
}

function CountBadge({ total, problems, problemLabel }: { total: number; problems: number; problemLabel: string }) {
  if (problems > 0) {
    return (
      <span className={classes.navCount} data-error="true" aria-label={`${problems} ${problemLabel}`}>
        {problems}
      </span>
    );
  }
  return <span className={classes.navCount}>{total}</span>;
}

const resourceBadge = (counts?: ResourceCounts) =>
  counts ? <CountBadge total={counts.total} problems={counts.error} problemLabel="with errors" /> : null;

const agentBadge = (agents?: OverviewData['agents']) =>
  agents ? <CountBadge total={agents.total} problems={agents.disconnected} problemLabel="offline" /> : null;

interface SidebarProps {
  isOpen: boolean;
  onNavigate: () => void;
}

export function Sidebar({ isOpen, onNavigate }: SidebarProps) {
  const { data } = useOverview();

  return (
    <aside className={classes.sidebar} data-open={isOpen} aria-label="Primary">
      <div className={classes.brand}>
        <span className={classes.logo}>
          <IconTag size={18} stroke={2.2} />
        </span>
        <div>
          <span className={classes.brandName}>Labelgate</span>
          <span className={classes.brandVersion}>{data ? formatVersion(data.version) : '—'}</span>
        </div>
      </div>

      <nav>
        <NavItem to="/" end label="Overview" icon={IconLayoutGrid} onNavigate={onNavigate} />

        <div className={classes.sectionTitle}>Resources</div>
        {KIND_IDS.map((id) => {
          const meta = KIND_META[id];
          return (
            <NavItem
              key={id}
              to={meta.path}
              label={meta.label}
              icon={meta.icon}
              color={meta.color}
              badge={resourceBadge(data?.resources[meta.overviewKey])}
              onNavigate={onNavigate}
            />
          );
        })}

        <div className={classes.sectionTitle}>Reference</div>
        <NavItem to="/labels" label="Labels" icon={IconTag} color="var(--lg-accent)" onNavigate={onNavigate} />

        <div className={classes.sectionTitle}>System</div>
        <NavItem to="/agents" label="Agents" icon={IconServer} badge={agentBadge(data?.agents)} onNavigate={onNavigate} />
      </nav>

      <div className={classes.sidebarFoot}>
        <LiveIndicator />
      </div>
    </aside>
  );
}
