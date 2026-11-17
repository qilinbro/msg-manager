// Device handles are public UI identities, never bearer credentials. At 29
// characters they cannot collide with the service's >=32-character master token.
const HANDLE = /^device:[A-Za-z0-9_-]{21}[AQgw]$/;
const DATABASE = 'social-intelligence.device-auth.v1';
const STORE = 'devices';
const CHANNEL = 'social-intelligence.device-auth.v1';
const FRONTEND_ORIGINS = new Set([
  'http://127.0.0.1:1420',
  'http://localhost:1420',
  'tauri://localhost',
  'http://tauri.localhost',
  'https://tauri.localhost',
]);
const AUTH_MESSAGES: Record<string, string> = {
  AUTH_RETRY_REQUIRED:
    '连接已恢复。本次操作未自动重放，请先刷新确认状态，再由你重新提交。',
  DEVICE_AUTH_UNAVAILABLE:
    '本机授权暂时无法续期，已保留授权与草稿。请检查本地服务后重试。',
  DEVICE_KEY_UNAVAILABLE:
    '暂时无法读取本机授权，授权未被清除。请检查浏览器存储权限后重试。',
  DEVICE_AUTH_INVALID_RESPONSE:
    '本机授权响应未通过安全检查，未继续操作。请检查本地服务版本后重试。',
  DEVICE_AUTH_CONTEXT:
    '本机授权与当前页面或本地服务地址不匹配，请回到原地址重试。',
};

export class DeviceAuthError extends Error {
  constructor(
    public readonly code: string,
    public readonly status = 503,
  ) {
    super(
      authErrorMessage(
        code,
        '未能记住此浏览器，本次连接仅在当前页面内存中保留，刷新后需重新连接。',
      ),
    );
    this.name = 'DeviceAuthError';
  }
}
// A rejected proof may mean its short-lived challenge disappeared or expired.
// It is retryable, not by itself evidence that the durable grant was revoked.
class ProofRejectedError extends DeviceAuthError {
  constructor() {
    super('DEVICE_AUTH_UNAVAILABLE');
  }
}
export const authErrorMessage = (code: string, fallback: string): string =>
  Object.hasOwn(AUTH_MESSAGES, code) ? AUTH_MESSAGES[code] : fallback;
export const isDeviceHandle = (value: string): boolean => HANDLE.test(value);
export interface DeviceSession {
  handle: string;
  expiresAt: string;
}
interface DeviceRecord {
  version: 1;
  service_url: string;
  service_origin: string;
  frontend_origin: string;
  device_id: string;
  expires_at: string;
  private_key: CryptoKey;
}
interface Access {
  token: string;
  expires: number;
}
interface AccessState {
  epoch: number;
  forgotten: boolean;
  access: Access | null;
  pending: { promise: Promise<Access>; controller: AbortController } | null;
}
const states = new Map<string, AccessState>();
const forgetListeners = new Set<(serviceURL: string, handle: string) => void>();
let database: Promise<IDBDatabase> | null = null;
let channel: BroadcastChannel | null = null;

const record = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);
const abortError = () => new DOMException('请求已取消', 'AbortError');
function checkAbort(signal?: AbortSignal | null): void {
  if (signal?.aborted) throw abortError();
}
function frontendOrigin(): string {
  const origin = window.location.origin;
  if (!FRONTEND_ORIGINS.has(origin))
    throw new DeviceAuthError('DEVICE_AUTH_CONTEXT', 403);
  return origin;
}
export function deviceAuthAvailable(): boolean {
  // Check capabilities before reading location; the tab-only path needs neither
  // IndexedDB nor an ambient browser origin (and never touches localStorage).
  try {
    return Boolean(
      window.isSecureContext &&
      window.crypto?.subtle &&
      window.indexedDB &&
      FRONTEND_ORIGINS.has(window.location.origin),
    );
  } catch {
    return false;
  }
}
function serviceOrigin(serviceURL: string): string {
  const url = new URL(serviceURL);
  if (
    url.protocol !== 'http:' ||
    !['127.0.0.1', 'localhost'].includes(url.hostname) ||
    !url.port ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== '/' ||
    url.origin !== serviceURL
  )
    throw new DeviceAuthError('DEVICE_AUTH_CONTEXT', 403);
  return `http://127.0.0.1:${url.port}`;
}
function base64url(buffer: ArrayBuffer | Uint8Array): string {
  const bytes = buffer instanceof Uint8Array ? buffer : new Uint8Array(buffer);
  return btoa(String.fromCharCode(...bytes))
    .replace(/\+/g, '-')
    .replace(/\//g, '_')
    .replace(/=+$/, '');
}
function canonicalID(value: unknown, bytes: number): value is string {
  if (typeof value !== 'string' || !/^[A-Za-z0-9_-]+$/.test(value))
    return false;
  if (value.length !== Math.ceil((bytes * 8) / 6)) return false;
  try {
    const decoded = Uint8Array.from(
      atob(value.replace(/-/g, '+').replace(/_/g, '/')),
      (character) => character.charCodeAt(0),
    );
    return decoded.length === bytes && base64url(decoded) === value;
  } catch {
    return false;
  }
}
function expiry(value: unknown, maxAge?: number): number {
  if (
    typeof value !== 'string' ||
    value.length > 40 ||
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d{1,9})?(?:Z|[+-]\d{2}:\d{2})$/.test(
      value,
    )
  )
    return NaN;
  const parsed = Date.parse(value);
  return maxAge !== undefined &&
    (parsed <= Date.now() || parsed > Date.now() + maxAge)
    ? NaN
    : parsed;
}
function privateKey(value: unknown): value is CryptoKey {
  if (!record(value)) return false;
  const key = value as unknown as CryptoKey;
  return (
    key.type === 'private' &&
    key.extractable === false &&
    key.algorithm?.name === 'ECDSA' &&
    (key.algorithm as EcKeyAlgorithm).namedCurve === 'P-256' &&
    Array.isArray(key.usages) &&
    key.usages.length === 1 &&
    key.usages[0] === 'sign'
  );
}
function openDatabase(): Promise<IDBDatabase> {
  if (database) return database;
  const opening = new Promise<IDBDatabase>((resolve, reject) => {
    let settled = false;
    const fail = () => {
      settled = true;
      reject(new DeviceAuthError('DEVICE_KEY_UNAVAILABLE'));
    };
    const request = window.indexedDB.open(DATABASE, 1);
    request.onupgradeneeded = () => {
      if (!request.result.objectStoreNames.contains(STORE))
        request.result.createObjectStore(STORE);
    };
    request.onerror = fail;
    request.onblocked = fail;
    request.onsuccess = () => {
      if (settled) {
        request.result.close();
        return;
      }
      const db = request.result;
      db.onversionchange = () => {
        db.close();
        database = null;
      };
      resolve(db);
    };
  });
  database = opening;
  void opening.catch(() => {
    if (database === opening) database = null;
  });
  return opening;
}
async function stored<T>(
  mode: IDBTransactionMode,
  operation: (store: IDBObjectStore) => IDBRequest<T>,
): Promise<T> {
  try {
    const db = await openDatabase();
    return await new Promise<T>((resolve, reject) => {
      const transaction = db.transaction(STORE, mode);
      const request = operation(transaction.objectStore(STORE));
      let value: T;
      request.onsuccess = () => {
        value = request.result;
      };
      transaction.oncomplete = () => resolve(value);
      transaction.onerror = transaction.onabort = () =>
        reject(new DeviceAuthError('DEVICE_KEY_UNAVAILABLE'));
    });
  } catch {
    throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
  }
}
async function deleteRecord(
  serviceURL: string,
  expectedID: string,
): Promise<void> {
  try {
    const db = await openDatabase();
    await new Promise<void>((resolve, reject) => {
      const transaction = db.transaction(STORE, 'readwrite');
      const store = transaction.objectStore(STORE);
      const request = store.get(serviceURL);
      request.onsuccess = () => {
        if (record(request.result) && request.result.device_id === expectedID)
          store.delete(serviceURL);
      };
      transaction.oncomplete = () => resolve();
      transaction.onerror = transaction.onabort = () =>
        reject(new DeviceAuthError('DEVICE_KEY_UNAVAILABLE'));
    });
  } catch {
    throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
  }
}
async function readDevice(
  serviceURL: string,
  expectedID?: string,
): Promise<DeviceRecord | null> {
  if (!deviceAuthAvailable())
    throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
  const value: unknown = await stored('readonly', (store) =>
    store.get(serviceURL),
  );
  if (value === undefined) return null;
  if (
    !record(value) ||
    value.version !== 1 ||
    !canonicalID(value.device_id, 16) ||
    (expectedID !== undefined && value.device_id !== expectedID) ||
    !privateKey(value.private_key) ||
    !Number.isFinite(expiry(value.expires_at))
  )
    throw new DeviceAuthError('DEVICE_UNAUTHORIZED', 401);
  if (
    value.service_url !== serviceURL ||
    value.service_origin !== serviceOrigin(serviceURL) ||
    value.frontend_origin !== frontendOrigin()
  )
    throw new DeviceAuthError('DEVICE_AUTH_CONTEXT', 403);
  return value as unknown as DeviceRecord;
}
export async function loadDeviceSession(
  serviceURL: string,
): Promise<DeviceSession | null> {
  const saved = await readDevice(serviceURL);
  if (!saved) return null;
  if (expiry(saved.expires_at) <= Date.now()) {
    await forgetDevice(serviceURL, `device:${saved.device_id}`, false);
    return null;
  }
  return { handle: `device:${saved.device_id}`, expiresAt: saved.expires_at };
}
function stateFor(serviceURL: string, id: string): AccessState {
  const key = `${serviceURL}\n${id}`;
  let state = states.get(key);
  if (!state) {
    state = { epoch: 0, forgotten: false, access: null, pending: null };
    states.set(key, state);
  }
  return state;
}
function live(
  state: AccessState,
  epoch: number,
  signal?: AbortSignal | null,
): void {
  checkAbort(signal);
  if (state.forgotten || state.epoch !== epoch) throw abortError();
}
function invalidate(serviceURL: string, handle: string): void {
  const state = stateFor(serviceURL, handle.slice(7));
  state.epoch++;
  state.forgotten = true;
  state.access = null;
  state.pending?.controller.abort();
  state.pending = null;
  for (const listener of forgetListeners) listener(serviceURL, handle);
}
function ensureChannel(): void {
  if (channel || !deviceAuthAvailable()) return;
  try {
    channel = new window.BroadcastChannel(CHANNEL);
    channel.onmessage = ({ data }: MessageEvent<unknown>) => {
      if (
        record(data) &&
        data.type === 'forgotten' &&
        typeof data.service_url === 'string' &&
        typeof data.handle === 'string' &&
        isDeviceHandle(data.handle)
      )
        invalidate(data.service_url, data.handle);
    };
  } catch {
    // Every device request also checks IndexedDB. Cross-tab safety does not
    // depend on BroadcastChannel being implemented in this WebView.
  }
}
export function subscribeDeviceForget(
  listener: (serviceURL: string, handle: string) => void,
): () => void {
  forgetListeners.add(listener);
  ensureChannel();
  return () => {
    forgetListeners.delete(listener);
    if (!forgetListeners.size) {
      channel?.close();
      channel = null;
    }
  };
}
function waitFor<T>(
  promise: Promise<T>,
  signal?: AbortSignal | null,
): Promise<T> {
  if (!signal) return promise;
  checkAbort(signal);
  return new Promise<T>((resolve, reject) => {
    const stop = () => reject(abortError());
    signal.addEventListener('abort', stop, { once: true });
    promise
      .then(resolve, reject)
      .finally(() => signal.removeEventListener('abort', stop));
  });
}
async function authJSON(
  serviceURL: string,
  path: string,
  body: unknown,
  signal: AbortSignal,
  master?: string,
): Promise<Record<string, unknown>> {
  checkAbort(signal);
  const controller = new AbortController();
  const stop = () => controller.abort();
  signal.addEventListener('abort', stop, { once: true });
  const timer = window.setTimeout(stop, 10_000);
  try {
    const response = await fetch(`${serviceURL}/api/v1/auth/${path}`, {
      method: 'POST',
      headers: {
        Accept: 'application/json',
        'Content-Type': 'application/json',
        ...(master === undefined ? {} : { Authorization: `Bearer ${master}` }),
      },
      body: JSON.stringify(body),
      signal: controller.signal,
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    });
    const value: unknown = await response.json().catch(() => null);
    checkAbort(signal);
    if (controller.signal.aborted)
      throw new DeviceAuthError('DEVICE_AUTH_UNAVAILABLE');
    if (!response.ok) {
      if (path === 'sessions' && response.status === 401)
        throw new ProofRejectedError();
      // Only an explicit challenge-endpoint rejection proves a grant invalid.
      // A generic/proxy 401 must not erase a still-valid 90-day device key.
      const terminalUnauthorized =
        response.status === 401 &&
        (path === 'devices' ||
          (path === 'challenges' &&
            record(value) &&
            record(value.error) &&
            value.error.code === 'DEVICE_UNAUTHORIZED'));
      throw new DeviceAuthError(
        terminalUnauthorized
          ? 'DEVICE_UNAUTHORIZED'
          : 'DEVICE_AUTH_UNAVAILABLE',
        terminalUnauthorized
          ? 401
          : [401, 404].includes(response.status)
            ? 503
            : response.status,
      );
    }
    if (!record(value))
      throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
    return value;
  } catch (error) {
    checkAbort(signal);
    if (error instanceof DeviceAuthError) throw error;
    throw new DeviceAuthError('DEVICE_AUTH_UNAVAILABLE');
  } finally {
    window.clearTimeout(timer);
    signal.removeEventListener('abort', stop);
  }
}
async function exchange(
  saved: DeviceRecord,
  signal: AbortSignal,
  retried = false,
): Promise<Access> {
  const challenge = await authJSON(
    saved.service_url,
    'challenges',
    { device_id: saved.device_id },
    signal,
  );
  if (
    !canonicalID(challenge.challenge_id, 32) ||
    !canonicalID(challenge.nonce, 32) ||
    !Number.isFinite(expiry(challenge.expires_at, 120_000))
  )
    throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
  const message = [
    'sociald-device-auth-v1',
    serviceOrigin(saved.service_url),
    frontendOrigin(),
    saved.device_id,
    challenge.challenge_id,
    challenge.nonce,
    challenge.expires_at,
  ].join('\n');
  if (challenge.message !== message)
    throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
  checkAbort(signal);
  const signature = await window.crypto.subtle.sign(
    { name: 'ECDSA', hash: 'SHA-256' },
    saved.private_key,
    new TextEncoder().encode(message),
  );
  checkAbort(signal);
  if (signature.byteLength !== 64)
    throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
  let session: Record<string, unknown>;
  try {
    session = await authJSON(
      saved.service_url,
      'sessions',
      {
        device_id: saved.device_id,
        challenge_id: challenge.challenge_id,
        signature: base64url(signature),
      },
      signal,
    );
  } catch (error) {
    // A restart or suspended tab can lose/expire a 60-second challenge while
    // the durable grant remains valid. Retry only this proof, at most once.
    if (error instanceof ProofRejectedError && !retried)
      return exchange(saved, signal, true);
    throw error;
  }
  const expires = expiry(session.expires_at, 16 * 60_000);
  if (
    typeof session.access_token !== 'string' ||
    !/^[\x21-\x7e]{32,4096}$/.test(session.access_token) ||
    !Number.isFinite(expires)
  )
    throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
  return { token: session.access_token, expires };
}
async function accessFor(
  serviceURL: string,
  id: string,
  state: AccessState,
  epoch: number,
  signal?: AbortSignal | null,
  rejectedToken?: string,
): Promise<Access> {
  live(state, epoch, signal);
  // Check the descriptor even when a memory-only access token is fresh. This
  // notices local deletion/replacement across tabs without a storage-event shim.
  const saved = await readDevice(serviceURL, id);
  live(state, epoch, signal);
  if (!saved || expiry(saved.expires_at) <= Date.now())
    throw new DeviceAuthError('DEVICE_UNAUTHORIZED', 401);
  if (
    state.access &&
    state.access.expires > Date.now() + 30_000 &&
    state.access.token !== rejectedToken
  )
    return state.access;
  if (!state.pending) {
    state.access = null;
    const controller = new AbortController();
    const promise = (async () => {
      const access = await exchange(saved, controller.signal);
      live(state, epoch);
      const current = await readDevice(serviceURL, id);
      live(state, epoch);
      if (!current) throw new DeviceAuthError('DEVICE_UNAUTHORIZED', 401);
      state.access = access;
      return access;
    })();
    const pending = { promise, controller };
    state.pending = pending;
    void promise
      .finally(() => {
        if (state.pending === pending) state.pending = null;
      })
      .catch(() => {});
  }
  return waitFor(state.pending.promise, signal);
}
function problem(status: number, code: string): Response {
  return new Response(JSON.stringify({ error: { code } }), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}
export async function authorizedFetch(
  url: string,
  credential: string,
  init: RequestInit,
): Promise<Response> {
  // The existing explicit master-token path is deliberately untouched: no
  // IndexedDB, origin reads, implicit retry, cookie or new failure semantics.
  if (!isDeviceHandle(credential)) return fetch(url, init);
  let serviceURL: string;
  try {
    const target = new URL(url);
    serviceURL = target.origin;
    serviceOrigin(serviceURL);
    if (
      target.username ||
      target.password ||
      target.hash ||
      !target.pathname.startsWith('/api/v1/')
    )
      return problem(403, 'DEVICE_AUTH_CONTEXT');
  } catch {
    return problem(403, 'DEVICE_AUTH_CONTEXT');
  }
  ensureChannel();
  const id = credential.slice(7);
  const state = stateFor(serviceURL, id);
  const epoch = state.epoch;
  const send = (access: Access) => {
    live(state, epoch, init.signal);
    const headers = new Headers(init.headers);
    headers.set('Authorization', `Bearer ${access.token}`);
    return fetch(url, {
      ...init,
      headers,
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    });
  };
  try {
    const access = await accessFor(serviceURL, id, state, epoch, init.signal);
    const response = await send(access);
    live(state, epoch, init.signal);
    if (response.status !== 401) return response;
    const replacement = await accessFor(
      serviceURL,
      id,
      state,
      epoch,
      init.signal,
      access.token,
    );
    live(state, epoch, init.signal);
    const method = (init.method ?? 'GET').toUpperCase();
    // Only a read can be retried. An import/model call/save is never replayed,
    // even if the backend probably rejected it before processing its body.
    if (method !== 'GET' && method !== 'HEAD')
      return problem(409, 'AUTH_RETRY_REQUIRED');
    const retried = await send(replacement);
    live(state, epoch, init.signal);
    if (retried.status === 401) {
      // A business/proxy rejection does not establish grant revocation. Stop
      // after this one read retry and let a later request prove again safely.
      if (state.access?.token === replacement.token) state.access = null;
      return problem(503, 'DEVICE_AUTH_UNAVAILABLE');
    }
    return retried;
  } catch (error) {
    checkAbort(init.signal);
    if (error instanceof DOMException && error.name === 'AbortError')
      throw error;
    if (error instanceof DeviceAuthError)
      return problem(error.status, error.code);
    throw error;
  }
}
async function revoke(
  serviceURL: string,
  id: string,
  token: string,
  signal?: AbortSignal,
): Promise<boolean> {
  if (signal?.aborted) return false;
  const controller = new AbortController();
  const stop = () => controller.abort();
  signal?.addEventListener('abort', stop, { once: true });
  const timer = window.setTimeout(stop, 5_000);
  try {
    const response = await fetch(`${serviceURL}/api/v1/auth/devices/${id}`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${token}` },
      signal: controller.signal,
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    });
    return response.status === 204 && !controller.signal.aborted;
  } catch {
    return false;
  } finally {
    window.clearTimeout(timer);
    signal?.removeEventListener('abort', stop);
  }
}
export async function forgetDevice(
  serviceURL: string,
  handle: string,
  revokeRemote = true,
): Promise<{ forgotten: boolean; revoked: boolean }> {
  if (!isDeviceHandle(handle)) return { forgotten: true, revoked: false };
  const id = handle.slice(7);
  const access = stateFor(serviceURL, id).access;
  // Capture a clone before deleting. If this tab has no usable access token,
  // this snapshot may prove possession ONLY for revocation; it never repopulates
  // the access cache or enables a queued business request after forgetting.
  const snapshot = revokeRemote
    ? readDevice(serviceURL, id).catch(() => null)
    : Promise.resolve(null);
  invalidate(serviceURL, handle);
  ensureChannel();
  try {
    channel?.postMessage({
      type: 'forgotten',
      service_url: serviceURL,
      handle,
    });
  } catch {
    /* IDB remains authoritative. */
  }
  const remote = async () => {
    if (!revokeRemote) return false;
    const controller = new AbortController();
    const timer = window.setTimeout(() => controller.abort(), 10_000);
    try {
      if (
        access &&
        (await revoke(serviceURL, id, access.token, controller.signal))
      )
        return true;
      const saved = await snapshot;
      if (!saved || expiry(saved.expires_at) <= Date.now()) return false;
      const fresh = await exchange(saved, controller.signal);
      return await revoke(serviceURL, id, fresh.token, controller.signal);
    } catch {
      return false;
    } finally {
      window.clearTimeout(timer);
    }
  };
  const [forgotten, revoked] = await Promise.all([
    deleteRecord(serviceURL, id).then(
      () => true,
      () => false,
    ),
    remote(),
  ]);
  return { forgotten, revoked };
}
export async function pairDevice(
  serviceURL: string,
  master: string,
  signal: AbortSignal,
): Promise<DeviceSession> {
  if (!deviceAuthAvailable())
    throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
  serviceOrigin(serviceURL);
  const origin = frontendOrigin();
  checkAbort(signal);
  const previous = await readDevice(serviceURL);
  checkAbort(signal);
  const keys = await window.crypto.subtle.generateKey(
    { name: 'ECDSA', namedCurve: 'P-256' },
    false,
    ['sign', 'verify'],
  );
  const probeKey = `probe:${base64url(window.crypto.getRandomValues(new Uint8Array(16)))}`;
  let createdID: string | null = null;
  let committed = false;
  try {
    // A real structured-clone roundtrip and sign/verify test precedes enrollment.
    // There is deliberately no extractable-key or plaintext fallback.
    await stored('readwrite', (store) => store.put(keys.privateKey, probeKey));
    const restored: unknown = await stored('readonly', (store) =>
      store.get(probeKey),
    );
    if (!privateKey(restored))
      throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
    const probe = new TextEncoder().encode('sociald-device-key-check-v1');
    const signature = await window.crypto.subtle.sign(
      { name: 'ECDSA', hash: 'SHA-256' },
      restored,
      probe,
    );
    if (
      !(await window.crypto.subtle.verify(
        { name: 'ECDSA', hash: 'SHA-256' },
        keys.publicKey,
        signature,
        probe,
      ))
    )
      throw new DeviceAuthError('DEVICE_KEY_UNAVAILABLE');
    checkAbort(signal);
    const publicKey = await window.crypto.subtle.exportKey(
      'spki',
      keys.publicKey,
    );
    const paired = await authJSON(
      serviceURL,
      'devices',
      { public_key: base64url(publicKey) },
      signal,
      master,
    );
    // Capture a canonical public ID even if a later response field is invalid,
    // so the short-lived master can attempt enrollment rollback.
    if (canonicalID(paired.device_id, 16)) createdID = paired.device_id;
    if (
      !createdID ||
      !Number.isFinite(expiry(paired.expires_at, 90 * 86_400_000 + 60_000)) ||
      paired.service_origin !== serviceOrigin(serviceURL) ||
      paired.frontend_origin !== origin
    )
      throw new DeviceAuthError('DEVICE_AUTH_INVALID_RESPONSE');
    const saved: DeviceRecord = {
      version: 1,
      service_url: serviceURL,
      service_origin: paired.service_origin,
      frontend_origin: origin,
      device_id: createdID,
      expires_at: paired.expires_at as string,
      private_key: restored,
    };
    // Prove the new grant before replacing an existing key. A failed proof or
    // atomic IDB put must not strand the previous descriptor unnecessarily.
    const access = await exchange(saved, signal);
    const state = stateFor(serviceURL, createdID);
    const epoch = state.epoch;
    live(state, epoch, signal);
    await stored('readwrite', (store) => store.put(saved, serviceURL));
    committed = true;
    live(state, epoch, signal);
    const current = await readDevice(serviceURL, createdID);
    live(state, epoch, signal);
    if (!current) throw new DeviceAuthError('DEVICE_UNAUTHORIZED', 401);
    state.access = access;
    if (previous && previous.device_id !== createdID) {
      void forgetDevice(serviceURL, `device:${previous.device_id}`, false);
      void revoke(serviceURL, previous.device_id, master);
    }
    return { handle: `device:${createdID}`, expiresAt: saved.expires_at };
  } catch (error) {
    if (createdID) {
      invalidate(serviceURL, `device:${createdID}`);
      await deleteRecord(serviceURL, createdID).catch(() => {});
      // Enrollment is not replayed. Cleanup may fail offline, so never promise
      // that the server grant was revoked merely because its local key is gone.
      void revoke(serviceURL, createdID, master);
      if (committed && previous && previous.device_id !== createdID)
        void revoke(serviceURL, previous.device_id, master);
    }
    throw error;
  } finally {
    await stored('readwrite', (store) => store.delete(probeKey)).catch(
      () => {},
    );
  }
}
