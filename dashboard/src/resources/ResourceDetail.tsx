import { useState, type CSSProperties, type ReactNode } from 'react';
import { Tabs } from '@mantine/core';
import { IconAlertTriangle, IconInfoCircle, IconTag } from '@tabler/icons-react';
import type { ManagedResource } from '../api/client';
import { KindIcon, StatusPill } from '../components/Badges';
import { CodeBlock } from '../labels/CodeBlock';
import { describeProblem } from './attention';
import { CopyAction } from './cells';
import { equivalentLabels } from './equivalentLabels';
import type { DetailSection, ResourceKind } from './kinds';
import type { ResourceIndex } from './useResourceIndex';
import classes from './ResourcePage.module.css';

interface ResourceDetailProps {
  kind: ResourceKind;
  resource: ManagedResource;
  index: ResourceIndex;
  // Rendered at the end of the header row, e.g. a close button in the drawer.
  headerAction?: ReactNode;
}

function Problem({ resource, noun }: { resource: ManagedResource; noun: string }) {
  const text = describeProblem(resource, noun);
  if (!text) return null;
  const isError = resource.status === 'error';
  const ProblemIcon = isError ? IconAlertTriangle : IconInfoCircle;
  return (
    <div className={classes.callout} style={{ '--tone': isError ? 'var(--lg-err)' : 'var(--lg-warn)' } as CSSProperties}>
      <ProblemIcon size={16} stroke={1.75} />
      <span>{text}</span>
    </div>
  );
}

export function DetailSections({ sections }: { sections: DetailSection[] }) {
  return (
    <div className={classes.detailBody}>
      {sections.map((section) => (
        <section key={section.title}>
          <h4 className={classes.sectionTitle}>{section.title}</h4>
          <dl className={classes.kv}>
            {section.rows.map(([label, value]) => (
              <div key={label} style={{ display: 'contents' }}>
                <dt>{label}</dt>
                <dd>{value}</dd>
              </div>
            ))}
          </dl>
        </section>
      ))}
    </div>
  );
}

export function ResourceDetailHeader({ kind, resource, headerAction }: Omit<ResourceDetailProps, 'index'>) {
  return (
    <div className={classes.detailHead}>
      <div className={classes.detailRow}>
        <KindIcon icon={kind.icon} color={kind.color} />
        <StatusPill status={resource.status} />
        {headerAction && <span style={{ marginLeft: 'auto' }}>{headerAction}</span>}
      </div>
      <div className={classes.detailRow}>
        <span className={classes.detailHost}>{resource.hostname}</span>
        <CopyAction value={resource.hostname} label="Copy hostname" isVisible />
      </div>
      <Problem resource={resource} noun={kind.noun} />
    </div>
  );
}

type DetailTab = 'details' | 'labels';

function LabelsPanel({ kind, resource, index }: Omit<ResourceDetailProps, 'headerAction'>) {
  return (
    <div className={classes.detailBody}>
      <p className={classes.note}>
        Equivalent labels for this {kind.noun}, rebuilt from what labelgate stored. Paste them under the service in
        compose.yaml.
      </p>
      <CodeBlock lines={equivalentLabels(kind.id, resource, index)} />
    </div>
  );
}

export function ResourceDetail({ kind, resource, index, headerAction }: ResourceDetailProps) {
  // Lives across row selections so the chosen tab sticks while browsing.
  const [tab, setTab] = useState<DetailTab>('details');

  return (
    <>
      <ResourceDetailHeader kind={kind} resource={resource} headerAction={headerAction} />
      <Tabs value={tab} onChange={(v) => setTab((v as DetailTab) ?? 'details')} keepMounted={false}>
        <Tabs.List className={classes.tabs}>
          <Tabs.Tab value="details">Details</Tabs.Tab>
          <Tabs.Tab value="labels" leftSection={<IconTag size={14} stroke={1.75} />}>
            Labels
          </Tabs.Tab>
        </Tabs.List>
        <Tabs.Panel value="details">
          <DetailSections sections={kind.details(resource, index)} />
        </Tabs.Panel>
        <Tabs.Panel value="labels">
          <LabelsPanel kind={kind} resource={resource} index={index} />
        </Tabs.Panel>
      </Tabs>
    </>
  );
}

export function ResourceDetailEmpty({ kind }: { kind: ResourceKind }) {
  return (
    <div className={classes.detailEmpty}>
      <KindIcon icon={kind.icon} color={kind.color} size={44} />
      <b>No {kind.noun} selected</b>
      <span>Pick a row to see its details.</span>
    </div>
  );
}
