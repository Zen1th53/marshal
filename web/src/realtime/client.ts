export type SSEConnectionState = 'connecting' | 'connected' | 'disconnected' | 'reconnecting';

export interface SSEMessageEvent<T = unknown> {
  id: number;
  type: string;
  timestamp: string;
  scope?: string;
  scope_id?: string;
  data: T;
}

export type SSEEventHandler<T = unknown> = (event: SSEMessageEvent<T>) => void;
export type SSEResyncHandler = (reason?: string) => void;
export type SSEStatusHandler = (status: SSEConnectionState) => void;

const MAX_EVENT_HISTORY = 50;

export class RealtimeClient {
  private eventSource: EventSource | null = null;
  private lastEventId = 0;
  private status: SSEConnectionState = 'disconnected';
  private reconnectAttempts = 0;
  private reconnectTimer: ReturnType<typeof setTimeout> | null = null;
  private handlers = new Map<string, Set<SSEEventHandler>>();
  private resyncHandlers = new Set<SSEResyncHandler>();
  private statusHandlers = new Set<SSEStatusHandler>();
  private eventHistory: SSEMessageEvent[] = [];
  private isClosedExplicitly = false;
  private readonly endpoint: string;

  constructor(endpoint = '/api/v1/events/stream') {
    this.endpoint = endpoint;
  }

  connect(): void {
    if (this.eventSource || this.status === 'connected' || this.status === 'connecting') {
      return;
    }

    this.isClosedExplicitly = false;
    this.setStatus('connecting');

    // Build URL with optional last_event_id for reconnect backfill without passing tokens
    const url = new URL(this.endpoint, window.location.origin);
    if (this.lastEventId > 0) {
      url.searchParams.set('last_event_id', String(this.lastEventId));
    }

    try {
      this.eventSource = new EventSource(url.toString(), { withCredentials: true });
    } catch {
      this.handleDisconnect();
      return;
    }

    this.eventSource.onopen = () => {
      this.setStatus('connected');
      this.reconnectAttempts = 0;
    };

    this.eventSource.onerror = () => {
      this.handleDisconnect();
    };

    this.eventSource.onmessage = (e) => {
      this.handleRawMessage(e);
    };
  }

  private handleDisconnect(): void {
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }

    if (this.isClosedExplicitly) {
      this.setStatus('disconnected');
      return;
    }

    this.setStatus('reconnecting');
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) return;
    this.reconnectAttempts++;
    // Exponential backoff with jitter (max 10s)
    const baseDelay = Math.min(1000 * Math.pow(1.5, this.reconnectAttempts), 10000);
    const jitter = Math.random() * 500;
    const delay = baseDelay + jitter;

    this.reconnectTimer = setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, delay);
  }

  private handleRawMessage(e: MessageEvent): void {
    try {
      if (e.lastEventId) {
        const idNum = parseInt(e.lastEventId, 10);
        if (!isNaN(idNum) && idNum > this.lastEventId) {
          this.lastEventId = idNum;
        }
      }

      const parsed = JSON.parse(e.data) as SSEMessageEvent;

      // Handle resync command
      if (parsed.type === 'resync') {
        this.resyncHandlers.forEach((h) => h((parsed.data as { reason?: string })?.reason));
        return;
      }

      // Deduplicate out-of-order or re-sent events
      if (parsed.id && parsed.id <= this.lastEventId && this.eventHistory.some((ev) => ev.id === parsed.id)) {
        return;
      }

      if (parsed.id && parsed.id > this.lastEventId) {
        this.lastEventId = parsed.id;
      }

      // Record in bounded history
      this.eventHistory.push(parsed);
      if (this.eventHistory.length > MAX_EVENT_HISTORY) {
        this.eventHistory.shift();
      }

      // Dispatch to typed handlers safely
      const typeHandlers = this.handlers.get(parsed.type);
      if (typeHandlers) {
        typeHandlers.forEach((h) => {
          try {
            h(parsed);
          } catch {
            // Ignore subscriber error to prevent breaking stream
          }
        });
      }

      // Global wildcard handlers
      const wildcard = this.handlers.get('*');
      if (wildcard) {
        wildcard.forEach((h) => h(parsed));
      }
    } catch {
      // Reject malformed JSON payload safely
    }
  }

  subscribe<T>(type: string, handler: SSEEventHandler<T>): () => void {
    if (!this.handlers.has(type)) {
      this.handlers.set(type, new Set());
    }
    const set = this.handlers.get(type)!;
    set.add(handler as SSEEventHandler);

    return () => {
      set.delete(handler as SSEEventHandler);
      if (set.size === 0) {
        this.handlers.delete(type);
      }
    };
  }

  onResync(handler: SSEResyncHandler): () => void {
    this.resyncHandlers.add(handler);
    return () => this.resyncHandlers.delete(handler);
  }

  onStatusChange(handler: SSEStatusHandler): () => void {
    this.statusHandlers.add(handler);
    handler(this.status);
    return () => this.statusHandlers.delete(handler);
  }

  getStatus(): SSEConnectionState {
    return this.status;
  }

  getLastEventId(): number {
    return this.lastEventId;
  }

  private setStatus(status: SSEConnectionState): void {
    if (this.status !== status) {
      this.status = status;
      this.statusHandlers.forEach((h) => h(status));
    }
  }

  disconnect(): void {
    this.isClosedExplicitly = true;
    if (this.reconnectTimer) {
      clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
    if (this.eventSource) {
      this.eventSource.close();
      this.eventSource = null;
    }
    this.setStatus('disconnected');
  }
}

export const realtime = new RealtimeClient();
