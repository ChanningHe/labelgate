import { useMemo, useState, type CSSProperties } from 'react';
import { useSearchParams } from 'react-router-dom';
import { ActionIcon, Button, Drawer, SegmentedControl, TextInput } from '@mantine/core';
import { useMediaQuery } from '@mantine/hooks';
import { IconAlertTriangle, IconSearch, IconX } from '@tabler/icons-react';
import type { ManagedResource } from '../api/client';
import { PageHeader } from '../components/PageHeader';
import { RESOURCE_KINDS } from './kinds';
import type { KindId } from './meta';
import {
  STATUS_FILTERS,
  countByStatus,
  filterResources,
  sortResources,
  type ResourceField,
  type StatusFilter,
} from './query';
import { ResourceDetail, ResourceDetailEmpty } from './ResourceDetail';
import { ResourceTable, type SortState } from './ResourceTable';
import { useResourceIndex } from './useResourceIndex';
import classes from './ResourcePage.module.css';

// Keep in sync with the single-column breakpoint in ResourcePage.module.css.
const SIDE_PANEL_QUERY = '(min-width: 1181px)';
const SELECTED_PARAM = 'id';
const NO_ROWS: readonly ManagedResource[] = [];

function EmptyState({ noun, isFiltered, onClear }: { noun: string; isFiltered: boolean; onClear: () => void }) {
  if (isFiltered) {
    return (
      <div className={classes.empty}>
        <b>No {noun}s match</b>
        <span>Try another search or status.</span>
        <Button variant="subtle" size="xs" onClick={onClear}>
          Clear filters
        </Button>
      </div>
    );
  }
  return (
    <div className={classes.empty}>
      <b>No {noun}s yet</b>
      <span>Add labels to a container and labelgate creates them on the next sync.</span>
    </div>
  );
}

function LoadError({ message }: { message: string }) {
  return (
    <div className={classes.empty}>
      <IconAlertTriangle size={20} color="var(--lg-err)" />
      <b>Could not load this list</b>
      <span>{message}</span>
    </div>
  );
}

export function ResourcePage({ kind: kindId }: { kind: KindId }) {
  const kind = RESOURCE_KINDS[kindId];
  const { index, lists } = useResourceIndex();
  const { data, error, isLoading } = lists[kindId];
  const rows = data?.resources ?? NO_ROWS;

  const [status, setStatus] = useState<StatusFilter>('all');
  const [query, setQuery] = useState('');
  const [sort, setSort] = useState<SortState>({ key: 'hostname', direction: 'asc' });
  const [params, setParams] = useSearchParams();
  const hasSidePanel = useMediaQuery(SIDE_PANEL_QUERY, true);

  const selectedId = params.get(SELECTED_PARAM);
  const selected = rows.find((r) => r.id === selectedId) ?? null;
  const counts = useMemo(() => countByStatus(rows), [rows]);
  const visible = useMemo(
    () => sortResources(filterResources(rows, { status, query, fields: kind.searchFields }), sort.key, sort.direction),
    [rows, status, query, kind.searchFields, sort],
  );

  const select = (id: string | null) => setParams(id ? { [SELECTED_PARAM]: id } : {}, { replace: true });
  const toggleSort = (key: ResourceField) =>
    setSort((prev) => ({ key, direction: prev.key === key && prev.direction === 'asc' ? 'desc' : 'asc' }));
  const clearFilters = () => {
    setStatus('all');
    setQuery('');
  };

  const closeButton = (
    <ActionIcon variant="subtle" color="gray" onClick={() => select(null)} aria-label="Close details">
      <IconX size={16} />
    </ActionIcon>
  );

  return (
    <>
      <PageHeader
        title={kind.title}
        description={kind.description}
        eyebrow={{ label: 'Resources', icon: kind.icon, color: kind.color }}
      />

      <div className={classes.layout} style={{ '--kind': kind.color } as CSSProperties}>
        <section className={`lg-panel ${classes.tablePanel}`} aria-label={kind.title}>
          <div className={classes.toolbar}>
            <SegmentedControl
              size="xs"
              aria-label="Filter by status"
              value={status}
              onChange={(v) => setStatus(v as StatusFilter)}
              data={STATUS_FILTERS.map((f) => ({
                value: f.value,
                label: (
                  <span>
                    {f.label}
                    <span className={classes.count} data-error={f.value === 'error' && counts.error > 0}>
                      {counts[f.value]}
                    </span>
                  </span>
                ),
              }))}
            />
            <TextInput
              className={classes.search}
              size="xs"
              aria-label="Search"
              placeholder={kind.searchPlaceholder}
              leftSection={<IconSearch size={14} />}
              value={query}
              onChange={(e) => setQuery(e.currentTarget.value)}
            />
          </div>

          {error && !data ? (
            <LoadError message={error instanceof Error ? error.message : String(error)} />
          ) : (
            <ResourceTable
              kind={kind}
              rows={visible}
              index={index}
              selectedId={selectedId}
              onSelect={select}
              sort={sort}
              onSort={toggleSort}
              isLoading={isLoading && !data}
              empty={<EmptyState noun={kind.noun} isFiltered={rows.length > 0} onClear={clearFilters} />}
            />
          )}

          <div className={classes.foot}>
            <span>
              Showing {visible.length} of {rows.length} {kind.noun}s
            </span>
            <span>Select a row for details</span>
          </div>
        </section>

        {hasSidePanel && (
          <aside className={`lg-panel ${classes.detail}`} aria-label="Details">
            {selected ? (
              <ResourceDetail kind={kind} resource={selected} index={index} />
            ) : (
              <ResourceDetailEmpty kind={kind} />
            )}
          </aside>
        )}
      </div>

      {!hasSidePanel && (
        <Drawer
          opened={selected !== null}
          onClose={() => select(null)}
          position="right"
          size="md"
          padding={0}
          withCloseButton={false}
          aria-label={`${kind.noun} details`}
        >
          {selected && <ResourceDetail kind={kind} resource={selected} index={index} headerAction={closeButton} />}
        </Drawer>
      )}
    </>
  );
}
