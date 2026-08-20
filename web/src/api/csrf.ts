let cachedCSRFToken: string | null = null;

export function getCookie(name: string): string | null {
  const match = document.cookie.match(new RegExp('(^|;\\s*)(' + name + ')=([^;]*)'));
  return match ? decodeURIComponent(match[3]) : null;
}

export async function fetchCSRFToken(): Promise<string> {
  // Check cookie first
  const cookieVal = getCookie('marshal_csrf');
  if (cookieVal) {
    cachedCSRFToken = cookieVal;
    return cookieVal;
  }

  if (cachedCSRFToken) {
    return cachedCSRFToken;
  }

  try {
    const resp = await fetch('/api/v1/auth/csrf', {
      method: 'GET',
      headers: { Accept: 'application/json' },
    });
    if (resp.ok) {
      const data = await resp.json();
      cachedCSRFToken = data.csrf_token || null;
      return cachedCSRFToken || '';
    }
  } catch {
    // Ignore fetch error
  }
  return '';
}

export function clearCSRFToken(): void {
  cachedCSRFToken = null;
}
