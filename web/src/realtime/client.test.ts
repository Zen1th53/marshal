import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { RealtimeClient } from './client';

class MockEventSource {
  public url: string;
  public onopen: (() => void) | null = null;
  public onerror: (() => void) | null = null;
  public onmessage: ((e: MessageEvent) => void) | null = null;
  public closed = false;

  constructor(url: string) {
    this.url = url;
  }

  close() {
    this.closed = true;
  }

  simulateOpen() {
    if (this.onopen) this.onopen();
  }

  simulateMessage(id: number, type: string, data: unknown) {
    if (this.onmessage) {
      this.onmessage(
        new MessageEvent('message', {
          data: JSON.stringify({ id, type, timestamp: new Date().toISOString(), data }),
          lastEventId: String(id),
        })
      );
    }
  }
}

describe('RealtimeClient (T178)', () => {
  let client: RealtimeClient;
  let mockES: MockEventSource | null = null;

  beforeEach(() => {
    mockES = null;
    vi.stubGlobal('EventSource', class extends MockEventSource {
      constructor(url: string) {
        super(url);
        // eslint-disable-next-line @typescript-eslint/no-this-alias
        mockES = this;
      }
    });
    client = new RealtimeClient('/api/v1/events/stream');
  });

  afterEach(() => {
    client.disconnect();
    vi.unstubAllGlobals();
  });

  it('connects and receives typed events', () => {
    const taskHandler = vi.fn();
    client.subscribe('task.status', taskHandler);

    client.connect();
    expect(mockES).not.toBeNull();
    mockES?.simulateOpen();
    expect(client.getStatus()).toBe('connected');

    mockES?.simulateMessage(1, 'task.status', { taskId: 'T-1', status: 'running' });
    expect(taskHandler).toHaveBeenCalledWith(
      expect.objectContaining({
        id: 1,
        type: 'task.status',
        data: { taskId: 'T-1', status: 'running' },
      })
    );
    expect(client.getLastEventId()).toBe(1);
  });

  it('deduplicates duplicate event IDs', () => {
    const handler = vi.fn();
    client.subscribe('audit.log', handler);
    client.connect();
    mockES?.simulateOpen();

    mockES?.simulateMessage(1, 'audit.log', { msg: 'First' });
    mockES?.simulateMessage(1, 'audit.log', { msg: 'Duplicate' });

    expect(handler).toHaveBeenCalledTimes(1);
  });

  it('triggers onResync callback on resync signal', () => {
    const resyncHandler = vi.fn();
    client.onResync(resyncHandler);
    client.connect();
    mockES?.simulateOpen();

    mockES?.simulateMessage(100, 'resync', { reason: 'replay_window_expired' });
    expect(resyncHandler).toHaveBeenCalledWith('replay_window_expired');
  });

  it('safely discards malformed or unknown event without throwing', () => {
    client.connect();
    mockES?.simulateOpen();

    expect(() => {
      mockES?.onmessage?.(new MessageEvent('message', { data: 'NOT_VALID_JSON{[' }));
    }).not.toThrow();
  });

  it('closes connection on disconnect', () => {
    client.connect();
    expect(mockES?.closed).toBe(false);
    client.disconnect();
    expect(mockES?.closed).toBe(true);
    expect(client.getStatus()).toBe('disconnected');
  });
});
