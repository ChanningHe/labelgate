import { useCallback, useEffect, useLayoutEffect, useRef, type KeyboardEvent, type ReactNode } from 'react';
import { Skeleton } from '@mantine/core';
import { IconChevronDown, IconChevronUp } from '@tabler/icons-react';
import type { ManagedResource } from '../api/client';
import type { ResourceKind } from './kinds';
import type { ResourceField, SortDirection } from './query';
import type { ResourceIndex } from './useResourceIndex';
import classes from './ResourcePage.module.css';

const SKELETON_ROWS = 4;

export interface SortState {
  key: ResourceField;
  direction: SortDirection;
}

interface ResourceTableProps {
  kind: ResourceKind;
  rows: readonly ManagedResource[];
  index: ResourceIndex;
  selectedId: string | null;
  onSelect: (id: string) => void;
  sort: SortState;
  onSort: (key: ResourceField) => void;
  isLoading: boolean;
  empty: ReactNode;
}

// Slides the accent bar to the selected row, measured from the DOM.
function useSelectionIndicator(selectedId: string | null, rowsKey: unknown) {
  const wrapRef = useRef<HTMLDivElement>(null);
  const barRef = useRef<HTMLDivElement>(null);

  const place = useCallback(() => {
    const wrap = wrapRef.current;
    const bar = barRef.current;
    if (!wrap || !bar) return;
    const row = selectedId ? wrap.querySelector<HTMLElement>(`tr[data-id="${CSS.escape(selectedId)}"]`) : null;
    if (!row) {
      bar.style.opacity = '0';
      return;
    }
    // Appear in place the first time; only slide between rows afterwards.
    const isAppearing = bar.style.opacity !== '1';
    if (isAppearing) bar.style.transition = 'none';
    const top = row.getBoundingClientRect().top - wrap.getBoundingClientRect().top;
    bar.style.transform = `translateY(${top}px)`;
    bar.style.height = `${row.offsetHeight}px`;
    bar.style.opacity = '1';
    if (isAppearing) {
      void bar.offsetHeight; // flush styles before restoring the transition
      bar.style.transition = '';
    }
  }, [selectedId]);

  useLayoutEffect(place, [place, rowsKey]);

  // Row heights change after web fonts load and when columns hide at narrow
  // widths, so re-measure whenever the table resizes.
  useEffect(() => {
    const wrap = wrapRef.current;
    if (!wrap) return;
    const observer = new ResizeObserver(place);
    observer.observe(wrap);
    return () => observer.disconnect();
  }, [place]);

  return { wrapRef, barRef };
}

const cellClass = (base: string, isSecondary?: boolean) =>
  isSecondary ? `${base} ${classes.secondary}` : base;

export function ResourceTable({
  kind,
  rows,
  index,
  selectedId,
  onSelect,
  sort,
  onSort,
  isLoading,
  empty,
}: ResourceTableProps) {
  const { wrapRef, barRef } = useSelectionIndicator(selectedId, rows);

  if (isLoading) {
    return (
      <div className={classes.skeletons}>
        {Array.from({ length: SKELETON_ROWS }, (_, i) => (
          <Skeleton key={i} height={36} radius="sm" />
        ))}
      </div>
    );
  }

  const handleKey = (e: KeyboardEvent, id: string) => {
    if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      onSelect(id);
    }
  };

  return (
    <div className={classes.tableWrap} ref={wrapRef}>
      <div className={classes.indicator} ref={barRef} aria-hidden="true" />
      <table className={classes.table}>
        <thead>
          <tr>
            {kind.columns.map((col) => {
              const { sortKey } = col;
              const isSorted = sortKey !== undefined && sort.key === sortKey;
              const SortIcon = isSorted && sort.direction === 'desc' ? IconChevronDown : IconChevronUp;
              return (
                <th
                  key={col.id}
                  scope="col"
                  className={cellClass(classes.th, col.isSecondary)}
                  aria-sort={isSorted ? (sort.direction === 'asc' ? 'ascending' : 'descending') : undefined}
                >
                  {sortKey ? (
                    <button type="button" className={classes.sortButton} onClick={() => onSort(sortKey)}>
                      {col.label}
                      <SortIcon size={12} className={classes.sortIcon} />
                    </button>
                  ) : (
                    col.label
                  )}
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.length === 0 ? (
            <tr>
              <td colSpan={kind.columns.length}>{empty}</td>
            </tr>
          ) : (
            rows.map((r) => (
              <tr
                key={r.id}
                data-id={r.id}
                className={classes.row}
                aria-selected={r.id === selectedId}
                tabIndex={0}
                onClick={() => onSelect(r.id)}
                onKeyDown={(e) => handleKey(e, r.id)}
              >
                {kind.columns.map((col) => (
                  <td key={col.id} className={cellClass(classes.td, col.isSecondary)}>
                    {col.render(r, index)}
                  </td>
                ))}
              </tr>
            ))
          )}
        </tbody>
      </table>
    </div>
  );
}
