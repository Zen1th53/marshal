import type { ReactNode } from 'react';
import { useCapabilities } from './capabilities';
import { StatusBadge } from '../components/ui';

interface RequireCapabilityProps {
  name: string;
  children: ReactNode;
  fallback?: ReactNode;
  showReason?: boolean;
}

export function RequireCapability({
  name,
  children,
  fallback,
  showReason = true,
}: RequireCapabilityProps) {
  const { getCapabilityState, getCapabilityReason } = useCapabilities();
  const state = getCapabilityState(name);
  const reason = getCapabilityReason(name);

  if (state === 'AVAILABLE') {
    return <>{children}</>;
  }

  if (fallback !== undefined) {
    return <>{fallback}</>;
  }

  return (
    <div className="capability-unavailable-banner" role="alert">
      <StatusBadge
        status={state === 'DEGRADED' ? 'degraded' : 'unavailable'}
        label={state}
      />
      <span className="capability-name">{name}</span>
      {showReason && reason && (
        <span className="capability-reason">{reason}</span>
      )}
    </div>
  );
}
