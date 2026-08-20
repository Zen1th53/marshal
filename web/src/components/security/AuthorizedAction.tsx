import { useState, type ReactNode } from 'react';
import { useAuth } from '../../auth/AuthContext';
import { hasAuthority } from '../../auth/permissions';
import { Button } from '../ui';

interface AuthorizedActionProps {
  authority: string;
  onAction: () => void | Promise<void>;
  children: ReactNode;
  variant?: 'primary' | 'secondary' | 'danger' | 'ghost';
  size?: 'sm' | 'md' | 'lg';
  isDestructive?: boolean;
  confirmTitle?: string;
  confirmMessage?: string;
  disabled?: boolean;
  className?: string;
}

export function AuthorizedAction({
  authority,
  onAction,
  children,
  variant = 'secondary',
  size = 'md',
  isDestructive = false,
  confirmTitle = 'Confirm Privileged Action',
  confirmMessage = 'Are you sure you want to proceed with this operation?',
  disabled = false,
  className = '',
}: AuthorizedActionProps) {
  const { user } = useAuth();
  const [showConfirm, setShowConfirm] = useState(false);
  const [isExecuting, setIsExecuting] = useState(false);

  const authorized = hasAuthority(user, authority);

  const handleClick = () => {
    if (!authorized || disabled || isExecuting) return;
    if (isDestructive) {
      setShowConfirm(true);
    } else {
      void executeAction();
    }
  };

  const executeAction = async () => {
    setIsExecuting(true);
    try {
      await onAction();
    } finally {
      setIsExecuting(false);
      setShowConfirm(false);
    }
  };

  if (!authorized) {
    return (
      <Button
        variant="ghost"
        size={size}
        disabled
        className={`btn-unauthorized ${className}`.trim()}
        title={`Requires authority: ${authority}`}
        aria-label={`Action disabled: requires ${authority} authority`}
      >
        {children}
      </Button>
    );
  }

  return (
    <>
      <Button
        variant={variant}
        size={size}
        disabled={disabled || isExecuting}
        onClick={handleClick}
        className={className}
      >
        {children}
      </Button>

      {showConfirm && (
        <div className="modal-backdrop" role="dialog" aria-modal="true" aria-labelledby="confirm-modal-title">
          <div className="modal-card">
            <h3 id="confirm-modal-title" className="modal-title">
              {confirmTitle}
            </h3>
            <p className="modal-body">{confirmMessage}</p>
            <div className="modal-actions">
              <Button
                variant="secondary"
                size="sm"
                onClick={() => setShowConfirm(false)}
                disabled={isExecuting}
              >
                Cancel
              </Button>
              <Button
                variant={isDestructive ? 'danger' : 'primary'}
                size="sm"
                onClick={executeAction}
                disabled={isExecuting}
              >
                {isExecuting ? 'Executing…' : 'Confirm Action'}
              </Button>
            </div>
          </div>
        </div>
      )}
    </>
  );
}
