import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { SafeLogViewer, type LogLine } from './SafeLogViewer';

const mockLogLines: LogLine[] = [
  {
    index: 1,
    timestamp: '2026-08-20T00:00:00Z',
    stream: 'system',
    message: 'Starting task sandbox cell',
  },
  {
    index: 2,
    timestamp: '2026-08-20T00:00:01Z',
    stream: 'stdout',
    message: 'Loaded 4 memory blocks into context',
  },
  {
    index: 3,
    timestamp: '2026-08-20T00:00:02Z',
    stream: 'stderr',
    message: 'Warning: ephemeral cache miss',
  },
  {
    index: 4,
    timestamp: '2026-08-20T00:00:03Z',
    stream: 'stdout',
    // Potential malicious XSS injection in raw logs: must be rendered inert
    message: '<script>alert("xss")</script><img src="x" onerror="alert(1)" />',
  },
];

describe('SafeLogViewer (T189)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
  });

  it('renders log lines and filters safely by search query', async () => {
    render(<SafeLogViewer lines={mockLogLines} />);

    expect(screen.getByText('Starting task sandbox cell')).toBeInTheDocument();
    expect(screen.getByText('Loaded 4 memory blocks into context')).toBeInTheDocument();
    expect(screen.getByText('Warning: ephemeral cache miss')).toBeInTheDocument();

    // Verify XSS script is treated strictly as plain text (no executable DOM elements injected)
    expect(screen.getByText('<script>alert("xss")</script><img src="x" onerror="alert(1)" />')).toBeInTheDocument();

    // Filter logs
    const searchInput = screen.getByLabelText(/Search logs/i);
    await userEvent.type(searchInput, 'sandbox');

    expect(screen.getByText('Starting task sandbox cell')).toBeInTheDocument();
    expect(screen.queryByText('Loaded 4 memory blocks into context')).not.toBeInTheDocument();
  });

  it('toggles follow tail state on button click', async () => {
    render(<SafeLogViewer lines={mockLogLines} />);

    const followBtn = screen.getByRole('button', { name: /Following Tail/i });
    expect(followBtn).toHaveAttribute('aria-pressed', 'true');

    await userEvent.click(followBtn);
    expect(screen.getByRole('button', { name: /Follow Tail Paused/i })).toHaveAttribute('aria-pressed', 'false');
  });
});
