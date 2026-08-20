export interface APIErrorDetail {
  code: string;
  message: string;
  correlation_id?: string;
}

export interface APIErrorEnvelope {
  error: APIErrorDetail;
}

export class APIError extends Error {
  public readonly code: string;
  public readonly correlationId?: string;
  public readonly status: number;

  constructor(status: number, code: string, message: string, correlationId?: string) {
    // Redact any potential sensitive stack trace or credentials in message
    const sanitized = sanitizeErrorMessage(message);
    super(sanitized);
    this.name = 'APIError';
    this.status = status;
    this.code = code;
    this.correlationId = correlationId;
  }
}

function sanitizeErrorMessage(msg: string): string {
  if (!msg) return 'An unknown error occurred';
  // Strip tokens, file paths, private keys if leaked
  return msg
    .replace(/(bearer\s+)[A-Za-z0-9_\-\.]+/gi, '$1[REDACTED]')
    .replace(/(password|secret|key)=([^&\s]+)/gi, '$1=[REDACTED]');
}
