import { useState } from 'react';
import { formatCorrelationId, buildAuditTraceUrl } from '../../api/correlation';

interface CorrelationLinkProps {
  correlationId: string;
  showCopy?: boolean;
}

export function CorrelationLink({ correlationId, showCopy = true }: CorrelationLinkProps) {
  const [copied, setCopied] = useState(false);

  if (!correlationId) return null;

  const handleCopy = async (e: React.MouseEvent) => {
    e.preventDefault();
    e.stopPropagation();
    try {
      await navigator.clipboard.writeText(correlationId);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Fallback
    }
  };

  return (
    <span className="correlation-link-wrapper" title={`Trace ID: ${correlationId}`}>
      <a
        href={buildAuditTraceUrl(correlationId)}
        className="correlation-link"
        aria-label={`Audit trace for request ${correlationId}`}
      >
        <code>{formatCorrelationId(correlationId)}</code>
      </a>
      {showCopy && (
        <button
          type="button"
          className="correlation-copy-btn"
          onClick={handleCopy}
          aria-label={copied ? 'Copied' : 'Copy correlation ID'}
          title="Copy Trace ID"
        >
          {copied ? '✓' : '⧉'}
        </button>
      )}
    </span>
  );
}
