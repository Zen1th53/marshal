export type SystemHealthState = 'READY' | 'DEGRADED' | 'UNAVAILABLE' | 'NOT_RUN';

export type TaskStatus = 'pending' | 'ready' | 'running' | 'completed' | 'failed' | 'canceled';

export type CapabilityState =
  | 'AVAILABLE'
  | 'DISABLED_BY_POLICY'
  | 'DEGRADED'
  | 'UNAVAILABLE'
  | 'NOT_RUN';

export interface CapabilityStatusDTO {
  state: CapabilityState;
  reason?: string;
  last_checked: string;
}

export interface CapabilitiesDTO {
  capabilities: Record<string, CapabilityStatusDTO>;
  evaluated_at: string;
}

export interface PagedResponse<T> {
  items: T[];
  next_cursor?: string;
  total: number;
  limit: number;
}

export interface MutationEnvelope<T> {
  expected_revision?: number;
  idempotency_key?: string;
  payload: T;
}

export interface SystemStatusDTO {
  state: SystemHealthState;
  version: string;
  commit_sha: string;
  database_schema: string;
  active_workers: number;
  pending_tasks: number;
  updated_at: string;
}

export interface AdapterSummaryDTO {
  name: string;
  binary_name: string;
  installed: boolean;
  state: SystemHealthState;
  version?: string;
  probed_at: string;
}

export interface AgentSummaryDTO {
  id: string;
  name: string;
  role: string;
  status: string;
  created_at: string;
}

export interface TaskSummaryDTO {
  id: string;
  title: string;
  status: TaskStatus;
  risk: string;
  assigned_to?: string;
  base_commit: string;
  head_commit: string;
  created_at: string;
  updated_at: string;
}

export interface MemoryRecordDTO {
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
}

export interface AuditEventDTO {
  id: string;
  timestamp: string;
  actor: string;
  action: string;
  target_id: string;
  outcome: string;
}
