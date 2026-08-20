import { APIError } from './errors';
import type { APIErrorEnvelope } from './errors';
import { fetchCSRFToken } from './csrf';
import type {
  SystemStatusDTO,
  AdapterSummaryDTO,
  PagedResponse,
  AgentSummaryDTO,
  TaskSummaryDTO,
  TaskStatus,
  MemoryRecordDTO,
} from './types';

export interface RequestOptions extends Omit<RequestInit, 'body'> {
  signal?: AbortSignal;
  params?: Record<string, string | number | boolean | undefined>;
  timeoutMs?: number;
  body?: unknown;
}

export class APIClient {
  private readonly baseUrl: string;

  constructor(baseUrl = '') {
    this.baseUrl = baseUrl.replace(/\/$/, '');
  }

  private generateCorrelationId(): string {
    return `req-${Date.now()}-${Math.random().toString(36).slice(2, 9)}`;
  }

  async request<T>(path: string, options: RequestOptions = {}): Promise<T> {
    const method = (options.method || 'GET').toUpperCase();
    const isRead = method === 'GET' || method === 'HEAD';
    const maxRetries = isRead ? 2 : 0; // STRICT INVARIANT: Never auto-retry non-idempotent mutations

    let attempt = 0;
    while (true) {
      attempt++;
      try {
        return await this.executeFetch<T>(path, method, options);
      } catch (err: unknown) {
        if (err instanceof APIError && err.status < 500) {
          // Client errors (4xx) should never be retried
          throw err;
        }
        if (options.signal?.aborted) {
          throw err;
        }
        if (attempt > maxRetries) {
          throw err;
        }
        // Exponential backoff with jitter
        await new Promise((resolve) => setTimeout(resolve, attempt * 100));
      }
    }
  }

  private async executeFetch<T>(
    path: string,
    method: string,
    options: RequestOptions
  ): Promise<T> {
    const url = new URL(`${this.baseUrl}${path.startsWith('/') ? path : `/${path}`}`, window.location.origin);
    if (options.params) {
      for (const [key, value] of Object.entries(options.params)) {
        if (value !== undefined) {
          url.searchParams.set(key, String(value));
        }
      }
    }

    const correlationId = this.generateCorrelationId();
    const headers = new Headers(options.headers || {});
    headers.set('Accept', 'application/json');
    headers.set('X-Correlation-ID', correlationId);

    // For state-changing mutations, attach X-CSRF-Token
    if (method !== 'GET' && method !== 'HEAD' && method !== 'OPTIONS') {
      const csrfToken = await fetchCSRFToken();
      if (csrfToken) {
        headers.set('X-CSRF-Token', csrfToken);
      }
    }

    let body: BodyInit | undefined;
    if (options.body !== undefined) {
      headers.set('Content-Type', 'application/json');
      body = JSON.stringify(options.body);
    }

    const timeoutMs = options.timeoutMs ?? 15000;
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(new DOMException('Request timeout', 'TimeoutError')), timeoutMs);

    // Combine signals if external signal passed
    if (options.signal) {
      options.signal.addEventListener('abort', () => controller.abort(options.signal?.reason));
    }

    let response: Response;
    try {
      response = await fetch(url.toString(), {
        ...options,
        method,
        headers,
        body,
        signal: controller.signal,
      });
    } catch (err: unknown) {
      if (err instanceof DOMException && err.name === 'TimeoutError') {
        throw new APIError(408, 'request_timeout', 'Request timed out', correlationId);
      }
      if (err instanceof DOMException && err.name === 'AbortError') {
        throw err;
      }
      throw new APIError(0, 'network_error', err instanceof Error ? err.message : 'Network request failed', correlationId);
    } finally {
      clearTimeout(timeoutId);
    }

    const contentType = response.headers.get('content-type') || '';
    if (!contentType.includes('application/json')) {
      if (!response.ok) {
        throw new APIError(
          response.status,
          'unexpected_response',
          `Server returned non-JSON response with status ${response.status}`,
          correlationId
        );
      }
      throw new APIError(
        response.status,
        'invalid_content_type',
        `Expected application/json response but received '${contentType}'`,
        correlationId
      );
    }

    const data = await response.json();
    if (!response.ok) {
      const errorEnvelope = data as APIErrorEnvelope;
      const code = errorEnvelope.error?.code || 'unknown_error';
      const message = errorEnvelope.error?.message || `HTTP error ${response.status}`;
      const respCorrelation = errorEnvelope.error?.correlation_id || correlationId;
      throw new APIError(response.status, code, message, respCorrelation);
    }

    return data as T;
  }

  // Convenience Typed Endpoints
  getSystemStatus(signal?: AbortSignal): Promise<SystemStatusDTO> {
    return this.request<SystemStatusDTO>('/api/v1/system/status', { method: 'GET', signal });
  }

  getAdapters(signal?: AbortSignal): Promise<AdapterSummaryDTO[]> {
    return this.request<AdapterSummaryDTO[]>('/api/v1/system/adapters', { method: 'GET', signal });
  }

  getCapabilities(signal?: AbortSignal): Promise<{ capabilities: Record<string, { state: string; reason?: string }> }> {
    return this.request('/api/v1/system/capabilities', { method: 'GET', signal });
  }

  getOverview(signal?: AbortSignal): Promise<{
    system_status: SystemStatusDTO;
    tasks: { active: number; queued: number; blocked: number; review: number; completed: number; failed: number; total: number };
    agents: { total: number; active: number; idle: number };
    providers: AdapterSummaryDTO[];
    memory_health: string;
    security_notices: Array<{ level: string; title: string; message: string; created_at: string }>;
    evaluated_at: string;
  }> {
    return this.request('/api/v1/overview', { method: 'GET', signal });
  }

  getAgents(signal?: AbortSignal): Promise<PagedResponse<AgentSummaryDTO>> {
    return this.request<PagedResponse<AgentSummaryDTO>>('/api/v1/agents', { method: 'GET', signal });
  }

  getAgentDetail(id: string, signal?: AbortSignal): Promise<{
    id: string;
    name: string;
    provider: string;
    model: string;
    status: string;
    capabilities: string[];
    current_task_id?: string;
    current_run_id?: string;
    completed_task_count: number;
    failed_task_count: number;
    last_heartbeat: string;
    created_at: string;
    memory_contributions: {
      episodes_extracted: number;
      decisions_logged: number;
      facts_asserted: number;
    };
  }> {
    return this.request(`/api/v1/agents/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  getTasks(signal?: AbortSignal): Promise<PagedResponse<TaskSummaryDTO>> {
    return this.request<PagedResponse<TaskSummaryDTO>>('/api/v1/tasks', { method: 'GET', signal });
  }

  getTaskDetail(id: string, signal?: AbortSignal): Promise<{
    id: string;
    title: string;
    description: string;
    status: TaskStatus;
    risk: string;
    assigned_to?: string;
    base_commit: string;
    head_commit: string;
    head_mismatch_detected?: boolean;
    approvals_count?: number;
    required_quorum?: number;
    stale_approval_detected?: boolean;
    correlation_id?: string;
    runs_count?: number;
    created_at: string;
    updated_at: string;
    lifecycle_history?: Array<{
      timestamp: string;
      actor: string;
      state: string;
      message: string;
    }>;
    runs?: Array<{
      run_id: string;
      status: string;
      step_count: number;
      duration_ms: number;
      started_at: string;
    }>;
  }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  createTask(payload: {
    title: string;
    description: string;
    risk: string;
    assigned_to?: string;
    dependencies?: string[];
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<TaskSummaryDTO> {
    return this.request<TaskSummaryDTO>('/api/v1/tasks', {
      method: 'POST',
      body: JSON.stringify({
        idempotency_key: idempotencyKey,
        payload,
      }),
      signal,
    });
  }

  updateTask(id: string, payload: {
    title?: string;
    description?: string;
    risk?: string;
    assigned_to?: string;
    dependencies?: string[];
  }, expectedRevision?: number, signal?: AbortSignal): Promise<{ id: string; revision: number; status: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify({
        expected_revision: expectedRevision,
        payload,
      }),
      signal,
    });
  }

  claimTask(id: string, signal?: AbortSignal): Promise<{ task_id: string; agent_id: string; lease_id: string; expires_at: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/claim`, { method: 'POST', signal });
  }

  runTask(id: string, signal?: AbortSignal): Promise<{ run_id: string; task_id: string; status: string; started_at: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/run`, { method: 'POST', signal });
  }

  cancelTask(id: string, signal?: AbortSignal): Promise<{ task_id: string; status: string; canceled_at: string; reason?: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/cancel`, { method: 'POST', signal });
  }

  retryTask(id: string, signal?: AbortSignal): Promise<{ run_id: string; task_id: string; status: string; started_at: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/retry`, { method: 'POST', signal });
  }

  listRuns(params?: {
    task_id?: string;
    agent_id?: string;
    status?: string;
    limit?: number;
    offset?: number;
  }, signal?: AbortSignal): Promise<{
    items: Array<{
      run_id: string;
      task_id: string;
      agent_id: string;
      provider: string;
      status: string;
      duration_ms: number;
      step_count: number;
      evidence_count: number;
      base_commit: string;
      head_commit: string;
      started_at: string;
      finished_at?: string;
    }>;
    total_count: number;
    limit: number;
    offset: number;
  }> {
    const sp = new URLSearchParams();
    if (params?.task_id) sp.set('task_id', params.task_id);
    if (params?.agent_id) sp.set('agent_id', params.agent_id);
    if (params?.status) sp.set('status', params.status);
    if (params?.limit) sp.set('limit', String(params.limit));
    if (params?.offset) sp.set('offset', String(params.offset));
    const qs = sp.toString();
    return this.request(`/api/v1/runs${qs ? `?${qs}` : ''}`, { method: 'GET', signal });
  }

  getRunDetail(id: string, signal?: AbortSignal): Promise<{
    run_id: string;
    task_id: string;
    agent_id: string;
    provider: string;
    status: string;
    duration_ms: number;
    step_count: number;
    evidence_count: number;
    base_commit: string;
    head_commit: string;
    started_at: string;
    finished_at?: string;
    correlation_id: string;
    summary: string;
    logs: Array<{
      index: number;
      timestamp: string;
      stream: string;
      message: string;
    }>;
  }> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  getRunLogs(id: string, cursor = 0, limit = 100, signal?: AbortSignal): Promise<{
    run_id: string;
    lines: Array<{
      index: number;
      timestamp: string;
      stream: string;
      message: string;
    }>;
    total_lines: number;
    is_truncated: boolean;
    next_cursor: number;
  }> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}/logs?cursor=${cursor}&limit=${limit}`, { method: 'GET', signal });
  }

  getRunResult(id: string, signal?: AbortSignal): Promise<{
    run_id: string;
    base_commit: string;
    head_commit: string;
    files_summary: Array<{
      path: string;
      status: string;
      insertions: number;
      deletions: number;
    }>;
    artifacts: Array<{
      id: string;
      name: string;
      sha256: string;
      size_bytes: number;
      content_type: string;
    }>;
    worktree_status: string;
    checkpoint_id?: string;
    can_recover: boolean;
    created_at: string;
  }> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}/result`, { method: 'GET', signal });
  }

  recoverRun(id: string, signal?: AbortSignal): Promise<{ run_id: string; checkpoint_id: string; recovered_at: string; status: string }> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}/recover`, { method: 'POST', signal });
  }

  getTaskDAG(maxDepth = 5, signal?: AbortSignal): Promise<{
    nodes: Array<{
      id: string;
      title: string;
      status: TaskStatus;
      risk: string;
      assigned_to?: string;
      layer: number;
    }>;
    edges: Array<{
      source_id: string;
      target_id: string;
      type: string;
    }>;
    has_cycles: boolean;
    cycle_path?: string[];
    max_depth: number;
  }> {
    return this.request(`/api/v1/tasks/dag?max_depth=${encodeURIComponent(maxDepth)}`, { method: 'GET', signal });
  }

  searchMemory(query: string, signal?: AbortSignal): Promise<PagedResponse<MemoryRecordDTO>> {
    return this.request<PagedResponse<MemoryRecordDTO>>('/api/v1/memory/search', {
      method: 'GET',
      params: { q: query },
      signal,
    });
  }
}

export const api = new APIClient();
