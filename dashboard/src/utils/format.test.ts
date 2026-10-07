import { describe, expect, test } from 'vitest';
import { formatRelative, formatVersion, maskIP } from './format';

describe('maskIP', () => {
  test('keeps the first two IPv4 octets', () => {
    expect(maskIP('198.51.100.42')).toBe('198.51.•••.••');
  });

  test('keeps the first two IPv6 groups', () => {
    expect(maskIP('2001:db8:85a3::8a2e:370:7334')).toBe('2001:db8:••••');
  });

  test('returns a dash when there is no address', () => {
    expect(maskIP(undefined)).toBe('—');
  });

  test('fully masks anything that is not an IP', () => {
    expect(maskIP('unknown')).toBe('•••');
  });
});

describe('formatVersion', () => {
  test('keeps a tag that already starts with v', () => {
    expect(formatVersion('v0.1.8')).toBe('v0.1.8');
  });

  test('adds v to a bare semantic version', () => {
    expect(formatVersion('0.1.8')).toBe('v0.1.8');
  });

  test('leaves non-numeric builds such as dev untouched', () => {
    expect(formatVersion('dev')).toBe('dev');
  });

  test('returns a dash for an empty version', () => {
    expect(formatVersion('')).toBe('—');
  });
});

describe('formatRelative', () => {
  const now = Date.parse('2026-10-07T12:00:00Z');

  test.each([
    ['2026-10-07T11:59:58Z', 'just now'],
    ['2026-10-07T11:59:30Z', '30s ago'],
    ['2026-10-07T11:48:00Z', '12m ago'],
    ['2026-10-07T07:00:00Z', '5h ago'],
    ['2026-10-04T12:00:00Z', '3d ago'],
  ])('formats %s as %s', (iso, expected) => {
    expect(formatRelative(iso, now)).toBe(expected);
  });

  test('formats dates older than a week as a calendar date', () => {
    expect(formatRelative('2026-09-01T12:00:00Z', now)).toBe('Sep 1');
  });

  test('includes the year for dates in another year', () => {
    expect(formatRelative('2025-12-24T12:00:00Z', now)).toBe('Dec 24, 2025');
  });

  test.each([[''], [null], [undefined], ['0001-01-01T00:00:00Z'], ['not a date']])(
    'returns a dash for missing or zero time %s',
    (value) => {
      expect(formatRelative(value, now)).toBe('—');
    },
  );
});
