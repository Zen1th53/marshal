type StatusKind = 'ready' | 'degraded' | 'unavailable' | 'not_run' | 'pending' | 'running' | 'success' | 'error';

interface StatusBadgeProps {
  status: StatusKind;
  label?: string;
}

const STATUS_CONFIG: Record<StatusKind, { icon: string; className: string; defaultLabel: string }> = {
  ready:       { icon: '●', className: 'status-ready',       defaultLabel: 'Ready' },
  success:     { icon: '✓', className: 'status-success',     defaultLabel: 'Success' },
  running:     { icon: '◌', className: 'status-running',     defaultLabel: 'Running' },
  pending:     { icon: '○', className: 'status-pending',     defaultLabel: 'Pending' },
  degraded:    { icon: '▲', className: 'status-degraded',    defaultLabel: 'Degraded' },
  error:       { icon: '✕', className: 'status-error',       defaultLabel: 'Error' },
  unavailable: { icon: '◆', className: 'status-unavailable', defaultLabel: 'Unavailable' },
  not_run:     { icon: '—', className: 'status-notrun',      defaultLabel: 'Not Run' },
};

export function StatusBadge({ status, label }: StatusBadgeProps) {
  const config = STATUS_CONFIG[status] ?? STATUS_CONFIG.not_run;
  return (
    <span className={`status-badge ${config.className}`} role="status" aria-label={label ?? config.defaultLabel}>
      <span className="status-icon" aria-hidden="true">{config.icon}</span>
      <span className="status-label">{label ?? config.defaultLabel}</span>
    </span>
  );
}

export type { StatusKind };
