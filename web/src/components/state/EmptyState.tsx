import type { ReactNode } from 'react';

interface EmptyStateProps {
  title: string;
  description?: string;
  action?: ReactNode;
}

export function EmptyState({ title, description, action }: EmptyStateProps) {
  return (
    <div className="state-view state-empty" role="region" aria-label={title}>
      <div className="empty-icon" aria-hidden="true">∅</div>
      <h3 className="state-title">{title}</h3>
      {description && <p className="state-description">{description}</p>}
      {action && <div className="state-action">{action}</div>}
    </div>
  );
}
