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

  getReviewQueue(params?: {
    stage?: string;
    risk?: string;
    owner?: string;
    limit?: number;
    offset?: number;
  }, signal?: AbortSignal): Promise<{
    items: Array<{
      task_id: string;
      title: string;
      stage: string;
      risk: string;
      owner: string;
      base_commit: string;
      head_commit: string;
      is_stale_head: boolean;
      approvals_count: number;
      required_quorum: number;
      blocker_count: number;
      submitted_at: string;
    }>;
    total_count: number;
    limit: number;
    offset: number;
  }> {
    const sp = new URLSearchParams();
    if (params?.stage) sp.set('stage', params.stage);
    if (params?.risk) sp.set('risk', params.risk);
    if (params?.owner) sp.set('owner', params.owner);
    if (params?.limit) sp.set('limit', String(params.limit));
    if (params?.offset) sp.set('offset', String(params.offset));
    const qs = sp.toString();
    return this.request(`/api/v1/review/queue${qs ? `?${qs}` : ''}`, { method: 'GET', signal });
  }

  getTaskQuorum(id: string, signal?: AbortSignal): Promise<{
    task_id: string;
    head_commit: string;
    required_quorum: number;
    current_approvals_count: number;
    has_veto: boolean;
    veto_reason?: string;
    is_quorum_met: boolean;
    independence_note: string;
    attestations: Array<{
      reviewer_id: string;
      provider: string;
      role: string;
      decision: string;
      comment: string;
      commit_hash: string;
      signed_at: string;
    }>;
  }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/quorum`, { method: 'GET', signal });
  }

  submitQuorumDecision(id: string, payload: {
    decision: 'approved' | 'rejected' | 'vetoed';
    comment: string;
    commit_hash: string;
  }, signal?: AbortSignal): Promise<{ task_id: string; decision: string; status: string }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/quorum/decision`, {
      method: 'POST',
      body: JSON.stringify({ payload }),
      signal,
    });
  }

  getMergePreflight(id: string, signal?: AbortSignal): Promise<{
    task_id: string;
    is_eligible: boolean;
    expected_head: string;
    target_branch: string;
    quorum_met: boolean;
    has_veto: boolean;
    is_stale_head: boolean;
    gating_checks: string[];
    denial_reason?: string;
  }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/merge/preflight`, { method: 'GET', signal });
  }

  executeMerge(id: string, payload: {
    expected_head: string;
    strategy: string;
  }, signal?: AbortSignal): Promise<{
    task_id: string;
    merged: boolean;
    merge_commit: string;
    target_branch: string;
    merged_at: string;
    correlation_id: string;
  }> {
    return this.request(`/api/v1/tasks/${encodeURIComponent(id)}/merge`, {
      method: 'POST',
      body: JSON.stringify({ payload }),
      signal,
    });
  }

  listEvidence(params?: {
    task_id?: string;
    type?: string;
    limit?: number;
    offset?: number;
  }, signal?: AbortSignal): Promise<{
    items: Array<{
      id: string;
      task_id: string;
      run_id: string;
      type: string;
      producer: string;
      digest: string;
      size_bytes: number;
      integrity_status: string;
      created_at: string;
    }>;
    total_count: number;
    limit: number;
    offset: number;
  }> {
    const sp = new URLSearchParams();
    if (params?.task_id) sp.set('task_id', params.task_id);
    if (params?.type) sp.set('type', params.type);
    if (params?.limit) sp.set('limit', String(params.limit));
    if (params?.offset) sp.set('offset', String(params.offset));
    const qs = sp.toString();
    return this.request(`/api/v1/evidence${qs ? `?${qs}` : ''}`, { method: 'GET', signal });
  }

  getEvidenceDetail(id: string, signal?: AbortSignal): Promise<{
    id: string;
    task_id: string;
    run_id: string;
    type: string;
    producer: string;
    digest: string;
    calculated_digest: string;
    integrity_status: string;
    artifact_id?: string;
    signature: string;
    payload: Record<string, any>;
    created_at: string;
  }> {
    return this.request(`/api/v1/evidence/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  getProvenanceTrace(targetId?: string, depth = 3, signal?: AbortSignal): Promise<{
    target_id: string;
    root_node: {
      id: string;
      type: string;
      title: string;
      producer: string;
      timestamp: string;
      relationship: string;
      is_proven_binding: boolean;
    };
    nodes: Array<{
      id: string;
      type: string;
      title: string;
      producer: string;
      timestamp: string;
      relationship: string;
      is_proven_binding: boolean;
      parent_id?: string;
    }>;
    max_depth: number;
    total_nodes: number;
    generated_at: string;
  }> {
    const sp = new URLSearchParams();
    if (targetId) sp.set('target_id', targetId);
    sp.set('depth', String(depth));
    return this.request(`/api/v1/provenance/trace?${sp.toString()}`, { method: 'GET', signal });
  }

  getProviders(signal?: AbortSignal): Promise<{
    providers: Array<{
      id: string;
      name: string;
      class: string;
      probe_status: string;
      capabilities: string[];
      models: Array<{
        id: string;
        context_window: number;
        latency_p95_ms: number;
      }>;
      last_probed_at: string;
    }>;
    routing_decisions: Array<{
      intent: string;
      selected_model: string;
      provider_id: string;
      rationale: string;
      is_pinned: boolean;
    }>;
    last_evaluated_at: string;
  }> {
    return this.request('/api/v1/providers', { method: 'GET', signal });
  }

  overrideRouter(payload: {
    intent: string;
    model_id: string;
    is_pinned: boolean;
  }, signal?: AbortSignal): Promise<{ intent: string; model_id: string; is_pinned: boolean; status: string }> {
    return this.request('/api/v1/providers/router/override', {
      method: 'POST',
      body: JSON.stringify({ payload }),
      signal,
    });
  }

  getSecurityPolicy(signal?: AbortSignal): Promise<{
    policy_id: string;
    revision: number;
    global_risk_level: string;
    degraded_controls: string[];
    gate_rules: Array<{
      id: string;
      name: string;
      enforcement: string;
      status: string;
      description: string;
      last_evaluated_at: string;
    }>;
    capability_rules: Array<{
      capability_name: string;
      required_role: string;
      decision: string;
      denial_reason?: string;
    }>;
    last_audited_at: string;
  }> {
    return this.request('/api/v1/security/policy', { method: 'GET', signal });
  }

  getRunBoundary(id: string, signal?: AbortSignal): Promise<{
    run_id: string;
    sandbox_backend: string;
    backend_status: string;
    network_policy: string;
    is_network_isolated: boolean;
    cpu_quota_pct: number;
    memory: { limit: number; used: number; unit: string; usage_pct: number };
    pids: { limit: number; used: number; unit: string; usage_pct: number };
    disk: { limit: number; used: number; unit: string; usage_pct: number };
    was_oom_killed: boolean;
    was_pid_exhausted: boolean;
    was_disk_exhausted: boolean;
    mounted_paths: string[];
    audited_at: string;
  }> {
    return this.request(`/api/v1/runs/${encodeURIComponent(id)}/boundary`, { method: 'GET', signal });
  }

  listAuditEvents(params?: {
    outcome?: string;
    action?: string;
    actor?: string;
    correlation_id?: string;
    limit?: number;
    offset?: number;
  }, signal?: AbortSignal): Promise<{
    events: Array<{
      id: string;
      actor: { principal_id: string; role: string };
      action: string;
      resource_type: string;
      resource_id: string;
      outcome: string;
      correlation_id: string;
      timestamp: string;
      details: Record<string, any>;
    }>;
    total_count: number;
    limit: number;
    offset: number;
  }> {
    const sp = new URLSearchParams();
    if (params?.outcome) sp.set('outcome', params.outcome);
    if (params?.action) sp.set('action', params.action);
    if (params?.actor) sp.set('actor', params.actor);
    if (params?.correlation_id) sp.set('correlation_id', params.correlation_id);
    if (params?.limit) sp.set('limit', String(params.limit));
    if (params?.offset) sp.set('offset', String(params.offset));
    const qs = sp.toString();
    return this.request(`/api/v1/audit/events${qs ? `?${qs}` : ''}`, { method: 'GET', signal });
  }

  searchMemory(params?: {
    query?: string;
    scope?: string;
    kind?: string;
    lifecycle?: string;
    limit?: number;
    offset?: number;
  }, signal?: AbortSignal): Promise<{
    items: Array<{
      id: string;
      project_id: string;
      scope: string;
      scope_id: string;
      kind: string;
      title: string;
      body: string;
      lifecycle: string;
      authority: string;
      confidence: number;
      observed_at: string;
      retrieval_score: number;
      retrieval_reason: string;
    }>;
    total_count: number;
    limit: number;
    offset: number;
    index_status: string;
  }> {
    const sp = new URLSearchParams();
    if (params?.query) sp.set('query', params.query);
    if (params?.scope) sp.set('scope', params.scope);
    if (params?.kind) sp.set('kind', params.kind);
    if (params?.lifecycle) sp.set('lifecycle', params.lifecycle);
    if (params?.limit) sp.set('limit', String(params.limit));
    if (params?.offset) sp.set('offset', String(params.offset));
    const qs = sp.toString();
    return this.request(`/api/v1/memory/search${qs ? `?${qs}` : ''}`, { method: 'GET', signal });
  }

  getMemoryRecord(id: string, signal?: AbortSignal): Promise<{
    id: string;
    project_id: string;
    scope: string;
    scope_id: string;
    kind: string;
    title: string;
    body: string;
    lifecycle: string;
    authority: string;
    confidence: number;
    observed_at: string;
    retrieval_score: number;
    retrieval_reason: string;
  }> {
    return this.request(`/api/v1/memory/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  getMemoryDetail(id: string, signal?: AbortSignal): Promise<{
    id: string;
    project_id: string;
    scope: string;
    scope_id: string;
    kind: string;
    title: string;
    body: string;
    lifecycle: string;
    authority: string;
    confidence: number;
    digest_sha256: string;
    revision: number;
    is_encrypted: boolean;
    observed_at: string;
    expires_at?: string;
    provenance: {
      producer_agent_id: string;
      source_run_id: string;
      correlation_id: string;
      evidence_ids: string[];
      created_at: string;
    };
    lineage: {
      supersedes_id?: string;
      superseded_by_id?: string;
      conflict_status: string;
      lineage_depth: number;
    };
  }> {
    return this.request(`/api/v1/memory/${encodeURIComponent(id)}/detail`, { method: 'GET', signal });
  }

  explainRetrieval(query?: string, signal?: AbortSignal): Promise<{
    query: string;
    embedder_model: string;
    embedder_status: string;
    fusion_algorithm: string;
    candidates: Array<{
      memory_id: string;
      title: string;
      kind: string;
      scope: string;
      lexical_rank: number;
      lexical_score: number;
      dense_rank: number;
      dense_score: number;
      graph_bonus: number;
      freshness_penalty: number;
      final_rrf_score: number;
      rerank_rationale: string;
    }>;
    evaluated_at: string;
  }> {
    const qParam = query ? `?query=${encodeURIComponent(query)}` : '';
    return this.request(`/api/v1/memory/retrieval/explain${qParam}`, { method: 'GET', signal });
  }

  listGovernanceQueue(category?: string, signal?: AbortSignal): Promise<{
    items: Array<{
      id: string;
      category: string;
      status: string;
      target_memory_id: string;
      conflict_with_id?: string;
      reason: string;
      detected_at: string;
    }>;
    total_count: number;
  }> {
    const qParam = category && category !== 'all' ? `?category=${encodeURIComponent(category)}` : '';
    return this.request(`/api/v1/memory/governance/queue${qParam}`, { method: 'GET', signal });
  }

  getConflictComparison(id: string, signal?: AbortSignal): Promise<{
    conflict_id: string;
    status: string;
    resolution_mode: string;
    base_memory: {
      id: string;
      title: string;
      body: string;
      authority: string;
      confidence: number;
      scope: string;
      kind: string;
      observed_at: string;
    };
    competing_memory: {
      id: string;
      title: string;
      body: string;
      authority: string;
      confidence: number;
      scope: string;
      kind: string;
      observed_at: string;
    };
    detected_at: string;
  }> {
    return this.request(`/api/v1/memory/governance/conflicts/${encodeURIComponent(id)}`, { method: 'GET', signal });
  }

  getWorkingMemory(signal?: AbortSignal): Promise<{
    slots: Array<{
      slot_key: string;
      owner_scope: string;
      scope_id: string;
      content: string;
      revision: number;
      is_pinned: boolean;
      is_private: boolean;
      allocated_bytes: number;
      expires_at: string;
      last_updated_at: string;
    }>;
    total_quota_bytes: number;
    used_bytes: number;
    eviction_strategy: string;
  }> {
    return this.request('/api/v1/memory/working', { method: 'GET', signal });
  }

  updateWorkingSlot(payload: {
    slot_key: string;
    expected_revision: number;
    content: string;
    is_pinned: boolean;
  }, signal?: AbortSignal): Promise<{
    slot_key: string;
    owner_scope: string;
    scope_id: string;
    content: string;
    revision: number;
    is_pinned: boolean;
    is_private: boolean;
    allocated_bytes: number;
    expires_at: string;
    last_updated_at: string;
  }> {
    return this.request('/api/v1/memory/working/slot', {
      method: 'POST',
      body: JSON.stringify(payload),
      signal,
    });
  }

  promoteWorkingSlot(payload: {
    slot_key: string;
    target_title: string;
  }, signal?: AbortSignal): Promise<{
    slot_key: string;
    candidate_memory_id: string;
    status: string;
    message: string;
  }> {
    return this.request('/api/v1/memory/working/promote', {
      method: 'POST',
      body: JSON.stringify(payload),
      signal,
    });
  }

  promoteMemory(payload: {
    memory_id: string;
    expected_revision?: number;
    expected_digest_sha256?: string;
    assigned_authority?: string;
    review_rationale: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    mutation_type: string;
    memory_id: string;
    new_lifecycle: string;
    new_revision: number;
    audit_id: string;
    signature_id: string;
    mutated_at: string;
  }> {
    return this.request('/api/v1/memory/mutations/promote', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-promote-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  supersedeMemory(payload: {
    target_memory_id: string;
    successor_id: string;
    expected_revision?: number;
    reason: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    mutation_type: string;
    memory_id: string;
    new_lifecycle: string;
    new_revision: number;
    audit_id: string;
    signature_id: string;
    mutated_at: string;
  }> {
    return this.request('/api/v1/memory/mutations/supersede', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-sup-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  tombstoneMemory(payload: {
    target_memory_id: string;
    expected_revision?: number;
    reason: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    mutation_type: string;
    memory_id: string;
    new_lifecycle: string;
    new_revision: number;
    audit_id: string;
    signature_id: string;
    mutated_at: string;
  }> {
    return this.request('/api/v1/memory/mutations/tombstone', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-tomb-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  listMemorySnapshots(signal?: AbortSignal): Promise<{
    snapshots: Array<{
      snapshot_id: string;
      branch: string;
      manifest_digest_sha256: string;
      record_count: number;
      message: string;
      created_by: string;
      created_at: string;
    }>;
    active_head: string;
    total_count: number;
  }> {
    return this.request('/api/v1/memory/versioning/snapshots', { method: 'GET', signal });
  }

  createMemorySnapshot(payload: {
    branch?: string;
    message: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    snapshot_id: string;
    branch: string;
    manifest_digest_sha256: string;
    record_count: number;
    message: string;
    created_by: string;
    created_at: string;
  }> {
    return this.request('/api/v1/memory/versioning/snapshots', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-snap-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  getMemorySnapshotDiff(fromSnapshot: string, toSnapshot: string, signal?: AbortSignal): Promise<{
    from_snapshot: string;
    to_snapshot: string;
    entries: Array<{
      memory_id: string;
      change_type: string;
      old_title?: string;
      new_title?: string;
      details: string;
    }>;
    has_conflict: boolean;
  }> {
    return this.request(`/api/v1/memory/versioning/diff?from_snapshot=${encodeURIComponent(fromSnapshot)}&to_snapshot=${encodeURIComponent(toSnapshot)}`, { method: 'GET', signal });
  }

  rollbackMemorySnapshot(payload: {
    target_snapshot_id: string;
    reason: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    status: string;
    target_snapshot_id: string;
    new_head_digest: string;
    audit_id: string;
    rolled_back_at: string;
  }> {
    return this.request('/api/v1/memory/versioning/rollback', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-rollback-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  getMemoryUsageTrace(memoryId: string, signal?: AbortSignal): Promise<{
    memory_id: string;
    title: string;
    total_recalls: number;
    total_injections: number;
    total_citations: number;
    events: Array<{
      event_id: string;
      event_type: string;
      task_id: string;
      run_id: string;
      agent_id: string;
      revision_used: number;
      evidence_plan_id?: string;
      causal_link_status: string;
      timestamp: string;
    }>;
  }> {
    return this.request(`/api/v1/memory/${encodeURIComponent(memoryId)}/usage`, { method: 'GET', signal });
  }

  getMemorySecurityHealth(signal?: AbortSignal): Promise<{
    encryption_status: string;
    key_id: string;
    integrity_status: string;
    verified_records: number;
    tampered_records: number;
    rebuild_watermark: number;
    indexes: Array<{
      name: string;
      generation: number;
      status: string;
      outbox_lag_ms: number;
      records_indexed: number;
    }>;
    acl_matrix: Array<{
      scope: string;
      enforcement_mode: string;
      read_isolation: string;
      write_authority: string;
    }>;
    evaluated_at: string;
  }> {
    return this.request('/api/v1/memory/security/health', { method: 'GET', signal });
  }

  getDoctorReport(signal?: AbortSignal): Promise<{
    overall_status: string;
    checks: Array<{
      component: string;
      status: string;
      latency_ms: number;
      message: string;
    }>;
    evaluated_at: string;
  }> {
    return this.request('/api/v1/health/doctor', { method: 'GET', signal });
  }

  listBackups(signal?: AbortSignal): Promise<{
    backups: Array<{
      backup_id: string;
      schema_version: number;
      size_bytes: number;
      digest_sha256: string;
      status: string;
      created_at: string;
    }>;
    total_count: number;
  }> {
    return this.request('/api/v1/operations/backups', { method: 'GET', signal });
  }

  createBackup(payload: { label: string }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    backup_id: string;
    schema_version: number;
    size_bytes: number;
    digest_sha256: string;
    status: string;
    created_at: string;
  }> {
    return this.request('/api/v1/operations/backups/create', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-bkp-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  verifyBackup(backupId: string, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    backup_id: string;
    integrity_status: string;
    schema_version: number;
    digest_sha256: string;
    verified_at: string;
  }> {
    return this.request('/api/v1/operations/backups/verify', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-verify-${Date.now()}`,
        payload: { backup_id: backupId },
      }),
      signal,
    });
  }

  restoreBackup(payload: {
    backup_id: string;
    expected_digest_sha256?: string;
    safety_backup_label: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    status: string;
    restored_backup_id: string;
    safety_backup_id: string;
    audit_id: string;
    restored_at: string;
  }> {
    return this.request('/api/v1/operations/backups/restore', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-restore-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  listMaintenanceJobs(signal?: AbortSignal): Promise<{
    jobs: Array<{
      job_id: string;
      job_type: string;
      status: string;
      is_dry_run: boolean;
      target_scope: string;
      reclaimed_bytes: number;
      records_affected: number;
      audit_id: string;
      started_at: string;
      completed_at?: string;
    }>;
    total_count: number;
  }> {
    return this.request('/api/v1/operations/maintenance/jobs', { method: 'GET', signal });
  }

  createMaintenanceJob(payload: {
    job_type: string;
    is_dry_run: boolean;
    target_scope: string;
  }, idempotencyKey?: string, signal?: AbortSignal): Promise<{
    job_id: string;
    job_type: string;
    status: string;
    is_dry_run: boolean;
    target_scope: string;
    reclaimed_bytes: number;
    records_affected: number;
    audit_id: string;
    started_at: string;
    completed_at?: string;
  }> {
    return this.request('/api/v1/operations/maintenance/jobs', {
      method: 'POST',
      headers: idempotencyKey ? { 'Idempotency-Key': idempotencyKey } : undefined,
      body: JSON.stringify({
        idempotency_key: idempotencyKey ?? `idem-maint-${Date.now()}`,
        payload,
      }),
      signal,
    });
  }

  listBenchmarks(signal?: AbortSignal): Promise<{
    reports: Array<{
      suite_id: string;
      suite_name: string;
      harness_type: string;
      status: string;
      dataset_subset: string;
      commit_sha: string;
      metrics: Array<{
        name: string;
        value: number;
        unit: string;
        baseline: number;
        threshold: number;
      }>;
      scope_notice: string;
      evaluated_at: string;
    }>;
    total_suites: number;
    evaluated_at: string;
  }> {
    return this.request('/api/v1/benchmarks', { method: 'GET', signal });
  }

  getReleaseTrust(signal?: AbortSignal): Promise<{
    binary_commit_sha: string;
    source_repo: string;
    pack_manifest_status: string;
    pack_manifest_digest: string;
    sbom_status: string;
    sbom_format: string;
    signing_status: string;
    signer_identity: string;
    reproducibility_status: string;
    artifacts: Array<{
      name: string;
      digest_sha256: string;
      size_bytes: number;
      download_path: string;
    }>;
    evaluated_at: string;
  }> {
    return this.request('/api/v1/operations/trust', { method: 'GET', signal });
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
}

export const api = new APIClient();
