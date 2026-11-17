import { describe, expect, test } from 'vitest';
import { openUrl } from './openUrl';

describe('openUrl', () => {
  test('links to the hostname root without a path', () => {
    expect(openUrl('app.example.com')).toBe('https://app.example.com');
  });

  test('has no link for wildcard hostnames', () => {
    expect(openUrl('*.example.com')).toBeNull();
    expect(openUrl('*.example.com', '/api')).toBeNull();
  });

  test.each([['/api'], ['/api/v1'], ['/static/'], ['/a_b-c~d']])('appends plain prefix path %s', (path) => {
    expect(openUrl('app.example.com', path)).toBe(`https://app.example.com${path}`);
  });

  test.each([['^/api'], ['/api$'], ['/v1.0'], ['/api/.*'], ['/(a|b)'], ['/a+'], ['/a?'], ['/[ab]'], ['/a{2}'], ['\\/a']])(
    'falls back to the hostname root for regex path %s',
    (path) => {
      expect(openUrl('app.example.com', path)).toBe('https://app.example.com');
    },
  );

  test('falls back to the root for paths that are not absolute or contain unsafe characters', () => {
    expect(openUrl('app.example.com', 'api')).toBe('https://app.example.com');
    expect(openUrl('app.example.com', '/a b')).toBe('https://app.example.com');
    expect(openUrl('app.example.com', '/a#b')).toBe('https://app.example.com');
  });

  test('treats an empty path as the root', () => {
    expect(openUrl('app.example.com', '')).toBe('https://app.example.com');
  });
});
