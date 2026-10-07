import { afterEach, beforeEach, describe, expect, test, vi } from 'vitest';
import { ApiError, UnauthorizedError, fetchAPI, verifyToken } from './client';
import { tokenStore } from './token';

function respond(status: number, body: unknown = {}) {
  return Promise.resolve(new Response(JSON.stringify(body), { status, statusText: `status ${status}` }));
}

describe('fetchAPI', () => {
  const fetchMock = vi.fn();

  beforeEach(() => {
    vi.stubGlobal('fetch', fetchMock);
    tokenStore.clear();
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    fetchMock.mockReset();
  });

  test('requests the API path with query parameters and no auth header by default', async () => {
    fetchMock.mockReturnValue(respond(200, { ok: true }));

    const data = await fetchAPI<{ ok: boolean }>('/resources/dns', { status: 'error', agent_id: '' });

    expect(data).toEqual({ ok: true });
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe('/api/resources/dns?status=error');
    expect(init.headers.Authorization).toBeUndefined();
  });

  test('sends the saved token as a bearer header', async () => {
    tokenStore.save('secret-token');
    fetchMock.mockReturnValue(respond(200));

    await fetchAPI('/overview');

    const [, init] = fetchMock.mock.calls[0];
    expect(init.headers.Authorization).toBe('Bearer secret-token');
  });

  test('throws UnauthorizedError on 401', async () => {
    fetchMock.mockReturnValue(respond(401, { error: 'unauthorized' }));

    await expect(fetchAPI('/overview')).rejects.toBeInstanceOf(UnauthorizedError);
  });

  test('throws ApiError with the status for other failures', async () => {
    fetchMock.mockReturnValue(respond(500));

    const err = await fetchAPI('/overview').catch((e: unknown) => e);

    expect(err).toBeInstanceOf(ApiError);
    expect(err).not.toBeInstanceOf(UnauthorizedError);
    expect((err as ApiError).status).toBe(500);
  });
});

describe('verifyToken', () => {
  const fetchMock = vi.fn();

  beforeEach(() => vi.stubGlobal('fetch', fetchMock));
  afterEach(() => {
    vi.unstubAllGlobals();
    fetchMock.mockReset();
  });

  test('returns true when the server accepts the token', async () => {
    fetchMock.mockReturnValue(respond(200, { version: '0.1.8' }));

    await expect(verifyToken('good')).resolves.toBe(true);
    expect(fetchMock.mock.calls[0][1].headers.Authorization).toBe('Bearer good');
  });

  test('returns false when the server rejects the token', async () => {
    fetchMock.mockReturnValue(respond(401));

    await expect(verifyToken('bad')).resolves.toBe(false);
  });

  test('throws when the server fails for another reason', async () => {
    fetchMock.mockReturnValue(respond(503));

    await expect(verifyToken('any')).rejects.toBeInstanceOf(ApiError);
  });
});
