import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Button, StatusBadge, CodeBlock } from '../components/ui';

describe('Button', () => {
  it('renders with text content', () => {
    render(<Button>Click me</Button>);
    expect(screen.getByRole('button')).toHaveTextContent('Click me');
  });

  it('applies disabled state with aria-disabled', () => {
    render(<Button disabled>Disabled</Button>);
    const btn = screen.getByRole('button');
    expect(btn).toBeDisabled();
    expect(btn).toHaveAttribute('aria-disabled', 'true');
  });

  it('supports keyboard activation', async () => {
    let clicked = false;
    render(<Button onClick={() => { clicked = true; }}>Press</Button>);
    const btn = screen.getByRole('button');
    btn.focus();
    await userEvent.keyboard('{Enter}');
    expect(clicked).toBe(true);
  });

  it('applies variant class', () => {
    render(<Button variant="danger">Delete</Button>);
    expect(screen.getByRole('button')).toHaveClass('btn-danger');
  });
});

describe('StatusBadge', () => {
  it('renders with icon and label text — never color-only', () => {
    render(<StatusBadge status="ready" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent('Ready');
    expect(badge.querySelector('.status-icon')).toBeTruthy();
    expect(badge.querySelector('.status-label')).toBeTruthy();
  });

  it('renders degraded status with distinct icon', () => {
    render(<StatusBadge status="degraded" />);
    const badge = screen.getByRole('status');
    expect(badge).toHaveTextContent('Degraded');
    expect(badge.querySelector('.status-icon')?.textContent).toBe('▲');
  });

  it('uses custom label when provided', () => {
    render(<StatusBadge status="error" label="Build Failed" />);
    expect(screen.getByRole('status')).toHaveTextContent('Build Failed');
  });
});

describe('CodeBlock', () => {
  it('renders code as text content, not innerHTML', () => {
    const malicious = '<script>alert("xss")</script>';
    render(<CodeBlock>{malicious}</CodeBlock>);
    const codeEl = document.querySelector('.code-block code');
    expect(codeEl?.textContent).toBe(malicious);
    expect(codeEl?.innerHTML).not.toContain('<script>');
  });
});
