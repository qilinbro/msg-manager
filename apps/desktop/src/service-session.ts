// Only the verified local-service credential is kept for this browser tab.
// Platform credentials, chat content and AI drafts never enter this record.
export const SERVICE_SESSION_KEY = 'social-intelligence.service-session.v1';

const validOrigin = (value: string) =>
  value === 'http://127.0.0.1:17890' || value === 'http://localhost:17890';
const validToken = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 4096 &&
  /^[\x21-\x7e]+$/.test(value);

function storage(): Storage {
  return window.sessionStorage;
}

export function loadServiceSession(serviceURL: string): {
  token: string | null;
  available: boolean;
} {
  if (!validOrigin(serviceURL)) return { token: null, available: false };
  try {
    const raw = storage().getItem(SERVICE_SESSION_KEY);
    if (!raw || raw.length > 16384) return { token: null, available: true };
    let record: unknown;
    try {
      record = JSON.parse(raw);
    } catch {
      return { token: null, available: true };
    }
    if (typeof record !== 'object' || record === null || Array.isArray(record))
      return { token: null, available: true };
    const value = record as Record<string, unknown>;
    return {
      token:
        value.version === 1 &&
        value.service_url === serviceURL &&
        validToken(value.token)
          ? value.token
          : null,
      available: true,
    };
  } catch {
    return { token: null, available: false };
  }
}

export function saveServiceSession(serviceURL: string, token: string): boolean {
  if (!validOrigin(serviceURL) || !validToken(token)) return false;
  try {
    storage().setItem(
      SERVICE_SESSION_KEY,
      JSON.stringify({ version: 1, service_url: serviceURL, token }),
    );
    return true;
  } catch {
    return false;
  }
}

export function clearServiceSession(
  serviceURL: string,
  expectedToken?: string,
): boolean {
  if (!validOrigin(serviceURL)) return false;
  try {
    const target = storage();
    if (expectedToken !== undefined) {
      const saved = loadServiceSession(serviceURL);
      if (!saved.available) return false;
      if (saved.token !== expectedToken) return true;
    }
    target.removeItem(SERVICE_SESSION_KEY);
    return true;
  } catch {
    return false;
  }
}
