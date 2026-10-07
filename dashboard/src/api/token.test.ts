import { describe, expect, test } from 'vitest';
import { createTokenStore, type KeyValueStorage } from './token';

function memoryStorage(): KeyValueStorage {
  const data = new Map<string, string>();
  return {
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => void data.set(k, v),
    removeItem: (k) => void data.delete(k),
  };
}

function throwingStorage(): KeyValueStorage {
  const fail = () => {
    throw new Error('SecurityError: storage disabled');
  };
  return { getItem: fail, setItem: fail, removeItem: fail };
}

describe('token store', () => {
  test('returns null when no token has been saved', () => {
    const store = createTokenStore(memoryStorage());

    expect(store.read()).toBeNull();
  });

  test('saves a trimmed token and reads it back', () => {
    const store = createTokenStore(memoryStorage());

    store.save('  secret-token  ');

    expect(store.read()).toBe('secret-token');
  });

  test('clears a saved token', () => {
    const store = createTokenStore(memoryStorage());
    store.save('secret-token');

    store.clear();

    expect(store.read()).toBeNull();
  });

  test('keeps working in memory when browser storage throws', () => {
    const store = createTokenStore(throwingStorage());

    store.save('secret-token');

    expect(store.read()).toBe('secret-token');
  });

  test('works without any browser storage', () => {
    const store = createTokenStore(null);

    store.save('secret-token');
    expect(store.read()).toBe('secret-token');
    store.clear();
    expect(store.read()).toBeNull();
  });
});
