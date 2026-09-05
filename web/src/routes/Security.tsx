import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';
import { useToast } from '../components/toast';
import { PolicyDiffModal } from '../features/security/PolicyDiffModal';
import type { SecurityPolicyInspectorResponseDTO, PolicyDiffDTO, PolicyValidationResultDTO } from '../api/types';

export function Security() {
  const [data, setData] = useState<SecurityPolicyInspectorResponseDTO | null>(null);
  const [activeTab, setActiveTab] = useState<'inspector' | 'editor'>('inspector');
  const [draftYAML, setDraftYAML] = useState<string>('');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [validating, setValidating] = useState(false);
  const [validationResult, setValidationResult] = useState<PolicyValidationResultDTO | null>(null);
  const [diffData, setDiffData] = useState<PolicyDiffDTO | null>(null);
  const [diffModalOpen, setDiffModalOpen] = useState(false);
  const [applying, setApplying] = useState(false);
  const { addToast } = useToast();

  const fetchPolicy = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getSecurityPolicy();
      setData(resp);
      if (resp.active_draft) {
        setDraftYAML(resp.active_draft.yaml_content);
      }
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to inspect security policy & gate matrix');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchPolicy();
  }, [fetchPolicy]);

  const handleSaveDraft = async () => {
    try {
      await api.savePolicyDraft({ yaml_content: draftYAML });
      addToast({
        type: 'success',
        message: 'Policy draft saved successfully.',
      });
      await fetchPolicy();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to save policy draft',
      });
    }
  };

  const handleValidate = async () => {
    setValidating(true);
    try {
      const res = await api.validatePolicy(draftYAML);
      setValidationResult(res);
      if (res.valid) {
        addToast({
          type: 'success',
          message: `Policy syntax & schema verified (${res.rules_count} rules).`,
        });
      } else {
        addToast({
          type: 'error',
          message: 'Policy validation failed. Inspect diagnostics.',
        });
      }
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to execute policy validation',
      });
    } finally {
      setValidating(false);
    }
  };

  const handleOpenDiff = async () => {
    try {
      const diff = await api.getPolicyDiff(draftYAML);
      setDiffData(diff);
      setDiffModalOpen(true);
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to compute policy diff',
      });
    }
  };

  const handleApply = async () => {
    setApplying(true);
    try {
      const res = await api.applyPolicy({
        expected_revision: data?.revision,
      });
      addToast({
        type: 'success',
        message: `Policy v${res.version} successfully activated! Revision r${res.revision}.`,
      });
      setDiffModalOpen(false);
      await fetchPolicy();
      setActiveTab('inspector');
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to apply policy',
      });
    } finally {
      setApplying(false);
    }
  };

  const handleRollback = async () => {
    try {
      const res = await api.rollbackPolicy();
      addToast({
        type: 'success',
        message: `Policy successfully rolled back to v${res.version} (r${res.revision})!`,
      });
      await fetchPolicy();
    } catch (err: unknown) {
      addToast({
        type: 'error',
        message: err instanceof Error ? err.message : 'Failed to rollback policy',
      });
    }
  };

  if (loading && !data) return <LoadingState message="Auditing active security policy & boundary enforcement…" />;
  if (error && !data) return <ErrorState severity="error" message={error} onRetry={fetchPolicy} />;
  if (!data) return null;

  return (
    <div className="security-container">
      <div className="security-header">
        <div className="security-headline">
          <h2 className="security-title">Security Policy & Governed Control Plane</h2>
          <span className="security-subtitle">
            Formal verification • Atomic policy activation • Strict fail-closed boundary
          </span>
        </div>
        <div style={{ display: 'flex', gap: '0.5rem' }}>
          {(data.history_count ?? 0) > 1 && (
            <Button variant="danger" size="sm" onClick={handleRollback}>
              Rollback Policy
            </Button>
          )}
          <Button variant="secondary" size="sm" onClick={fetchPolicy}>
            Audit Policy
          </Button>
        </div>
      </div>

      {/* Mode Navigation Tabs */}
      <div className="filter-tabs" style={{ marginBottom: '1rem' }} role="tablist">
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'inspector'}
          className={`filter-tab ${activeTab === 'inspector' ? 'active' : ''}`}
          onClick={() => setActiveTab('inspector')}
        >
          Active Policy & Gates
        </button>
        <button
          type="button"
          role="tab"
          aria-selected={activeTab === 'editor'}
          className={`filter-tab ${activeTab === 'editor' ? 'active' : ''}`}
          onClick={() => setActiveTab('editor')}
        >
          Governed Policy Editor {data.active_draft ? '• Draft' : ''}
        </button>
      </div>

      {activeTab === 'inspector' ? (
        <>
          {/* Policy Overview Card */}
          <div className="policy-overview-card">
            <div className="task-meta-grid">
              <div className="meta-box">
                <span className="meta-label">Active Policy ID</span>
                <code className="meta-value font-mono">{data.policy_id}</code>
              </div>
              <div className="meta-box">
                <span className="meta-label">Revision</span>
                <span className="meta-value font-mono">r{data.revision}</span>
              </div>
              <div className="meta-box">
                <span className="meta-label">Global Risk Level</span>
                <span className={`risk-badge risk-${data.global_risk_level.toLowerCase()}`}>
                  {data.global_risk_level}
                </span>
              </div>
              <div className="meta-box">
                <span className="meta-label">Degraded Controls</span>
                <span className="meta-value font-mono">
                  {data.degraded_controls.length === 0 ? '0 (ALL SECURE)' : `${data.degraded_controls.length} DEGRADED`}
                </span>
              </div>
            </div>
          </div>

          {/* Mandatory Gate Rules */}
          <div className="security-section">
            <h3 className="section-title">Automated Security Gates ({data.gate_rules.length})</h3>
            <div className="table-responsive">
              <table className="data-table" aria-label="Security Gates Table">
                <thead>
                  <tr>
                    <th>Gate ID</th>
                    <th>Security Gate Name</th>
                    <th>Enforcement</th>
                    <th>Status</th>
                    <th>Description</th>
                    <th>Last Evaluation</th>
                  </tr>
                </thead>
                <tbody>
                  {data.gate_rules.map((g) => (
                    <tr key={g.id}>
                      <td>
                        <code className="gate-id-code font-mono text-xs">{g.id}</code>
                      </td>
                      <td className="font-semibold text-xs">{g.name}</td>
                      <td>
                        <span className="enforcement-badge">{g.enforcement.toUpperCase()}</span>
                      </td>
                      <td>
                        <StatusBadge
                          status={g.status === 'enforced' ? 'ready' : 'degraded'}
                          label={g.status.toUpperCase()}
                        />
                      </td>
                      <td className="description-cell text-xs">{g.description}</td>
                      <td className="text-xs font-mono text-dim">
                        {new Date(g.last_evaluated_at).toLocaleTimeString()}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>

          {/* Capability Authority Decision Matrix */}
          <div className="security-section">
            <h3 className="section-title">Capability Authorization Decisions</h3>
            <div className="table-responsive">
              <table className="data-table" aria-label="Capability Decisions Table">
                <thead>
                  <tr>
                    <th>Capability Target</th>
                    <th>Required Role</th>
                    <th>Evaluation Decision</th>
                    <th>Denial Rationale</th>
                  </tr>
                </thead>
                <tbody>
                  {data.capability_rules.map((c) => (
                    <tr key={c.capability_name}>
                      <td>
                        <code className="cap-name-badge font-mono text-xs">{c.capability_name}</code>
                      </td>
                      <td>
                        <span className="font-mono text-xs text-dim">{c.required_role}</span>
                      </td>
                      <td>
                        <span className={`decision-pill pill-${c.decision.toLowerCase()}`}>
                          {c.decision}
                        </span>
                      </td>
                      <td className="denial-cell text-xs">
                        {c.denial_reason ? <code>{c.denial_reason}</code> : <span className="text-dim">—</span>}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
        </>
      ) : (
        /* Governed Policy Editor View */
        <div className="policy-editor-container" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', padding: '0.75rem 1rem', background: 'var(--color-surface, #1e293b)', borderRadius: '6px' }}>
            <div style={{ fontSize: '0.8125rem' }}>
              <span>Editing Policy Draft for: <strong>{data.policy_id}</strong> (Target v{(data.active_draft?.version ?? 2)})</span>
            </div>
            <div style={{ display: 'flex', gap: '0.5rem' }}>
              <Button variant="secondary" size="sm" onClick={handleSaveDraft}>
                Save Draft
              </Button>
              <Button variant="secondary" size="sm" onClick={handleValidate} disabled={validating}>
                {validating ? 'Validating…' : 'Validate Syntax'}
              </Button>
              <Button variant="primary" size="sm" onClick={handleOpenDiff}>
                Inspect Diff & Apply…
              </Button>
            </div>
          </div>

          {validationResult && (
            <div
              style={{
                padding: '0.75rem 1rem',
                borderRadius: '6px',
                background: validationResult.valid ? 'rgba(16, 185, 129, 0.1)' : 'rgba(239, 68, 68, 0.1)',
                border: `1px solid ${validationResult.valid ? 'rgba(16, 185, 129, 0.3)' : 'rgba(239, 68, 68, 0.3)'}`,
                fontSize: '0.8125rem',
              }}
            >
              <strong>{validationResult.valid ? '✓ Policy Validated' : '✕ Validation Errors Detected'}:</strong>
              {validationResult.valid ? (
                <span style={{ marginLeft: '0.5rem', color: '#10b981' }}>
                  {validationResult.rules_count} rules parsed correctly. Digest: <code>{validationResult.digest?.slice(0, 16)}…</code>
                </span>
              ) : (
                <ul style={{ margin: '0.5rem 0 0', paddingLeft: '1.25rem', color: '#ef4444' }}>
                  {validationResult.errors?.map((err, idx) => (
                    <li key={idx}>{err}</li>
                  ))}
                </ul>
              )}
            </div>
          )}

          <div className="editor-textarea-wrapper">
            <textarea
              className="font-mono text-xs"
              style={{
                width: '100%',
                minHeight: '360px',
                padding: '1rem',
                background: '#0f172a',
                color: '#f8fafc',
                border: '1px solid var(--color-border, #334155)',
                borderRadius: '6px',
                lineHeight: 1.5,
                resize: 'vertical',
              }}
              value={draftYAML}
              onChange={(e) => setDraftYAML(e.target.value)}
              placeholder="Enter Policy Document in YAML / JSON format…"
              spellCheck={false}
            />
          </div>
        </div>
      )}

      {diffModalOpen && diffData && (
        <PolicyDiffModal
          diff={diffData}
          isOpen={diffModalOpen}
          onClose={() => setDiffModalOpen(false)}
          onApply={handleApply}
          applying={applying}
        />
      )}
    </div>
  );
}
