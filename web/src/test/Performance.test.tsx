import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { SafeLogViewer, type LogLine } from '../features/logs/SafeLogViewer';

describe('Large-State Performance & Resource Bounds (T217)', () => {
  it('renders large log stream (2,000 lines) within bounded rendering time', () => {
    const lines: LogLine[] = [];
    for (let i = 0; i < 2000; i++) {
      lines.push({
        index: i,
        timestamp: '2026-08-20T00:00:00Z',
        stream: 'stdout',
        message: `Step ${i}: Processing AST compilation segment and verifying memory consistency`,
      });
    }

    const start = performance.now();
    render(<SafeLogViewer lines={lines} isTruncated={false} />);
    const elapsed = performance.now() - start;

    expect(screen.getByText(/Step 0:/)).toBeInTheDocument();
    expect(screen.getByText(/Step 1999:/)).toBeInTheDocument();
    expect(elapsed).toBeLessThan(2000); // Must render comfortably in jsdom
  });
});
