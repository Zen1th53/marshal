import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import App from '../App';

describe('App (T180)', () => {
  beforeEach(() => {
    vi.restoreAllMocks();
    vi.spyOn(globalThis, 'fetch').mockImplementation((url) => {
      const u = String(url);
      if (u.includes('/api/v1/auth/me')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              principal_id: 'operator-loopback',
              role: 'admin',
              authorities: ['*'],
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } }
          )
        );
      }
      if (u.includes('/api/v1/system/capabilities')) {
        return Promise.resolve(
          new Response(
            JSON.stringify({
              capabilities: {
                'cap:system:read': { state: 'AVAILABLE' },
                'cap:task:read': { state: 'AVAILABLE' },
              },
            }),
            { status: 200, headers: { 'Content-Type': 'application/json' } }
          )
        );
      }
      return Promise.resolve(
        new Response(JSON.stringify({}), {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        })
      );
    });
  });

  it('renders MARSHAL heading after loading', async () => {
    render(<App />);
    expect(await screen.findByRole('heading', { level: 1 })).toHaveTextContent('MARSHAL');
  });

  it('does not expose secrets in document', async () => {
    render(<App />);
    await screen.findByRole('heading', { level: 1 });
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
