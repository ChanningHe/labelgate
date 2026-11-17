import { describe, expect, test } from 'vitest';
import { syncDisplay } from './syncStatus';

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
