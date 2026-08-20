import { useState, useEffect, useCallback } from 'react';
import { api } from '../api/client';
import { StatusBadge, Button } from '../components/ui';
import { LoadingState, ErrorState } from '../components/state';

interface GateRule {
  id: string;
  name: string;
  enforcement: string;
  status: string;
  description: string;
  last_evaluated_at: string;
}

interface CapabilityRule {
  capability_name: string;
  required_role: string;
  decision: string;
  denial_reason?: string;
}

interface SecurityPolicyData {
  policy_id: string;
  revision: number;
  global_risk_level: string;
  degraded_controls: string[];
  gate_rules: GateRule[];
  capability_rules: CapabilityRule[];
  last_audited_at: string;
}

export function Security() {
  const [data, setData] = useState<SecurityPolicyData | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const fetchPolicy = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const resp = await api.getSecurityPolicy();
      setData(resp);
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to inspect security policy & gate matrix');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void fetchPolicy();
  }, [fetchPolicy]);

  if (loading) return <LoadingState message="Auditing active security policy & boundary enforcement…" />;
  if (error) return <ErrorState severity="error" message={error} onRetry={fetchPolicy} />;
  if (!data) return null;

  return (
    <div className="security-container">
      <div className="security-header">
        <div className="security-headline">
          <h2 className="security-title">Security Policy & Gate Boundary Inspector</h2>
          <span className="security-subtitle">
            Read-only verified policy enforcement • Hardened execution sandbox
          </span>
        </div>
        <Button variant="secondary" size="sm" onClick={fetchPolicy}>
          Audit Policy
        </Button>
      </div>

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
    </div>
  );
}
