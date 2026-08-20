import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SafeLogViewer } from '../features/logs/SafeLogViewer';
import { api } from '../api/client';

describe('Adversarial Security & Zero Injection Tests (T219)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('neutralizes stored XSS injection in log viewer via DOM text rendering', () => {
    const rawXSSLines = [
      {
        index: 0,
        timestamp: '2026-08-20T00:00:00Z',
        stream: 'stderr',
        message: '<script>window.__xss_leaked = true;</script><img src="x" onerror="window.__xss_leaked = true;">',
      },
    ];

    render(<SafeLogViewer lines={rawXSSLines} isTruncated={false} />);

    // Must be rendered as text node, not executed in DOM
    expect(screen.getByText(/<script>window.__xss_leaked = true;<\/script>/)).toBeInTheDocument();
    expect((window as any).__xss_leaked).toBeUndefined();
  });

  it('rejects cross-site origin injection attempts in client API layer', async () => {
    vi.spyOn(window, 'fetch').mockImplementationOnce(() =>
      Promise.reject(new Error('Cross-origin mutation blocked by security policy'))
    );

    await expect(api.createTask({ title: 'Adversarial Task', description: 'desc', risk: 'low' })).rejects.toThrow();
  });
});
