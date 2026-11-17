// Credentials in this fallback cache live only in the current JavaScript page.
// A reload/new page starts empty; durable recovery belongs exclusively to the
// nonextractable IndexedDB device key. Never store any bearer or public handle
// in Web Storage, and never import a legacy stored master into device pairing.
export const SERVICE_SESSION_KEY = 'social-intelligence.service-session.v1';

const validOrigin = (value: string) =>
  value === 'http://127.0.0.1:17890' || value === 'http://localhost:17890';
const validToken = (value: unknown): value is string =>
  typeof value === 'string' &&
  value.length > 0 &&
  value.length <= 4096 &&
  /^[\x21-\x7e]+$/.test(value);

let cached: { serviceURL: string; token: string } | null = null;

function purgeLegacySession(): boolean {
  try {
    // Remove our own obsolete key without reading/parsing its credential.
    window.sessionStorage.removeItem(SERVICE_SESSION_KEY);
    return true;
  } catch {
    return false;
  }
}
purgeLegacySession();

export function loadServiceSession(serviceURL: string): {
  token: string | null;
  // Compatibility name: reports whether legacy cleanup was possible, not
  // whether credentials are persisted (they never are).
  available: boolean;
} {
  const available = purgeLegacySession();
  if (!validOrigin(serviceURL)) return { token: null, available: false };
  return {
    token: cached?.serviceURL === serviceURL ? cached.token : null,
    available,
  };
}

export function saveServiceSession(serviceURL: string, token: string): boolean {
  const available = purgeLegacySession();
  if (!validOrigin(serviceURL) || !validToken(token)) return false;
  cached = { serviceURL, token };
  return available;
}

export function clearServiceSession(
  serviceURL: string,
  expectedToken?: string,
): boolean {
  const available = purgeLegacySession();
  if (!validOrigin(serviceURL)) return false;
  if (
    cached?.serviceURL === serviceURL &&
    (expectedToken === undefined || cached.token === expectedToken)
  )
    cached = null;
  return available;
}
