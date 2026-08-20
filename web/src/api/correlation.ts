export function formatCorrelationId(id: string): string {
  if (!id) return '';
  if (id.length <= 16) return id;
  return `${id.slice(0, 8)}…${id.slice(-6)}`;
}

export function buildAuditTraceUrl(correlationId: string): string {
  return `/audit?correlation_id=${encodeURIComponent(correlationId)}`;
}
