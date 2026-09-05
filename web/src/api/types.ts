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
  role?: string;
  provider?: string;
  model?: string;
  status: string;
  revision?: number;
  capabilities?: string[];
  current_task_id?: string;
  completed_task_count?: number;
  last_heartbeat?: string;
  created_at: string;
}

export interface AgentDetailDTO {
  id: string;
  name: string;
  role: string;
  provider: string;
  model: string;
  status: string;
  revision: number;
  capabilities: string[];
  current_task_id?: string;
  current_run_id?: string;
  completed_task_count: number;
  failed_task_count: number;
  last_heartbeat: string;
  created_at: string;
  memory_contributions?: {
    episodes_extracted: number;
    decisions_logged: number;
    facts_asserted: number;
  };
}

export interface CreateAgentPayload {
  id?: string;
  name: string;
  role: string;
  provider?: string;
  model?: string;
  capabilities?: string[];
  status?: string;
}

export interface UpdateAgentPayload {
  name?: string;
  provider?: string;
  model?: string;
  capabilities?: string[];
  status?: string;
}

export interface ActiveModelDTO {
  id: string;
  context_window: number;
  latency_p95_ms: number;
}

export interface SecretRefMetadataDTO {
  configured: boolean;
  ref_name: string;
  provider: string;
  version: string;
}

export interface ProviderDTO {
  id: string;
  name: string;
  class: 'cloud' | 'local';
  enabled: boolean;
  endpoint_url?: string;
  probe_status: 'healthy' | 'degraded' | 'unavailable' | 'not_run';
  capabilities: string[];
  models: ActiveModelDTO[];
  last_probed_at: string;
  secret_ref: SecretRefMetadataDTO;
}

export interface RouterDecisionDTO {
  intent: string;
  selected_model: string;
  provider_id: string;
  rationale: string;
  is_pinned: boolean;
}

export interface ProviderInventoryResponseDTO {
  providers: ProviderDTO[];
  routing_decisions: RouterDecisionDTO[];
  last_evaluated_at: string;
}

export interface UpdateProviderPayload {
  enabled?: boolean;
  endpoint_url?: string;
  models?: string[];
}

export interface SetProviderSecretPayload {
  secret_key?: string;
  env_var?: string;
  version?: string;
}

export interface PolicyDraftDTO {
  policy_id: string;
  version: number;
  yaml_content: string;
  rules_count: number;
  digest: string;
  status: 'draft' | 'validated';
  updated_at: string;
}

export interface PolicyRuleDiffDTO {
  type: 'added' | 'removed' | 'modified' | 'unchanged';
  rule_id: string;
  old_description?: string;
  new_description?: string;
  old_effect?: string;
  new_effect?: string;
  changes?: string[];
}

export interface PolicyDiffDTO {
  active_policy_id: string;
  active_version: number;
  active_digest: string;
  draft_version: number;
  draft_digest: string;
  has_changes: boolean;
  rule_diffs: PolicyRuleDiffDTO[];
}

export interface PolicyValidationResultDTO {
  valid: boolean;
  digest?: string;
  rules_count: number;
  errors?: string[];
  warnings?: string[];
}

export interface GateRuleDTO {
  id: string;
  name: string;
  enforcement: string;
  status: string;
  description: string;
  last_evaluated_at: string;
}

export interface CapabilityPolicyRuleDTO {
  capability_name: string;
  required_role: string;
  decision: string;
  denial_reason?: string;
}

export interface SecurityPolicyInspectorResponseDTO {
  policy_id: string;
  revision: number;
  digest?: string;
  global_risk_level: string;
  degraded_controls: string[];
  gate_rules: GateRuleDTO[];
  capability_rules: CapabilityPolicyRuleDTO[];
  active_draft?: PolicyDraftDTO;
  history_count?: number;
  last_audited_at: string;
}

export type RiskLevel = 'R0' | 'R1' | 'R2' | 'R3' | 'LOW' | 'MEDIUM' | 'HIGH' | 'CRITICAL';

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
