import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { CorrelationLink } from './CorrelationLink';

describe('CorrelationLink (T179)', () => {
  it('renders link to audit trace with formatted ID', () => {
    render(<CorrelationLink correlationId="req-1724000000-abcdef123456" />);
    const link = screen.getByRole('link');
    expect(link).toHaveAttribute('href', '/audit?correlation_id=req-1724000000-abcdef123456');
    expect(link).toHaveTextContent('req-1724…123456');
  });

  it('copies correlation ID to clipboard on copy button click', async () => {
    const writeTextMock = vi.fn().mockResolvedValue(undefined);
    Object.assign(navigator, {
      clipboard: { writeText: writeTextMock },
    });

    render(<CorrelationLink correlationId="req-test-999" />);
    const copyBtn = screen.getByRole('button', { name: /Copy correlation ID/i });
    await userEvent.click(copyBtn);

    expect(writeTextMock).toHaveBeenCalledWith('req-test-999');
    expect(await screen.findByRole('button', { name: /Copied/i })).toBeInTheDocument();
  });
});
