import { describe, expect, test } from 'vitest';
import type { OverviewData } from '../api/client';
import { MAX_SHOWN_PROBLEMS, syncDisplay, syncProblems } from './syncStatus';

type Sync = OverviewData['sync'];

const sync = (patch: Partial<Sync>): Sync => ({ last_sync: '', status: 'error', error: 'boom', ...patch });
const problem = (i: number) => ({ subject: `local container c${i}`, text: `local container c${i}: bad label` });

describe('syncDisplay', () => {
  test('success is ok', () => {
    expect(syncDisplay('success')).toEqual({ label: 'Succeeded', tone: 'ok' });
  });

  test('error is failed', () => {
    expect(syncDisplay('error')).toEqual({ label: 'Failed', tone: 'err' });
  });

  test('pending is neutral, never failed', () => {
    expect(syncDisplay('pending')).toEqual({ label: 'Pending', tone: 'off' });
  });
});

describe('syncProblems', () => {
  test('returns every problem the server sent', () => {
    const problems = [problem(1), problem(2)];
    expect(syncProblems(sync({ problems, problems_total: 2 }))).toEqual({ items: problems, more: 0 });
  });

  test('counts problems the server left out', () => {
    expect(syncProblems(sync({ problems: [problem(1)], problems_total: 7 }))).toEqual({
      items: [problem(1)],
      more: 6,
    });
  });

  test('bounds the list on the client too', () => {
    const problems = Array.from({ length: MAX_SHOWN_PROBLEMS + 3 }, (_, i) => problem(i));
    const got = syncProblems(sync({ problems, problems_total: problems.length }));
    expect(got.items).toHaveLength(MAX_SHOWN_PROBLEMS);
    expect(got.more).toBe(3);
  });

  test('is empty for an older server without the list', () => {
    expect(syncProblems(sync({}))).toEqual({ items: [], more: 0 });
  });

  test('is empty unless the sync failed', () => {
    expect(syncProblems(sync({ status: 'success', problems: [problem(1)], problems_total: 1 }))).toEqual({
      items: [],
      more: 0,
    });
  });
});
