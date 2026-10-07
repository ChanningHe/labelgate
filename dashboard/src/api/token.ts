// API token persistence for servers that set api.token.
// The token lives in sessionStorage so it is forgotten when the tab closes.
// When storage is unavailable (private mode, blocked site data) it falls back
// to memory, so the dashboard still works for the current page load.

const TOKEN_KEY = 'labelgate.apiToken';

export interface KeyValueStorage {
  getItem(key: string): string | null;
  setItem(key: string, value: string): void;
  removeItem(key: string): void;
}

export interface TokenStore {
  read(): string | null;
  save(token: string): void;
  clear(): void;
}

export function createTokenStore(storage: KeyValueStorage | null): TokenStore {
  let memory: string | null = null;

  const attempt = (fn: (s: KeyValueStorage) => void) => {
    if (!storage) return;
    try {
      fn(storage);
    } catch {
      // Storage blocked; the in-memory copy remains authoritative.
    }
  };

  return {
    read() {
      if (memory !== null) return memory;
      if (!storage) return null;
      try {
        memory = storage.getItem(TOKEN_KEY);
      } catch {
        memory = null;
      }
      return memory;
    },
    save(token) {
      memory = token.trim();
      attempt((s) => s.setItem(TOKEN_KEY, memory as string));
    },
    clear() {
      memory = null;
      attempt((s) => s.removeItem(TOKEN_KEY));
    },
  };
}

function browserSessionStorage(): KeyValueStorage | null {
  try {
    return typeof window === 'undefined' ? null : window.sessionStorage;
  } catch {
    return null;
  }
}

export const tokenStore = createTokenStore(browserSessionStorage());
