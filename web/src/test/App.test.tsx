import { describe, it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import App from '../App';

describe('App', () => {
  it('renders MARSHAL Control Plane heading', () => {
    render(<App />);
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent(
      'MARSHAL Control Plane',
    );
  });

  it('does not expose secrets in document', () => {
    render(<App />);
    const html = document.documentElement.innerHTML;
    const forbidden = [
      'private_key',
      'secret_key',
      'bearer_token',
      'api_token',
      'VITE_SECRET',
    ];
    for (const token of forbidden) {
      expect(html.toLowerCase()).not.toContain(token.toLowerCase());
    }
  });
});
