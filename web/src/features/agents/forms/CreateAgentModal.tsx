import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { ErrorState } from '../../../components/state';
import type { AgentDetailDTO } from '../../../api/types';

interface CreateAgentModalProps {
  isOpen: boolean;
  onClose: () => void;
  onCreated: (agent: AgentDetailDTO) => void;
}

const AVAILABLE_CAPABILITIES = [
  'code_edit',
  'dag_plan',
  'test_execute',
  'sandbox_run',
  'quorum_review',
  'visual_audit',
  'memory_query',
  'git_commit',
];

const ROLES = ['developer', 'architect', 'orchestrator', 'qa', 'appsec'];
const PROVIDERS = ['claude', 'codex', 'gemini', 'opencode', 'ollama-local'];

export function CreateAgentModal({ isOpen, onClose, onCreated }: CreateAgentModalProps) {
  const [name, setName] = useState('');
  const [id, setId] = useState('');
  const [role, setRole] = useState('developer');
  const [provider, setProvider] = useState('claude');
  const [model, setModel] = useState('claude-3-7-sonnet');
  const [capabilities, setCapabilities] = useState<string[]>(['code_edit']);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  if (!isOpen) return null;

  const toggleCapability = (cap: string) => {
    setCapabilities((prev) =>
      prev.includes(cap) ? prev.filter((c) => c !== cap) : [...prev, cap]
    );
  };

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) {
      setError('Agent name is required');
      return;
    }
    setSubmitting(true);
    setError(null);

    try {
      const created = await api.createAgent({
        id: id.trim() || undefined,
        name: name.trim(),
        role,
        provider,
        model,
        capabilities,
        status: 'registered',
      });
      onCreated(created);
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to register agent');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Register New Agent">
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '560px' }}>
        <div className="modal-header">
          <h3 className="modal-title">Register Autonomous Agent</h3>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
            {error && <ErrorState severity="error" message={error} />}

            <div className="form-group">
              <label className="form-label" htmlFor="agent-name">Agent Display Name *</label>
              <input
                id="agent-name"
                type="text"
                className="filter-input"
                placeholder="e.g. Codex Security Auditor"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="agent-id">Agent ID (Optional, auto-generated if empty)</label>
              <input
                id="agent-id"
                type="text"
                className="filter-input font-mono"
                placeholder="e.g. agent-security-auditor"
                value={id}
                onChange={(e) => setId(e.target.value)}
              />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
              <div className="form-group">
                <label className="form-label" htmlFor="agent-role">Role *</label>
                <select
                  id="agent-role"
                  className="filter-input"
                  value={role}
                  onChange={(e) => setRole(e.target.value)}
                >
                  {ROLES.map((r) => (
                    <option key={r} value={r}>
                      {r.toUpperCase()}
                    </option>
                  ))}
                </select>
              </div>

              <div className="form-group">
                <label className="form-label" htmlFor="agent-provider">Model Provider *</label>
                <select
                  id="agent-provider"
                  className="filter-input"
                  value={provider}
                  onChange={(e) => setProvider(e.target.value)}
                >
                  {PROVIDERS.map((p) => (
                    <option key={p} value={p}>
                      {p.toUpperCase()}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="agent-model">Model Name</label>
              <input
                id="agent-model"
                type="text"
                className="filter-input font-mono"
                placeholder="e.g. claude-3-7-sonnet"
                value={model}
                onChange={(e) => setModel(e.target.value)}
              />
            </div>

            <div className="form-group">
              <label className="form-label">Capabilities</label>
              <div style={{ display: 'flex', flexWrap: 'wrap', gap: '0.5rem', marginTop: '0.25rem' }}>
                {AVAILABLE_CAPABILITIES.map((cap) => (
                  <button
                    key={cap}
                    type="button"
                    className={`cap-pill ${capabilities.includes(cap) ? 'active' : ''}`}
                    onClick={() => toggleCapability(cap)}
                    style={{
                      cursor: 'pointer',
                      background: capabilities.includes(cap) ? 'var(--color-primary, #3b82f6)' : 'var(--color-surface, #1e293b)',
                      color: '#ffffff',
                      border: '1px solid var(--color-border, #334155)',
                    }}
                  >
                    {capabilities.includes(cap) ? `✓ ${cap}` : `+ ${cap}`}
                  </button>
                ))}
              </div>
            </div>
          </div>

          <div className="modal-actions" style={{ marginTop: '1.5rem' }}>
            <Button variant="secondary" size="sm" type="button" onClick={onClose} disabled={submitting}>
              Cancel
            </Button>
            <Button variant="primary" size="sm" type="submit" disabled={submitting}>
              {submitting ? 'Registering…' : 'Register Agent'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
