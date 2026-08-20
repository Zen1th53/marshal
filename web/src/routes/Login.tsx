import { useState, useEffect } from 'react';
import { useAuth } from '../auth/AuthContext';
import { Button } from '../components/ui';
import { APIError } from '../api/errors';

interface LoginProps {
  onSuccess?: () => void;
}

export function Login({ onSuccess }: LoginProps) {
  const { login, isAuthenticated } = useAuth();
  const [code, setCode] = useState('');
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  useEffect(() => {
    // Auto-fill from URL query param if present
    const params = new URLSearchParams(window.location.search);
    const codeParam = params.get('code');
    if (codeParam) {
      setCode(codeParam);
    }
  }, []);

  useEffect(() => {
    if (isAuthenticated && onSuccess) {
      onSuccess();
    }
  }, [isAuthenticated, onSuccess]);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!code.trim()) {
      setError('Please enter a one-time login code.');
      return;
    }

    setError(null);
    setIsSubmitting(true);
    try {
      await login(code.trim());
      if (onSuccess) {
        onSuccess();
      }
    } catch (err: unknown) {
      if (err instanceof APIError) {
        setError(err.message);
      } else {
        setError('Login failed. Please verify your code and try again.');
      }
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="login-container">
      <div className="login-card">
        <div className="login-header">
          <span className="login-logo" aria-hidden="true">◇</span>
          <h2 className="login-title">MARSHAL Web Control Plane</h2>
          <p className="login-subtitle">Authenticate with an operator one-time code generated via CLI</p>
        </div>

        {error && (
          <div className="login-error-banner" role="alert">
            <span className="error-icon" aria-hidden="true">✕</span>
            <span className="error-text">{error}</span>
          </div>
        )}

        <form onSubmit={handleSubmit} className="login-form">
          <div className="form-group">
            <label htmlFor="otc-input" className="form-label">
              One-Time Login Code
            </label>
            <input
              id="otc-input"
              type="text"
              className="form-input"
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="e.g. 9a8b7c6d5e4f3a2b1c0d"
              autoComplete="off"
              spellCheck="false"
              required
            />
            <span className="form-hint">
              Generate a new code via: <code>marshal web code</code>
            </span>
          </div>

          <div className="form-actions">
            <Button
              type="submit"
              variant="primary"
              size="lg"
              disabled={isSubmitting || !code.trim()}
            >
              {isSubmitting ? 'Authenticating…' : 'Authenticate Operator'}
            </Button>
          </div>
        </form>
      </div>
    </div>
  );
}
