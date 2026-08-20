import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { APIClient } from './client';
import { APIError } from './errors';

describe('APIClient (T170)', () => {
  let client: APIClient;

  beforeEach(() => {
    client = new APIClient('http://127.0.0.1:8787');
    vi.restoreAllMocks();
  });

  afterEach(() => {
    vi.restoreAllMocks();
  });

  it('performs successful GET request and decodes JSON', async () => {
    const mockData = {
      state: 'READY',
      version: '1.0.0',
      commit_sha: '5668671',
      database_schema: 'v67',
      active_workers: 0,
      pending_tasks: 0,
      updated_at: '2026-08-20T00:00:00Z',
    };

    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify(mockData), {
        status: 200,
        headers: { 'Content-Type': 'application/json; charset=utf-8' },
      })
    );

    const result = await client.getSystemStatus();
    expect(result).toEqual(mockData);
    expect(fetchSpy).toHaveBeenCalledTimes(1);

    const calledUrl = fetchSpy.mock.calls[0][0] as string;
    const calledInit = fetchSpy.mock.calls[0][1] as RequestInit;
    expect(calledUrl).toContain('/api/v1/system/status');
    expect(new Headers(calledInit.headers).get('Accept')).toBe('application/json');
    expect(new Headers(calledInit.headers).get('X-Correlation-ID')).toMatch(/^req-/);
  });

  it('throws APIError on 4xx/5xx responses with parsed error envelope', async () => {
    const errorBody = {
      error: {
        code: 'unauthorized',
        message: 'Missing or invalid session Bearer token: eyJhbGciOi...',
        correlation_id: 'corr-12345',
      },
    };

    vi.spyOn(globalThis, 'fetch').mockResolvedValue(
      new Response(JSON.stringify(errorBody), {
        status: 401,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await expect(client.getSystemStatus()).rejects.toThrowError(APIError);

    try {
      await client.getSystemStatus();
    } catch (err: unknown) {
      if (err instanceof APIError) {
        expect(err.status).toBe(401);
        expect(err.code).toBe('unauthorized');
        // Check redaction of bearer tokens
        expect(err.message).not.toContain('eyJhbGciOi');
        expect(err.message).toContain('[REDACTED]');
      }
    }
  });

  it('STRICT INVARIANT: Never auto-retries non-idempotent mutations (POST/PUT/DELETE)', async () => {
    const fetchSpy = vi.spyOn(globalThis, 'fetch').mockRejectedValue(new Error('Network drop'));

    await expect(
      client.request('/api/v1/tasks/123/run', {
        method: 'POST',
        body: { adapter: 'codex' },
      })
    ).rejects.toThrowError(APIError);

    // Exactly 1 attempt, 0 retries!
    expect(fetchSpy).toHaveBeenCalledTimes(1);
  });

  it('rejects unexpected non-JSON content-type responses', async () => {
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response('<html><body>502 Bad Gateway from Nginx</body></html>', {
        status: 502,
        headers: { 'Content-Type': 'text/html' },
      })
    );

    await expect(client.getSystemStatus()).rejects.toThrowError(APIError);
  });

  it('honors AbortSignal cancellation', async () => {
    const controller = new AbortController();
    controller.abort();

    await expect(client.getSystemStatus(controller.signal)).rejects.toThrow();
  });

  it('verifies zero storage persistence of credentials in localStorage/sessionStorage', async () => {
    // Check that calling API client never writes to localStorage or sessionStorage
    const localSetItem = vi.spyOn(Storage.prototype, 'setItem');
    vi.spyOn(globalThis, 'fetch').mockResolvedValueOnce(
      new Response(JSON.stringify({ state: 'READY' }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      })
    );

    await client.getSystemStatus();
    expect(localSetItem).not.toHaveBeenCalled();
  });
});
