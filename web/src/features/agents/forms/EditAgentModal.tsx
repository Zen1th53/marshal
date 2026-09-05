import { useState } from 'react';
import { api } from '../../../api/client';
import { Button } from '../../../components/ui';
import { ErrorState } from '../../../components/state';
import type { AgentDetailDTO } from '../../../api/types';

interface EditAgentModalProps {
  agent: AgentDetailDTO;
  isOpen: boolean;
  onClose: () => void;
  onUpdated: (updated: AgentDetailDTO) => void;
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

const PROVIDERS = ['claude', 'codex', 'gemini', 'opencode', 'ollama-local'];
const STATUSES = ['registered', 'active', 'disabled'];

export function EditAgentModal({ agent, isOpen, onClose, onUpdated }: EditAgentModalProps) {
  const [name, setName] = useState(agent.name);
  const [provider, setProvider] = useState(agent.provider);
  const [model, setModel] = useState(agent.model);
  const [status, setStatus] = useState(agent.status);
  const [capabilities, setCapabilities] = useState<string[]>(agent.capabilities ?? []);
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
      const updated = await api.updateAgent(
        agent.id,
        {
          name: name.trim(),
          provider,
          model,
          status,
          capabilities,
        },
        agent.revision
      );
      onUpdated(updated);
      onClose();
    } catch (err: unknown) {
      setError(err instanceof Error ? err.message : 'Failed to update agent');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Edit Agent">
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '560px' }}>
        <div className="modal-header">
          <div>
            <h3 className="modal-title">Edit Agent Configuration</h3>
            <code className="agent-id-badge">{agent.id}</code>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <form onSubmit={handleSubmit}>
          <div className="modal-body" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
            {error && <ErrorState severity="error" message={error} />}

            <div className="form-group">
              <label className="form-label" htmlFor="edit-agent-name">Display Name *</label>
              <input
                id="edit-agent-name"
                type="text"
                className="filter-input"
                value={name}
                onChange={(e) => setName(e.target.value)}
                required
              />
            </div>

            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '1rem' }}>
              <div className="form-group">
                <label className="form-label" htmlFor="edit-agent-provider">Provider</label>
                <select
                  id="edit-agent-provider"
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

              <div className="form-group">
                <label className="form-label" htmlFor="edit-agent-status">Status</label>
                <select
                  id="edit-agent-status"
                  className="filter-input"
                  value={status}
                  onChange={(e) => setStatus(e.target.value)}
                >
                  {STATUSES.map((s) => (
                    <option key={s} value={s}>
                      {s.toUpperCase()}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="form-group">
              <label className="form-label" htmlFor="edit-agent-model">Model Name</label>
              <input
                id="edit-agent-model"
                type="text"
                className="filter-input font-mono"
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
              {submitting ? 'Saving…' : 'Save Changes'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
