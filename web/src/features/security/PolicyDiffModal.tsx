import { Button } from '../../components/ui';
import type { PolicyDiffDTO } from '../../api/types';

interface PolicyDiffModalProps {
  diff: PolicyDiffDTO;
  isOpen: boolean;
  onClose: () => void;
  onApply: () => void;
  applying: boolean;
}

export function PolicyDiffModal({ diff, isOpen, onClose, onApply, applying }: PolicyDiffModalProps) {
  if (!isOpen) return null;

  return (
    <div className="modal-backdrop" onClick={onClose} role="dialog" aria-modal="true" aria-label="Policy Diff Inspector">
      <div className="modal-card" onClick={(e) => e.stopPropagation()} style={{ maxWidth: '720px' }}>
        <div className="modal-header">
          <div>
            <h3 className="modal-title">Governed Policy Diff</h3>
            <span className="text-xs text-dim">
              Comparing Active (v{diff.active_version}) → Proposed Draft (v{diff.draft_version})
            </span>
          </div>
          <button type="button" className="btn btn-ghost btn-sm" onClick={onClose} aria-label="Close">
            ✕
          </button>
        </div>

        <div className="modal-body" style={{ display: 'flex', flexDirection: 'column', gap: '1rem' }}>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: '0.75rem', padding: '0.75rem', background: 'var(--color-surface, #1e293b)', borderRadius: '6px', fontSize: '0.8125rem' }}>
            <div>
              <span className="meta-label">Active Digest:</span>
              <code className="font-mono text-xs" style={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis' }}>{diff.active_digest || 'none'}</code>
            </div>
            <div>
              <span className="meta-label">Draft Digest:</span>
              <code className="font-mono text-xs" style={{ display: 'block', overflow: 'hidden', textOverflow: 'ellipsis' }}>{diff.draft_digest || 'none'}</code>
            </div>
          </div>

          {!diff.has_changes ? (
            <div style={{ padding: '1.5rem', textAlign: 'center', color: 'var(--color-text-muted, #94a3b8)' }}>
              No structural or semantic differences detected between active policy and draft.
            </div>
          ) : (
            <div style={{ display: 'flex', flexDirection: 'column', gap: '0.75rem', maxHeight: '360px', overflowY: 'auto' }}>
              {diff.rule_diffs.map((rd) => {
                let badgeColor = '#94a3b8';
                let bgColor = 'rgba(148, 163, 184, 0.05)';
                let border = '1px solid rgba(148, 163, 184, 0.2)';

                if (rd.type === 'added') {
                  badgeColor = '#10b981';
                  bgColor = 'rgba(16, 185, 129, 0.08)';
                  border = '1px solid rgba(16, 185, 129, 0.3)';
                } else if (rd.type === 'removed') {
                  badgeColor = '#ef4444';
                  bgColor = 'rgba(239, 68, 68, 0.08)';
                  border = '1px solid rgba(239, 68, 68, 0.3)';
                } else if (rd.type === 'modified') {
                  badgeColor = '#f59e0b';
                  bgColor = 'rgba(245, 158, 11, 0.08)';
                  border = '1px solid rgba(245, 158, 11, 0.3)';
                }

                return (
                  <div key={rd.rule_id} style={{ background: bgColor, border, borderRadius: '6px', padding: '0.75rem' }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
                      <code className="font-mono" style={{ fontWeight: 600 }}>{rd.rule_id}</code>
                      <span style={{ fontSize: '0.6875rem', fontWeight: 700, padding: '0.125rem 0.375rem', borderRadius: '4px', background: badgeColor, color: '#000' }}>
                        {rd.type.toUpperCase()}
                      </span>
                    </div>

                    <p style={{ margin: '0.375rem 0 0', fontSize: '0.8125rem' }}>
                      {rd.new_description || rd.old_description}
                    </p>

                    {rd.changes && rd.changes.length > 0 && (
                      <ul style={{ margin: '0.375rem 0 0', paddingLeft: '1.25rem', fontSize: '0.75rem', color: badgeColor }}>
                        {rd.changes.map((c, i) => (
                          <li key={i}>{c}</li>
                        ))}
                      </ul>
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>

        <div className="modal-actions" style={{ marginTop: '1.5rem', display: 'flex', justifyContent: 'space-between' }}>
          <Button variant="secondary" size="sm" onClick={onClose} disabled={applying}>
            Close Diff
          </Button>
          <Button variant="primary" size="sm" onClick={onApply} disabled={applying || !diff.has_changes}>
            {applying ? 'Applying Policy…' : 'Approve & Apply Policy'}
          </Button>
        </div>
      </div>
    </div>
  );
}
