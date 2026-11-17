import assert from 'node:assert/strict';
import { randomBytes, webcrypto } from 'node:crypto';
import { setImmediate as nextTurn } from 'node:timers/promises';
import { browserHarness, syntheticStorage, system, nodes, find, settings } from './test-service-session.mjs';

// Shipped TS transport, hook, API clients and App execute in isolated VM tabs.
// Node WebCrypto really generates/signs/verifies P-256 keys; every IDB put/get
// uses native structuredClone (including nonextractable CryptoKey handles).
// The HTTP service/IDB scheduler/React lifecycle are synthetic, not browser or
// Tauri acceptance. No real service, credentials, chat, disk DB or network IO.
const SERVICE = 'http://127.0.0.1:17890';
const FRONTEND = 'http://127.0.0.1:1420';
const MASTER = 'synthetic-master-only-for-enrollment-0123456789';
const DAY = 86_400_000;
const rawurl = (bytes) => randomBytes(bytes).toString('base64url');
const json = (status, value) => new Response(status === 204 ? null : JSON.stringify(value), {
  status, headers: { 'Content-Type': 'application/json' },
});
const unauthorized = () => json(401, { error: { code: 'UNAUTHORIZED' } });
const deniedDevice = () => json(401, { error: { code: 'DEVICE_UNAUTHORIZED' } });
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };
let groups = 0;

function indexedDBHarness() {
  const values = new Map(), queue = [], writes = [];
  let active = false, created = false;
  const factory = {
    values, writes, denied: false, readDenied: false, writeDenied: false,
    failClone: false, failDescriptorPut: false, beforeGet: null,
    open(name, version) {
      assert.equal(name, 'social-intelligence.device-auth.v1');
      assert.equal(version, 1);
      const request = {};
      queueMicrotask(() => {
        if (factory.denied) { request.error = new DOMException('synthetic IDB denial', 'SecurityError'); request.onerror?.(); return; }
        request.result = {
          objectStoreNames: { contains: (name) => created && name === 'devices' },
          createObjectStore(name) { assert.equal(name, 'devices'); created = true; },
          close() {},
          transaction(name, mode) {
            assert.equal(name, 'devices');
            assert.ok(mode === 'readonly' || mode === 'readwrite');
            const operations = [], changes = new Map();
            const tx = {
              objectStore(name) {
                assert.equal(name, 'devices');
                function operation(kind, key, input) {
                  if (kind !== 'get') assert.equal(mode, 'readwrite');
                  if (kind === 'put' && factory.failClone) throw new DOMException('synthetic clone denial', 'DataCloneError');
                  const copy = kind === 'put' ? structuredClone(input) : undefined;
                  const request = {};
                  operations.push({ kind, key, copy, request });
                  return request;
                }
                return {
                  get: (key) => operation('get', key),
                  put: (value, key) => operation('put', key, value),
                  delete: (key) => operation('delete', key),
                };
              },
            };
            queue.push(async () => {
              try {
                for (let i = 0; i < operations.length; i++) {
                  const { kind, key, copy, request } = operations[i];
                  if (kind === 'get') {
                    if (factory.beforeGet) await factory.beforeGet(key);
                    if (factory.readDenied) throw new DOMException('synthetic read denial', 'UnknownError');
                    request.result = structuredClone(changes.has(key) ? changes.get(key) : values.get(key));
                  } else {
                    if (factory.writeDenied || (factory.failDescriptorPut && kind === 'put' && copy?.version === 1))
                      throw new DOMException('synthetic write denial', 'QuotaExceededError');
                    changes.set(key, kind === 'delete' ? undefined : copy);
                    request.result = kind === 'put' ? key : undefined;
                    writes.push({ kind, key, value: copy });
                  }
                  request.onsuccess?.();
                }
                for (const [key, value] of changes) value === undefined ? values.delete(key) : values.set(key, value);
                tx.oncomplete?.();
              } catch (error) {
                tx.error = error;
                tx.onabort?.();
                tx.onerror?.();
              } finally {
                active = false;
                start();
              }
            });
            queueMicrotask(start);
            return tx;
          },
        };
        if (!created) request.onupgradeneeded?.();
        request.onsuccess?.();
      });
      return request;
    },
  };
  function start() {
    if (active || !queue.length) return;
    active = true;
    void queue.shift()();
  }
  return factory;
}

function broadcastHarness() {
  const channels = new Set(), messages = [];
  return {
    messages,
    Channel: class {
      constructor(name) { this.name = name; channels.add(this); }
      postMessage(value) {
        messages.push(structuredClone(value));
        for (const peer of channels) if (peer !== this && peer.name === this.name)
          queueMicrotask(() => channels.has(peer) && peer.onmessage?.({ data: structuredClone(value) }));
      }
      close() { channels.delete(this); }
    },
  };
}

function serviceHarness() {
  const requests = [], grants = new Map(), challenges = new Map(), access = new Map(), verifiedMessages = [];
  const server = {
    requests, grants, challenges, access, verifiedMessages,
    before: null, pairResponse: null, challengeResponse: null, sessionResponse: null,
    restart() { access.clear(); challenges.clear(); },
    count(path, method) { return requests.filter((r) => r.path === `/api/v1/${path}` && (!method || r.method === method)).length; },
    async fetch(url, options, frontend = FRONTEND) {
      if (options.signal?.aborted) throw new DOMException('synthetic canceled fetch', 'AbortError');
      const target = new URL(url), headers = new Headers(options.headers);
      const request = { url, path: target.pathname, method: options.method ?? 'GET', headers, options, frontend };
      if (request.method === 'POST' && request.path.startsWith('/api/v1/auth/')) request.body = JSON.parse(options.body);
      requests.push(request);
      assert.equal(options.credentials, 'omit');
      assert.equal(options.cache, 'no-store');
      assert.equal(target.search, '', 'no credential/challenge may enter a URL query');
      const intercepted = await server.before?.(request);
      if (intercepted !== undefined) return intercepted;
      const bearer = headers.get('Authorization')?.replace(/^Bearer /, '');
      const authorized = bearer === MASTER || (() => {
        const token = access.get(bearer);
        return token && token.expires > Date.now() && !grants.get(token.id)?.revoked;
      })();
      const canonicalService = `http://127.0.0.1:${target.port}`;
      if (target.pathname.startsWith('/api/v1/auth/')) {
        assert.equal(options.redirect, 'error', 'all auth fetches reject redirects');
        if (request.method === 'DELETE') {
          assert.equal(options.body, undefined);
          const id = target.pathname.split('/').at(-1), token = access.get(bearer);
          if (!authorized || (bearer !== MASTER && token.id !== id)) return unauthorized();
          const grant = grants.get(id);
          if (grant) grant.revoked = true;
          for (const [value, token] of access) if (token.id === id) access.delete(value);
          return json(204);
        }
        assert.equal(request.method, 'POST');
        assert.equal(headers.get('Content-Type'), 'application/json');
        const body = JSON.parse(options.body);
        request.body = body;
        if (request.path === '/api/v1/auth/devices') {
          assert.deepEqual(Object.keys(body), ['public_key']);
          assert.equal(bearer, MASTER, 'only this single enrollment request may use the master');
          const publicKey = await webcrypto.subtle.importKey('spki', Buffer.from(body.public_key, 'base64url'), { name: 'ECDSA', namedCurve: 'P-256' }, true, ['verify']);
          if ([...grants.values()].filter((g) => !g.revoked).length >= 32) return json(429, { error: { code: 'DEVICE_LIMIT' } });
          const id = rawurl(16), expires = new Date(Date.now() + 90 * DAY).toISOString();
          grants.set(id, { publicKey, frontend, service: canonicalService, expires, revoked: false });
          const result = { device_id: id, expires_at: expires, service_origin: canonicalService, frontend_origin: frontend };
          return json(201, server.pairResponse ? server.pairResponse(result) : result);
        }
        if (request.path === '/api/v1/auth/challenges') {
          assert.deepEqual(Object.keys(body), ['device_id']);
          assert.equal(bearer, undefined);
          const grant = grants.get(body.device_id);
          if (!grant || grant.revoked || grant.frontend !== frontend || grant.service !== canonicalService || Date.parse(grant.expires) <= Date.now()) return deniedDevice();
          const challenge_id = rawurl(32), nonce = rawurl(32), expires_at = new Date(Date.now() + 60_000).toISOString();
          const message = ['sociald-device-auth-v1', canonicalService, frontend, body.device_id, challenge_id, nonce, expires_at].join('\n');
          const result = { challenge_id, nonce, expires_at, message };
          challenges.set(challenge_id, { ...result, id: body.device_id, used: false });
          return json(200, server.challengeResponse ? server.challengeResponse(result) : result);
        }
        if (request.path === '/api/v1/auth/sessions') {
          assert.deepEqual(Object.keys(body).sort(), ['challenge_id', 'device_id', 'signature']);
          assert.equal(bearer, undefined);
          const challenge = challenges.get(body.challenge_id), grant = grants.get(body.device_id);
          if (!grant || grant.revoked || !challenge || challenge.used || challenge.id !== body.device_id || grant.frontend !== frontend || grant.service !== canonicalService || Date.parse(challenge.expires_at) <= Date.now()) return deniedDevice();
          challenge.used = true;
          const signature = Buffer.from(body.signature, 'base64url');
          assert.equal(signature.length, 64, 'signature is real 64-byte P1363, not DER or a stub');
          assert.equal(signature.toString('base64url'), body.signature, 'signature encoding is canonical');
          const verified = await webcrypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, grant.publicKey, signature, new TextEncoder().encode(challenge.message));
          assert.equal(verified, true, 'strict seven-line challenge must verify against enrolled SPKI');
          verifiedMessages.push(challenge.message);
          const token = `synthetic-access-${rawurl(32)}`, expires = Date.now() + 15 * 60_000;
          access.set(token, { id: body.device_id, expires });
          const result = { access_token: token, expires_at: new Date(expires).toISOString() };
          return json(200, server.sessionResponse ? server.sessionResponse(result) : result);
        }
        assert.fail(`unexpected auth path: ${request.path}`);
      }
      assert.ok(!bearer?.startsWith('device:'), 'a public UI handle must never reach Authorization');
      if (!authorized) return unauthorized();
      return json(200, system());
    },
  };
  return server;
}

function setup({ idb = indexedDBHarness(), server = serviceHarness(), broadcasts = broadcastHarness(), origin = FRONTEND, storage = syntheticStorage(), mode = 'hook', channel = true, secure = true, crypto = webcrypto } = {}) {
  const authBrowser = {
    window: { isSecureContext: secure, crypto, indexedDB: idb, location: { origin }, BroadcastChannel: channel ? broadcasts.Channel : undefined },
    globals: { TextEncoder, Headers, Response, Uint8Array, ArrayBuffer, atob, btoa },
    fetch: (url, init) => server.fetch(url, init, origin),
  };
  const h = browserHarness({ storage, mode, authBrowser });
  return Object.assign(h, { idb, server, broadcasts });
}
async function until(h, predicate, description = 'asynchronous state did not settle', render = true) {
  for (let i = 0; i < 500; i++) {
    if (render) await h.flush();
    else await Promise.resolve();
    if (predicate()) return;
    await nextTurn();
  }
  assert.fail(description);
}
async function pair(h, remember = true) {
  await h.flush();
  h.value.setRememberDevice(remember);
  h.value.setTokenDraft(MASTER);
  await h.flush();
  await h.value.connect({ preventDefault() {} });
  await h.flush();
  return h.value.activeToken;
}
function business(h, handle, method = 'GET', path = 'system', signal) {
  return h.auth.authorizedFetch(`${SERVICE}/api/v1/${path}`, handle, {
    method, signal, headers: { Accept: 'application/json', Authorization: `Bearer ${handle}` },
    credentials: 'omit', cache: 'no-store',
    ...(method !== 'GET' && method !== 'HEAD' ? { body: JSON.stringify({ draft: 'synthetic unsaved mutation' }) } : {}),
  });
}
function sameStorage(h, id) {
  assert.equal(h.idb.values.get(SERVICE)?.device_id, id);
  const persisted = JSON.stringify([...h.idb.values]) + JSON.stringify([...h.storage.values]);
  assert.ok(!persisted.includes(MASTER), 'master must never persist in IDB or Web Storage');
  assert.equal(h.storage.values.size, 0, 'not even public handles belong in sessionStorage');
  assert.equal(h.storage.reads.length, 0, 'legacy sessionStorage is removed without reading');
  assert.equal(h.storage.writes.length, 0, 'sessionStorage writes are forbidden');
  assert.equal(h.localStorageReads, 0);
  for (const token of h.server.access.keys()) assert.ok(!persisted.includes(token), 'access tokens are memory-only');
  for (const request of h.server.requests.filter((r) => r.path === '/api/v1/auth/sessions')) {
    assert.ok(!persisted.includes(request.body.signature), 'signatures/challenges must not persist');
    assert.ok(!persisted.includes(request.body.challenge_id));
  }
}
async function retainedError(response) { assert.notEqual(response.status, 401); return (await response.json()).error.code; }

// Capability checks precede location access; direct-master calls are unchanged.
{
  const h = browserHarness();
  assert.equal(h.auth.deviceAuthAvailable(), false);
  assert.equal((await h.api.getSystem(MASTER, new AbortController().signal)).version, 'synthetic');
  assert.equal(h.requests[0].options.headers.Authorization, `Bearer ${MASTER}`);
  assert.equal(h.locationReads, 0);
  assert.equal(h.localStorageReads, 0);
  assert.equal(h.auth.isDeviceHandle(`device:${Buffer.alloc(16).toString('base64url')}`), true);
  assert.equal(h.auth.isDeviceHandle(`device:${'A'.repeat(21)}B`), false, 'noncanonical final base64 bits are rejected');
  h.unmount(); groups++;
}

// Actual nonextractable key + IDB structured-clone roundtrip, proof, persistence.
{
  const h = setup();
  assert.equal((await pair(h)).length, 29);
  const handle = h.value.activeToken, id = handle.slice(7), saved = h.idb.values.get(SERVICE);
  assert.equal(h.value.rememberDevice, true);
  assert.equal(h.value.remembered, true);
  assert.equal(h.value.tokenDraft, '');
  assert.equal(h.value.session.status, 'connected');
  assert.deepEqual([...h.idb.values.keys()], [SERVICE], 'temporary CryptoKey probe is removed');
  assert.equal(saved.private_key.type, 'private');
  assert.equal(saved.private_key.extractable, false);
  assert.equal(saved.private_key.algorithm.name, 'ECDSA');
  assert.equal(saved.private_key.algorithm.namedCurve, 'P-256');
  assert.deepEqual([...saved.private_key.usages], ['sign']);
  await assert.rejects(webcrypto.subtle.exportKey('pkcs8', saved.private_key), (e) => e.name === 'InvalidAccessException');
  const wrapping = await webcrypto.subtle.generateKey({ name: 'AES-GCM', length: 256 }, false, ['wrapKey']);
  await assert.rejects(webcrypto.subtle.wrapKey('pkcs8', saved.private_key, wrapping, { name: 'AES-GCM', iv: new Uint8Array(12) }));
  const copy = structuredClone(saved.private_key), proof = new TextEncoder().encode('synthetic restored-key proof');
  const signature = await webcrypto.subtle.sign({ name: 'ECDSA', hash: 'SHA-256' }, copy, proof);
  assert.equal(await webcrypto.subtle.verify({ name: 'ECDSA', hash: 'SHA-256' }, h.server.grants.get(id).publicKey, signature, proof), true);
  assert.equal(h.server.verifiedMessages[0].split('\n').length, 7);
  assert.equal(h.server.count('auth/devices', 'POST'), 1);
  sameStorage(h, id);
  const original = h.server.requests.find((r) => r.path === '/api/v1/auth/sessions');
  const replay = await h.server.fetch(original.url, original.options);
  assert.equal(replay.status, 401, 'a consumed challenge/signature is not reusable');
  h.unmount(); groups++;
}

// Fresh tab and service restart restore without master; refresh keeps identity,
// callbacks, React keys and pending draft map rather than remounting editors.
{
  const first = setup();
  const handle = await pair(first), { idb, server, broadcasts } = first;
  first.unmount(); server.restart();
  const h = setup({ idb, server, broadcasts });
  await until(h, () => h.value.session.status === 'connected', 'new tab must restore from cloned CryptoKey');
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.value.tokenDraft, '');
  assert.equal(server.count('auth/devices', 'POST'), 1, 'restoration never repeats enrollment');
  const callback = h.value.onAuthFailure, update = h.value.onSystemUpdate, drafts = h.aiDrafts;
  drafts.set('synthetic-chat', { draft: 'synthetic unfinished reply' });
  const before = server.count('system');
  server.restart(); h.dispatch('focus');
  await until(h, () => server.count('system') >= before + 2 && h.value.session.status === 'connected');
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.value.onAuthFailure, callback);
  assert.equal(h.value.onSystemUpdate, update);
  assert.equal(h.aiDrafts, drafts);
  assert.equal(drafts.size, 1);
  sameStorage(h, handle.slice(7));
  h.unmount(); groups++;
}

// Exact API URL is the storage binding; only signed service audience normalizes
// localhost. A copied descriptor from another frontend origin is not usable.
{
  const h = setup();
  const paired = await h.auth.pairDevice('http://localhost:17890', MASTER, new AbortController().signal);
  assert.equal(await h.auth.loadDeviceSession(SERVICE), null);
  assert.equal((await h.auth.loadDeviceSession('http://localhost:17890')).handle, paired.handle);
  assert.equal(h.idb.values.get('http://localhost:17890').service_url, 'http://localhost:17890');
  assert.equal(h.idb.values.get('http://localhost:17890').service_origin, SERVICE);
  assert.equal(h.server.verifiedMessages[0].split('\n')[1], SERVICE);
  const other = setup({ idb: h.idb, server: h.server, origin: 'http://localhost:1420' });
  await assert.rejects(other.auth.loadDeviceSession('http://localhost:17890'), (e) => e.code === 'DEVICE_AUTH_CONTEXT');
  assert.equal(h.idb.values.size, 1);
  h.unmount(); other.unmount(); groups++;
}

// Do not sign untrusted message/audience/nonce/expiry shapes even with a valid
// local key. Each rejected challenge retains its descriptor for manual recovery.
{
  const seed = setup();
  const handle = await pair(seed); seed.unmount();
  const changes = [
    (v) => ({ ...v, message: `${v.message}\n` }),
    (v) => ({ ...v, message: v.message.replace(SERVICE, 'http://localhost:17890') }),
    (v) => ({ ...v, message: v.message.replace(FRONTEND, 'http://localhost:1420') }),
    (v) => ({ ...v, nonce: `${v.nonce}=` }),
    (v) => ({ ...v, challenge_id: 'a'.repeat(64) }),
    (v) => ({ ...v, expires_at: new Date(Date.now() - 1000).toISOString() }),
    (v) => ({ ...v, expires_at: new Date(Date.now() + 10 * 60_000).toISOString() }),
  ];
  for (const change of changes) {
    seed.server.challengeResponse = change;
    const h = setup({ idb: seed.idb, server: seed.server });
    const count = seed.server.count('auth/sessions');
    assert.equal(await retainedError(await business(h, handle)), 'DEVICE_AUTH_INVALID_RESPONSE');
    assert.equal(seed.server.count('auth/sessions'), count, 'invalid challenge must not be signed/submitted');
    assert.equal(seed.idb.values.has(SERVICE), true);
    h.unmount();
  }
  seed.server.challengeResponse = null; groups++;
}

// Concurrent readers/writers share a single proof. Aborting one consumer does
// not cancel the other consumers' shared credential acquisition.
{
  const seed = setup(), handle = await pair(seed); seed.unmount();
  const h = setup({ idb: seed.idb, server: seed.server }), gate = deferred();
  const before = h.server.count('auth/sessions'), challenges = h.server.count('auth/challenges');
  h.server.before = (r) => r.path === '/api/v1/auth/sessions' ? gate.promise : undefined;
  const controller = new AbortController();
  const canceled = business(h, handle, 'GET', 'synthetic-canceled', controller.signal);
  const canceledCheck = assert.rejects(canceled, (e) => e.name === 'AbortError');
  const work = [business(h, handle), business(h, handle), business(h, handle, 'POST', 'synthetic-write')];
  await until(h, () => h.server.count('auth/sessions') === before + 1, 'singleflight proof did not start', false);
  // Do not mount the hook during the gate wait: it is another legitimate read
  // consumer, but would obscure this group's precise dispatch count.
  controller.abort(); await canceledCheck;
  assert.equal(h.server.requests.filter((r) => r.path === '/api/v1/auth/sessions').at(-1).options.signal.aborted, false);
  gate.resolve(undefined);
  const responses = await Promise.all(work);
  assert.ok(responses.every((r) => r.status === 200));
  assert.equal(h.server.count('auth/sessions'), before + 1);
  assert.equal(h.server.count('auth/challenges'), challenges + 1);
  assert.equal(h.server.count('synthetic-canceled'), 0);
  h.server.before = null; h.unmount(); groups++;
}

// A late old-token 401 reuses the newer access, not another renewal. The short
// tokens change while the public UI handle is stable.
{
  const h = setup(), handle = await pair(h), gate = deferred();
  const old = [...h.server.access.keys()][0], sessions = h.server.count('auth/sessions');
  h.server.before = (r) => {
    if (r.path === '/api/v1/synthetic-late' && r.headers.get('Authorization') === `Bearer ${old}`) return gate.promise;
    if (r.path === '/api/v1/synthetic-current' && r.headers.get('Authorization') === `Bearer ${old}`) return unauthorized();
  };
  const late = business(h, handle, 'GET', 'synthetic-late');
  await until(h, () => h.server.count('synthetic-late') === 1);
  assert.equal((await business(h, handle, 'GET', 'synthetic-current')).status, 200);
  gate.resolve(unauthorized());
  assert.equal((await late).status, 200);
  assert.equal(h.server.count('auth/sessions'), sessions + 1);
  const latest = [...h.server.access.keys()].at(-1);
  assert.notEqual(latest, old);
  assert.equal(h.server.requests.filter((r) => r.path === '/api/v1/synthetic-late').at(-1).headers.get('Authorization'), `Bearer ${latest}`);
  assert.equal(h.value.activeToken, handle);
  h.server.before = null; h.unmount(); groups++;
}

// GET/HEAD retry at most once. POST/PATCH/DELETE bodies are never replayed, even
// after successful renewal; callers get explicit refresh-and-manual-retry 409.
{
  const h = setup(), handle = await pair(h);
  for (const method of ['POST', 'PATCH', 'DELETE']) {
    h.server.restart();
    const before = h.server.count('synthetic-write', method);
    const response = await business(h, handle, method, 'synthetic-write');
    assert.equal(response.status, 409);
    assert.equal((await response.json()).error.code, 'AUTH_RETRY_REQUIRED');
    assert.equal(h.server.count('synthetic-write', method), before + 1);
  }
  for (const method of ['GET', 'HEAD']) {
    h.server.before = (r) => r.path === '/api/v1/synthetic-read' ? unauthorized() : undefined;
    const before = h.server.count('synthetic-read', method);
    const response = await business(h, handle, method, 'synthetic-read');
    assert.equal(response.status, 503, 'a repeated generic read 401 is not proof of grant revocation');
    assert.equal((await response.json()).error.code, 'DEVICE_AUTH_UNAVAILABLE');
    assert.equal(h.server.count('synthetic-read', method), before + 2);
  }
  assert.match(h.auth.authErrorMessage('AUTH_RETRY_REQUIRED', ''), /未自动重放/);
  h.server.before = null; h.unmount(); groups++;
}

// Service/network/rate-limit/server failures retain stable identity, key and
// drafts. Only definitive device 401 removes them.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7);
  h.aiDrafts.set('synthetic-chat', { draft: 'synthetic retained draft' });
  h.server.restart();
  for (const failure of ['offline', 429, 500, 503]) {
    h.server.before = (r) => {
      if (r.path !== '/api/v1/auth/challenges') return;
      if (failure === 'offline') throw new TypeError('synthetic offline');
      return json(failure, { error: { code: 'TEMPORARY', message: 'private upstream detail' } });
    };
    const before = h.server.count('auth/challenges');
    h.dispatch('focus');
    await until(h, () => h.server.count('auth/challenges') > before && h.value.session.status === 'error' && h.value.session.error);
    assert.equal(h.value.activeToken, handle);
    assert.equal(h.aiDrafts.size, 1);
    sameStorage(h, id);
    assert.ok(!h.value.session.error.includes('private'));
    // Let finally clear the metadata-flight guard before the next focus.
    await nextTurn(); await h.flush();
  }
  h.server.before = null; h.dispatch('focus');
  await until(h, () => h.value.session.status === 'connected');
  assert.equal(h.aiDrafts.size, 1);
  h.server.grants.get(id).revoked = true; h.server.restart(); h.dispatch('focus');
  await until(h, () => !h.value.activeToken && !h.idb.values.has(SERVICE));
  assert.equal(h.aiDrafts.size, 0);
  assert.equal(h.sessions.loadServiceSession(SERVICE).token, null);
  assert.match(h.value.session.error, /本机授权已失效/);
  h.unmount(); groups++;
}

// Opt-out, capability/storage denial and older services still require a manual
// successful master check. Their fallback is page memory, never SS/localStorage.
{
  const cases = [
    { name: 'explicit opt-out', remember: false },
    { name: 'insecure browser context', secure: false },
    { name: 'missing WebCrypto', crypto: null },
    { name: 'private/denied IDB', storageFailure: 'denied' },
    { name: 'failed CryptoKey structured-clone', storageFailure: 'failClone' },
    { name: 'old backend 404', backend: 404 },
    { name: 'unavailable backend 503', backend: 503 },
  ];
  for (const variant of cases) {
    const idb = indexedDBHarness();
    if (variant.storageFailure) idb[variant.storageFailure] = true;
    const h = setup({ ...variant, idb });
    if (variant.backend) h.server.before = (r) => r.path === '/api/v1/auth/devices'
      ? json(variant.backend, { error: { code: 'UNSUPPORTED' } }) : undefined;
    assert.equal(await pair(h, variant.remember !== false), MASTER, variant.name);
    assert.equal(h.value.session.status, 'connected');
    assert.equal(h.value.remembered, false);
    assert.match(h.value.persistenceNotice, /当前页面.*刷新后需重新连接/);
    assert.equal(h.server.count('auth/devices', 'POST'), variant.backend ? 1 : 0);
    assert.equal(idb.values.size, 0);
    assert.equal(h.storage.values.size, 0);
    assert.equal(h.storage.reads.length, 0);
    assert.equal(h.storage.writes.length, 0);
    assert.equal(h.localStorageReads, 0);
    h.unmount();
    const before = h.server.requests.length;
    const reload = setup({ idb, server: h.server, storage: h.storage, ...variant });
    await reload.flush();
    assert.equal(reload.value.activeToken, '');
    assert.equal(reload.value.tokenDraft, '');
    assert.equal(h.server.requests.length, before, 'a new page must never restore the fallback master');
    reload.unmount();
  }
  for (const atPairing of [false, true]) {
    const h = setup();
    h.server.before = (r) => r.path === (atPairing ? '/api/v1/auth/devices' : '/api/v1/system')
      ? unauthorized() : undefined;
    assert.equal(await pair(h), '');
    assert.equal(h.value.session.status, 'error');
    assert.equal(h.value.persistenceNotice, null, 'master 401 must never become a verified fallback');
    assert.equal(h.sessions.loadServiceSession(SERVICE).token, null);
    assert.equal(h.idb.values.size, 0);
    assert.equal(h.storage.writes.length, 0);
    h.unmount();
  }
  groups++;
}

// A valid legacy master/public handle/access-looking value is discarded, not
// migrated or auto-authorized. An old record never causes a 90-day enrollment.
{
  const key = 'social-intelligence.service-session.v1';
  for (const token of [MASTER, `device:${rawurl(16)}`, `synthetic-access-${rawurl(32)}`]) {
    const storage = syntheticStorage(new Map([[key, JSON.stringify({ version: 1, service_url: SERVICE, token })]]));
    const h = setup({ storage });
    assert.equal(storage.values.size, 0, 'legacy cleanup is immediate, before React mounts');
    await h.flush();
    assert.equal(h.server.requests.length, 0);
    assert.equal(h.idb.values.size, 0);
    assert.equal(h.value.activeToken, '');
    assert.equal(h.value.tokenDraft, '');
    assert.equal(h.value.rememberDevice, true);
    assert.equal(storage.reads.length, 0);
    assert.equal(storage.writes.length, 0);
    h.unmount();
  }
  groups++;
}

// Re-pairing safely replaces the old grant and compare-ID cleanup preserves
// the new descriptor. More than the backend's 32-grant cap can be replaced.
{
  const h = setup();
  let previous = await pair(h);
  const staleFailure = h.value.onAuthFailure, staleUpdate = h.value.onSystemUpdate;
  for (let i = 0; i < 34; i++) {
    const next = await pair(h);
    assert.ok(next.startsWith('device:'));
    assert.notEqual(next, previous);
    await until(h, () => [...h.server.grants.values()].filter((g) => !g.revoked).length === 1);
    assert.equal(h.server.grants.get(previous.slice(7)).revoked, true);
    assert.equal(h.idb.values.get(SERVICE).device_id, next.slice(7));
    await assert.rejects(business(h, previous), (e) => e.name === 'AbortError');
    previous = next;
  }
  assert.equal(h.server.count('auth/devices', 'POST'), 35);
  staleFailure(); staleUpdate(system('stale-replaced-device')); await h.flush();
  assert.equal(h.value.activeToken, previous);
  assert.notEqual(h.value.session.system.version, 'stale-replaced-device');
  sameStorage(h, previous.slice(7));
  h.unmount(); groups++;
}

// Known-ID enrollment failures are rolled back with the master. Bad proof or
// failed descriptor commit must not overwrite a previously working key.
{
  const h = setup(), old = await pair(h), oldID = old.slice(7);
  const variants = ['invalid audience', 'failed first proof', 'failed IDB commit'];
  for (const variant of variants) {
    h.server.pairResponse = variant === 'invalid audience' ? (v) => ({ ...v, frontend_origin: 'https://untrusted.invalid' }) : null;
    h.server.sessionResponse = variant === 'failed first proof' ? (v) => ({ ...v, access_token: 'unsafe\naccess' }) : null;
    h.idb.failDescriptorPut = variant === 'failed IDB commit';
    const count = h.server.grants.size;
    await assert.rejects(h.auth.pairDevice(SERVICE, MASTER, new AbortController().signal));
    await until(h, () => [...h.server.grants.values()].filter((g) => !g.revoked).length === 1);
    assert.equal(h.server.grants.size, count + 1, 'one enrollment attempt, not an unsafe retry');
    assert.equal(h.server.grants.get(oldID).revoked, false);
    assert.equal(h.idb.values.get(SERVICE).device_id, oldID, variant);
    assert.equal((await business(h, old)).status, 200);
    const cleanup = h.server.requests.filter((r) => r.method === 'DELETE').at(-1);
    assert.equal(cleanup.headers.get('Authorization'), `Bearer ${MASTER}`, 'known-ID rollback uses the enrollment master');
    assert.notEqual(cleanup.path.split('/').at(-1), oldID);
    assert.equal(h.idb.values.size, 1, 'temporary probe is removed on every failure');
  }
  h.server.pairResponse = null; h.server.sessionResponse = null; h.idb.failDescriptorPut = false;
  h.unmount();
  // The response can be lost after the server creates the grant, before its ID
  // is known locally. This unavoidable orphan is bounded by expiry/rotation,
  // not falsely claimed to have been revoked or automatically retried.
  const lost = setup();
  lost.server.pairResponse = () => { throw new TypeError('synthetic lost enrollment response'); };
  assert.equal(await pair(lost), MASTER);
  assert.equal(lost.server.count('auth/devices', 'POST'), 1);
  assert.equal(lost.server.grants.size, 1);
  assert.equal([...lost.server.grants.values()][0].revoked, false);
  assert.equal(lost.idb.values.size, 0);
  assert.equal(lost.server.requests.filter((r) => r.method === 'DELETE').length, 0);
  assert.match(lost.value.persistenceNotice, /未能记住/);
  assert.equal(lost.storage.writes.length, 0);
  lost.unmount(); groups++;
}

// Forget without a cached access token performs a proof solely to revoke;
// it never re-enables business access or persists a replacement bearer.
{
  const seed = setup(), handle = await pair(seed), id = handle.slice(7);
  seed.unmount(); seed.server.restart();
  const h = setup({ idb: seed.idb, server: seed.server }), before = h.server.count('auth/sessions');
  const result = await h.auth.forgetDevice(SERVICE, handle);
  assert.equal(result.forgotten, true);
  assert.equal(result.revoked, true);
  assert.equal(h.server.count('auth/sessions'), before + 1);
  assert.equal(h.server.grants.get(id).revoked, true);
  assert.equal(h.idb.values.size, 0);
  assert.equal(h.server.requests.at(-1).method, 'DELETE');
  assert.ok(!h.server.requests.at(-1).headers.get('Authorization').includes(MASTER));
  const businessBefore = h.server.count('synthetic-after-forget');
  await assert.rejects(business(h, handle, 'POST', 'synthetic-after-forget'), (e) => e.name === 'AbortError');
  assert.equal(h.server.count('synthetic-after-forget'), businessBefore);
  assert.equal(h.storage.writes.length, 0);
  h.unmount(); groups++;
}

// Forget is locally immediate, blocks reconnect until cleanup settles, and is
// truthful about offline remote revoke and denied local deletion.
{
  for (const failure of ['none', 'offline', 'delete denied']) {
    const h = setup(), handle = await pair(h), id = handle.slice(7), gate = deferred();
    const staleFailure = h.value.onAuthFailure, staleUpdate = h.value.onSystemUpdate;
    h.aiDrafts.set('synthetic-chat', { draft: 'synthetic draft removed on explicit forget' });
    if (failure === 'none') h.server.before = (r) => r.method === 'DELETE' ? gate.promise : undefined;
    if (failure === 'offline') h.server.before = () => { throw new TypeError('synthetic offline'); };
    if (failure === 'delete denied') h.idb.writeDenied = true;
    const clear = h.value.clear(); await h.flush();
    assert.equal(h.value.activeToken, '');
    assert.equal(h.value.tokenDraft, '');
    assert.equal(h.aiDrafts.size, 0);
    assert.equal(h.sessions.loadServiceSession(SERVICE).token, null);
    if (failure === 'none') {
      assert.equal(h.value.forgetting, true);
      h.value.setTokenDraft(MASTER); await h.flush();
      const count = h.server.count('system');
      await h.value.connect({ preventDefault() {} }); await h.flush();
      assert.equal(h.server.count('system'), count, 'reconnect cannot race a forget in progress');
      h.value.setTokenDraft('');
      gate.resolve(undefined);
    }
    await clear; await h.flush();
    assert.equal(h.value.forgetting, false);
    assert.equal(h.value.activeToken, '');
    assert.equal(h.idb.values.has(SERVICE), failure === 'delete denied');
    if (failure === 'none') assert.match(h.value.persistenceNotice, /确认后台授权已撤销/);
    if (failure === 'offline') assert.match(h.value.persistenceNotice, /未能确认后台授权撤销/);
    if (failure === 'delete denied') assert.match(h.value.persistenceNotice, /无法确认本机密钥已删除/);
    assert.equal(h.server.grants.get(id).revoked, failure !== 'offline');
    staleFailure(); staleUpdate(system('stale-forgotten-device')); h.dispatch('focus'); await h.flush();
    assert.equal(h.value.activeToken, '');
    assert.equal(h.value.session.system, null);
    await assert.rejects(business(h, handle, 'POST', 'synthetic-after-forget'), (e) => e.name === 'AbortError');
    assert.equal(h.server.count('synthetic-after-forget'), 0);
    assert.equal(h.storage.writes.length, 0);
    h.unmount();
  }
  groups++;
}

// Clearing a pending shared renewal wins even if fetch ignores abort. A write
// queued behind that renewal never reaches the service and late proof cannot
// recreate a key, memory access, session or draft.
{
  const h = setup(), handle = await pair(h), gate = deferred();
  h.server.restart();
  let held;
  h.server.before = (r) => {
    if (r.path === '/api/v1/auth/sessions' && !held) { held = r; return gate.promise; }
  };
  const read = business(h, handle, 'GET', 'synthetic-pending-read').catch((e) => e);
  await until(h, () => Boolean(held));
  const write = business(h, handle, 'POST', 'synthetic-queued-write').catch((e) => e);
  await h.flush();
  const clear = h.value.clear(); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(held.options.signal.aborted, true);
  await clear;
  assert.equal(h.idb.values.size, 0);
  gate.resolve(undefined);
  for (const result of await Promise.all([read, write])) assert.equal(result.name, 'AbortError');
  assert.equal(h.server.count('synthetic-queued-write'), 0);
  assert.equal(h.server.count('synthetic-pending-read'), 1, 'only the original rejected read was dispatched');
  h.dispatch('focus'); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.value.session.system, null);
  assert.equal(h.idb.values.size, 0);
  await assert.rejects(business(h, handle), (e) => e.name === 'AbortError');
  h.unmount(); groups++;
}

// A read already dispatched as the single allowed retry is also epoch-guarded
// after its response. Late success cannot escape a concurrent explicit forget.
{
  const h = setup(), handle = await pair(h), gate = deferred();
  const old = [...h.server.access.keys()][0];
  h.server.restart();
  let held;
  h.server.before = (r) => {
    if (r.path === '/api/v1/synthetic-retried-read' && r.headers.get('Authorization') !== `Bearer ${old}`) {
      held = r; return gate.promise;
    }
  };
  const request = business(h, handle, 'GET', 'synthetic-retried-read');
  const check = assert.rejects(request, (e) => e.name === 'AbortError');
  await until(h, () => Boolean(held));
  await h.value.clear();
  gate.resolve(json(200, system('late-retried-response')));
  await check; await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.value.session.system, null);
  assert.equal(h.idb.values.size, 0);
  h.unmount(); groups++;
}

// Optional BroadcastChannel is a fast UI invalidation, not the security gate.
// Even without it (and with remote revoke unavailable), deleting/replacing the
// shared IDB row while another tab renews blocks its queued mutation dispatch.
{
  for (const { action, channel } of [
    { action: 'delete', channel: false },
    { action: 'replace', channel: false },
    { action: 'delete', channel: true },
  ]) {
    const a = setup({ channel }), old = await pair(a), gate = deferred();
    const b = setup({ idb: a.idb, server: a.server, broadcasts: a.broadcasts, channel });
    await until(b, () => b.value.session.status === 'connected');
    b.aiDrafts.set('synthetic-chat', { draft: 'synthetic other-tab draft' });
    a.server.restart();
    let held;
    a.server.before = (r) => {
      if (r.method === 'DELETE') return json(503, { error: { code: 'TEMPORARY' } });
      if (r.path === '/api/v1/auth/sessions' && !held) { held = r; return gate.promise; }
    };
    const read = business(b, old, 'GET', 'synthetic-peer-read').catch((e) => e);
    await until(b, () => Boolean(held));
    const write = business(b, old, 'PATCH', 'synthetic-peer-write').catch((e) => e);
    await b.flush();
    let replacement = '';
    if (action === 'delete') {
      await a.value.clear(); await a.flush();
      assert.match(a.value.persistenceNotice, /未能确认后台授权撤销/);
      assert.equal(a.idb.values.size, 0);
    } else {
      replacement = await pair(a);
      assert.notEqual(replacement, old);
      assert.equal(a.idb.values.get(SERVICE).device_id, replacement.slice(7));
    }
    assert.equal(a.server.grants.get(old.slice(7)).revoked, false, 'test must not rely on successful remote revoke');
    if (channel) {
      await until(b, () => !b.value.activeToken);
      assert.equal(b.aiDrafts.size, 0);
      assert.equal(held.options.signal.aborted, true);
    } else {
      assert.equal(b.value.activeToken, old, 'without BC, the transport/next metadata check enforces invalidation');
      assert.equal(a.broadcasts.messages.length, 0);
    }
    gate.resolve(undefined);
    for (const result of await Promise.all([read, write])) {
      if (channel) assert.equal(result.name, 'AbortError');
      else assert.equal(result.status, 401, 'post-proof IDB reread rejects a deleted/replaced descriptor');
    }
    assert.equal(a.server.count('synthetic-peer-write'), 0);
    b.value.onAuthFailure(); await b.flush();
    assert.equal(b.value.activeToken, '');
    assert.equal(b.aiDrafts.size, 0);
    if (replacement) {
      assert.equal(a.idb.values.get(SERVICE).device_id, replacement.slice(7), 'stale tab cleanup cannot erase the replacement row');
      assert.equal((await business(a, replacement)).status, 200);
      assert.equal(a.value.activeToken, replacement);
    }
    for (const message of a.broadcasts.messages) {
      assert.deepEqual(Object.keys(message).sort(), ['handle', 'service_url', 'type']);
      assert.ok(!JSON.stringify(message).includes(MASTER));
    }
    a.unmount(); b.unmount();
  }
  groups++;
}

// Clear wins a pending IndexedDB restore. StrictMode effect replay and stale
// unmounted callbacks cannot erase a valid device or revive a cleared session.
{
  const seed = setup(), handle = await pair(seed); seed.unmount();
  const h = setup({ idb: seed.idb, server: seed.server }), gate = deferred();
  let blocked = false;
  h.idb.beforeGet = (key) => {
    if (key === SERVICE && !blocked) { blocked = true; return gate.promise; }
  };
  const before = h.server.count('system');
  await until(h, () => blocked);
  const clear = h.value.clear(); await h.flush();
  assert.equal(h.value.activeToken, '');
  gate.resolve(); await clear; await h.flush();
  assert.equal(h.idb.values.size, 0);
  assert.equal(h.server.count('system'), before, 'abandoned restore must not dispatch a metadata request');
  h.dispatch('focus'); await h.flush();
  assert.equal(h.value.activeToken, '');
  h.unmount();

  const second = setup(), strictHandle = await pair(second); second.unmount();
  const strict = setup({ idb: second.idb, server: second.server }), late = deferred();
  let held;
  strict.server.before = (r) => {
    if (r.path === '/api/v1/system' && !held) { held = r; return late.promise; }
  };
  await until(strict, () => Boolean(held));
  strict.strictReplayEffects();
  await until(strict, () => strict.value.session.status === 'connected');
  assert.equal(held.options.signal.aborted, true);
  assert.equal(strict.value.activeToken, strictHandle);
  late.resolve(unauthorized()); await nextTurn(); await strict.flush();
  assert.equal(strict.value.activeToken, strictHandle);
  assert.equal(strict.idb.values.has(SERVICE), true);
  const staleFailure = strict.value.onAuthFailure, staleUpdate = strict.value.onSystemUpdate;
  strict.unmount();
  const current = setup({ idb: strict.idb, server: strict.server });
  await until(current, () => current.value.session.status === 'connected');
  staleFailure(); staleUpdate(system('unmounted-device-callback')); await current.flush();
  assert.equal(strict.postUnmountStateWrites, 0);
  assert.equal(current.value.activeToken, strictHandle);
  assert.notEqual(current.value.session.system.version, 'unmounted-device-callback');
  current.unmount(); groups++;
}

// Cancel/clear during the first possession proof or the post-commit reread
// must not install a late session/key. Every known new ID gets rollback DELETE.
{
  for (const stage of ['cancel proof', 'clear proof', 'clear committed descriptor']) {
    const h = setup(), gate = deferred();
    let blocked = false;
    if (stage.includes('proof')) h.server.before = (r) => {
      if (r.path === '/api/v1/auth/sessions' && !blocked) { blocked = true; return gate.promise; }
    };
    else h.idb.beforeGet = (key) => {
      if (key === SERVICE && h.idb.values.has(SERVICE) && !blocked) { blocked = true; return gate.promise; }
    };
    await h.flush(); h.value.setTokenDraft(MASTER); await h.flush();
    const pairing = h.value.connect({ preventDefault() {} });
    await until(h, () => blocked);
    const stop = stage.startsWith('cancel') ? h.value.cancel() : h.value.clear();
    await h.flush();
    assert.equal(h.value.activeToken, '');
    gate.resolve(undefined);
    await Promise.all([pairing, stop]);
    await until(h, () => h.idb.values.size === 0 && [...h.server.grants.values()].every((g) => g.revoked));
    assert.equal(h.value.activeToken, '');
    assert.equal(h.value.session.system, null);
    assert.equal(h.sessions.loadServiceSession(SERVICE).token, null);
    assert.equal(h.server.count('auth/devices', 'POST'), 1);
    assert.equal(h.storage.writes.length, 0);
    h.unmount();
  }
  groups++;
}

// Malformed session responses, denied reads and timeouts are transient: no
// invalid token is cached, but an existing key/draft is not silently discarded.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7);
  h.aiDrafts.set('synthetic-chat', { draft: 'synthetic preserved draft' });
  const callback = h.value.onAuthFailure;
  for (const mutate of [
    (v) => ({ ...v, access_token: '' }),
    (v) => ({ ...v, access_token: { token: 'synthetic-not-a-string' } }),
    (v) => ({ ...v, access_token: handle }),
    (v) => ({ ...v, access_token: `unsafe\n${v.access_token}` }),
    (v) => ({ ...v, expires_at: new Date(Date.now() - 1_000).toISOString() }),
    (v) => ({ ...v, expires_at: new Date(Date.now() + 17 * 60_000).toISOString() }),
    (v) => ({ ...v, expires_at: 'not-a-date' }),
  ]) {
    h.server.sessionResponse = mutate; h.server.restart();
    const before = h.server.count('auth/sessions');
    h.dispatch('focus');
    await until(h, () => h.server.count('auth/sessions') > before && h.value.session.error);
    assert.equal(h.value.activeToken, handle);
    assert.equal(h.value.session.status, 'error');
    assert.equal(h.value.onAuthFailure, callback);
    assert.equal(h.aiDrafts.size, 1);
    assert.equal(h.idb.values.get(SERVICE).device_id, id);
    assert.match(h.value.session.error, /连接信息已保留/);
    await nextTurn(); await h.flush();
  }
  h.server.sessionResponse = null; h.idb.readDenied = true;
  const businessBefore = h.server.count('system');
  h.dispatch('focus');
  await nextTurn(); await h.flush();
  assert.equal(h.value.session.status, 'error');
  assert.match(h.value.session.error, /连接信息已保留/);
  assert.equal(h.server.count('system'), businessBefore, 'denied descriptor read blocks even cached access dispatch');
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.aiDrafts.size, 1);
  h.idb.readDenied = false; h.server.restart();
  const gate = deferred(); let held;
  h.server.before = (r) => {
    if (r.path === '/api/v1/auth/challenges' && !held) { held = r; return gate.promise; }
  };
  h.dispatch('focus'); await until(h, () => Boolean(held));
  const timeout = [...h.timers.values()].filter((timer) => timer.ms === 10_000).at(-1);
  assert.ok(timeout, 'shipped auth HTTP request installs its bounded timeout');
  timeout.callback(); gate.resolve(undefined);
  await until(h, () => h.value.session.error);
  assert.equal(held.options.signal.aborted, true);
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.aiDrafts.size, 1);
  assert.equal(h.idb.values.get(SERVICE).device_id, id);
  h.server.before = null; h.dispatch('focus');
  await until(h, () => h.value.session.status === 'connected');
  assert.equal(h.value.onAuthFailure, callback);
  assert.equal(h.server.count('auth/devices', 'POST'), 1);
  sameStorage(h, id);
  h.unmount(); groups++;
}

// Expired grants require fresh manual authorization. Tampered durable metadata
// or a non-private/non-key object never reaches challenge/sign/business fetch.
{
  const seed = setup(), handle = await pair(seed); seed.unmount();
  const descriptor = seed.idb.values.get(SERVICE);
  for (const mutate of [
    (v) => ({ ...v, version: 2 }),
    (v) => ({ ...v, device_id: `${'A'.repeat(21)}B` }),
    (v) => ({ ...v, service_url: 'http://localhost:17890' }),
    (v) => ({ ...v, service_origin: 'http://localhost:17890' }),
    (v) => ({ ...v, frontend_origin: 'null' }),
    (v) => ({ ...v, expires_at: 'not-a-date' }),
    (v) => ({ ...v, private_key: {} }),
  ]) {
    const idb = indexedDBHarness(); idb.values.set(SERVICE, mutate(descriptor));
    const h = setup({ idb, server: seed.server }), before = h.server.requests.length;
    await assert.rejects(h.auth.loadDeviceSession(SERVICE), (e) => [401, 403].includes(e.status));
    assert.equal(h.server.requests.length, before);
    h.unmount();
  }
  seed.idb.values.set(SERVICE, { ...descriptor, expires_at: new Date(Date.now() - 1_000).toISOString() });
  const expired = setup({ idb: seed.idb, server: seed.server }), before = seed.server.requests.length;
  await expired.flush(); await until(expired, () => !expired.idb.values.has(SERVICE));
  assert.equal(expired.value.activeToken, '');
  assert.equal(expired.value.tokenDraft, '');
  assert.equal(seed.server.requests.length, before, 'expired local grant is never auto-renewed or auto-paired');
  assert.equal(expired.sessions.loadServiceSession(SERVICE).token, null);
  expired.unmount(); groups++;
}

// All nine shipped domain clients execute through the real device transport,
// not a replacement auth stub. Each mutation gets one rejected dispatch and a
// recovered access token, but reports an explicit 409 instead of replaying.
{
  const h = setup(), handle = await pair(h), signal = new AbortController().signal;
  const cases = [
    ['ai-api.ts', 'analyzeConversation', [handle, signal, 'synthetic-conversation', 'synthetic-context-hash'], 'POST'],
    ['inbox-api.ts', 'importMessages', [handle, signal, { platform: 'qq', connection_id: 'synthetic', messages: [] }], 'POST'],
    ['model-config-api.ts', 'saveModelConfig', [handle, signal, { expected_revision: 0, base_url: 'https://synthetic.invalid/v1', model: 'synthetic-model', api_key: 'synthetic-model-secret', daily_limit: 1, confirmed: true }], 'PATCH'],
    ['digest-api.ts', 'runDigestNow', [handle], 'POST'],
    ['email-api.ts', 'syncEmail', [handle, signal], 'POST'],
    ['wechat-api.ts', 'saveWechatMonitor', [handle, signal, { enabled: true, peer_ids: ['synthetic-peer'], interval_seconds: 10 }], 'POST'],
    ['milky-api.ts', 'syncMilky', [handle, signal, '12345', 1], 'POST'],
    ['profile-api.ts', 'generateProfile', [handle, signal, 'synthetic-conversation', { expected_revision: 0, confirmed: true, model_config_id: 'synthetic-model' }], 'POST'],
    ['profile-links-api.ts', 'confirmProfileLink', [handle, signal, 'synthetic-conversation', 'synthetic-target'], 'POST'],
  ];
  for (const [file, name, args, method] of cases) {
    const client = h.evaluate(file, [name]);
    h.server.restart();
    const before = h.server.requests.length, sessions = h.server.count('auth/sessions');
    await assert.rejects(client[name](...args), (e) => e instanceof h.api.ServiceError && e.code === 'AUTH_RETRY_REQUIRED' && e.status === 409 && /未自动重放/.test(e.message));
    const dispatched = h.server.requests.slice(before).filter((r) => !r.path.startsWith('/api/v1/auth/'));
    assert.equal(dispatched.length, 1, `${file} must never replay its domain mutation`);
    assert.equal(dispatched[0].method, method);
    assert.notEqual(dispatched[0].headers.get('Authorization'), `Bearer ${handle}`);
    assert.notEqual(dispatched[0].headers.get('Authorization'), `Bearer ${MASTER}`);
    assert.equal(h.server.count('auth/sessions'), sessions + 1);
  }
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.value.remembered, true);
  sameStorage(h, handle.slice(7));
  assert.ok(!JSON.stringify([...h.idb.values]).includes('synthetic-model-secret'));
  h.unmount(); groups++;
}

// Actual Settings JSX and actual App navigation keep the public React keys,
// callbacks and AI buffer stable across access renewal. Password UI never shows
// a durable bearer; expiry, default trust, page-only fallback and forget states
// are tested from rendered controls, not source-string assertions.
{
  const h = setup({ mode: 'app' }); await h.flush();
  const navigate = (label) => find(h, (n) => n.type === 'button' && n.props['aria-label'] === label).props.onClick();
  navigate('设置'); await h.flush();
  const panel = () => { const element = find(h, (n) => typeof n.type === 'function' && n.type.name === 'Settings'); return element.type(element.props); };
  const control = (predicate) => { const value = nodes(panel()).find(predicate); assert.ok(value, 'actual Settings control missing'); return value; };
  const text = () => nodes(panel()).flatMap((n) => (n.props?.children ?? []).flat(Infinity).filter((value) => typeof value === 'string')).join(' ');
  const password = control((n) => n.type === 'input' && n.props.id === 'service-token');
  assert.equal(password.props.type, 'password');
  assert.equal(password.props.autoComplete, 'off');
  assert.equal(password.props.spellCheck, false);
  assert.equal(password.props['aria-describedby'], 'token-description');
  const checkbox = control((n) => n.type === 'input' && n.props.type === 'checkbox');
  assert.equal(checkbox.props.checked, true);
  assert.equal(checkbox.props['aria-describedby'], 'remember-description');
  assert.match(text(), /可信任的电脑（90 天）/);
  assert.match(text(), /不可导出.*而非主令牌.*共享电脑.*清除站点数据.*更换地址/);
  assert.match(text(), /当前页面内存.*刷新后需重新连接/);
  assert.ok(nodes(panel()).some((n) => n.props?.['aria-live'] === 'polite'));
  checkbox.props.onChange({ target: { checked: false } }); await h.flush();
  assert.equal(settings(h).rememberDevice, false);
  control((n) => n.type === 'input' && n.props.type === 'checkbox').props.onChange({ target: { checked: true } });
  password.props.onChange({ target: { value: MASTER } }); await h.flush();
  await control((n) => n.type === 'form').props.onSubmit({ preventDefault() {} }); await h.flush();
  const handle = settings(h).activeToken, id = handle.slice(7);
  assert.ok(handle.startsWith('device:'));
  assert.equal(settings(h).remembered, true);
  assert.equal(control((n) => n.type === 'input' && n.props.id === 'service-token').props.value, '');
  assert.match(text(), /有效期至/);
  assert.ok(text().includes(new Date(h.idb.values.get(SERVICE).expires_at).toLocaleDateString('zh-CN')));
  assert.ok(!text().includes(MASTER));
  assert.ok(!nodes(panel()).some((n) => n.type === 'input' && n.props.type === 'checkbox'));
  const callback = settings(h).onAuthFailure, update = settings(h).onSystemUpdate;
  assert.equal(control((n) => typeof n.type === 'function' && n.type.name === 'QQSettings').props.key, handle);
  assert.equal(control((n) => n.type === 'MilkyPanelStub').props.key, `milky-${handle}`);
  navigate('消息'); await h.flush();
  const inbox = find(h, (n) => n.type === 'InboxStub'), drafts = inbox.props.aiDrafts;
  assert.equal(inbox.props.key, handle);
  drafts.set('synthetic-chat', { draft: 'synthetic real-App draft' });
  const before = h.server.count('system'); h.server.restart(); h.dispatch('focus'); h.dispatch('visibilitychange');
  await until(h, () => h.server.count('system') >= before + 2);
  assert.equal(find(h, (n) => n.type === 'InboxStub').props.key, handle);
  assert.equal(find(h, (n) => n.type === 'InboxStub').props.aiDrafts, drafts);
  assert.equal(find(h, (n) => n.type === 'InboxStub').props.onAuthFailure, callback);
  navigate('联系人'); await h.flush();
  assert.equal(find(h, (n) => n.type === 'InboxStub').props.key, `profiles:${handle}`);
  assert.equal(find(h, (n) => n.type === 'InboxStub').props.aiDrafts, drafts);
  assert.equal(drafts.size, 1);
  navigate('设置'); await h.flush();
  assert.equal(settings(h).onAuthFailure, callback);
  assert.equal(settings(h).onSystemUpdate, update);
  assert.equal(control((n) => typeof n.type === 'function' && n.type.name === 'QQSettings').props.key, handle);
  assert.equal(control((n) => n.type === 'MilkyPanelStub').props.key, `milky-${handle}`);
  const gate = deferred(); h.server.before = (r) => r.method === 'DELETE' ? gate.promise : undefined;
  const forget = control((n) => n.type === 'button' && n.props.onClick === settings(h).onClear);
  assert.ok(forget.props.children.flat(Infinity).includes('忘记本机并断开'));
  const clearing = forget.props.onClick(); await h.flush();
  assert.equal(settings(h).activeToken, '');
  assert.equal(settings(h).forgetting, true);
  assert.equal(drafts.size, 0);
  assert.equal(control((n) => n.type === 'input' && n.props.id === 'service-token').props.disabled, true);
  assert.equal(control((n) => n.type === 'input' && n.props.type === 'checkbox').props.disabled, true);
  assert.equal(control((n) => n.type === 'button' && n.props.type === 'submit').props.disabled, true);
  assert.match(text(), /正在忘记/);
  gate.resolve(undefined); await clearing; await h.flush();
  assert.match(text(), /确认后台授权已撤销/);
  assert.equal(h.idb.values.size, 0);
  assert.equal(h.storage.writes.length, 0);
  assert.equal(h.storage.reads.length, 0);
  assert.equal(h.localStorageReads, 0);
  h.unmount(); groups++;
}

// A same-master backend restart between challenge and session clears only the
// in-flight proof, not its 90-day grant. Recovery gets one fresh challenge and
// preserves the hook's public identity, callbacks and drafts.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7);
  const descriptor = h.idb.values.get(SERVICE), callback = h.value.onAuthFailure, update = h.value.onSystemUpdate;
  h.aiDrafts.set('synthetic-proof-restart', { draft: 'retain across challenge loss' });
  h.server.restart();
  let restartedDuringProof = false;
  h.server.before = (r) => {
    if (r.path === '/api/v1/auth/sessions' && !restartedDuringProof) {
      restartedDuringProof = true;
      h.server.restart();
    }
  };
  const challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions'), readsBefore = h.server.count('system');
  h.dispatch('focus');
  await until(h, () => restartedDuringProof && h.server.count('system') === readsBefore + 2 && h.value.session.status === 'connected');
  assert.equal(h.server.count('auth/challenges'), challengesBefore + 2);
  assert.equal(h.server.count('auth/sessions'), proofsBefore + 2);
  const proofs = h.server.requests.filter((r) => r.path === '/api/v1/auth/sessions').slice(-2);
  assert.notEqual(proofs[0].body.challenge_id, proofs[1].body.challenge_id, 'retry must fetch a new single-use challenge');
  assert.equal(h.server.count('auth/devices', 'POST'), 1, 'proof recovery must not enroll again or use a master');
  assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0);
  assert.equal(h.server.grants.get(id).revoked, false);
  assert.equal(h.idb.values.get(SERVICE), descriptor, 'no descriptor/key rewrite during proof recovery');
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.value.onAuthFailure, callback);
  assert.equal(h.value.onSystemUpdate, update);
  assert.equal(h.aiDrafts.size, 1);
  sameStorage(h, id);
  h.unmount(); groups++;
}

// Advance both client and service's synthetic wall clock past the real 60-second
// challenge TTL immediately before its session POST (no sleep). Only the proof
// is retried; an already rejected business mutation is never replayed.
{
  const originalNow = Date.now;
  const h = setup(), handle = await pair(h), id = handle.slice(7), descriptor = h.idb.values.get(SERVICE);
  h.aiDrafts.set('synthetic-proof-expiry', { draft: 'retain across expired challenge' });
  let expiredDuringProof = false;
  try {
    h.server.restart();
    h.server.before = (r) => {
      if (r.path === '/api/v1/auth/sessions' && !expiredDuringProof) {
        expiredDuringProof = true;
        const challenge = h.server.challenges.get(JSON.parse(r.options.body).challenge_id);
        assert.ok(Date.parse(challenge.expires_at) > Date.now());
        assert.ok(Date.parse(challenge.expires_at) <= Date.now() + 60_000);
        Date.now = () => originalNow() + 61_000;
        assert.ok(Date.parse(challenge.expires_at) < Date.now(), 'first proof reaches the actual expired-challenge branch');
      }
    };
    const challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions');
    const response = await business(h, handle, 'POST', 'synthetic-expired-proof');
    assert.equal(response.status, 409);
    assert.equal((await response.json()).error.code, 'AUTH_RETRY_REQUIRED');
    assert.equal(h.server.count('synthetic-expired-proof'), 1, 'fresh proof does not authorize automatic mutation replay');
    assert.equal(h.server.count('auth/challenges'), challengesBefore + 2);
    assert.equal(h.server.count('auth/sessions'), proofsBefore + 2);
    assert.equal(h.idb.values.get(SERVICE), descriptor);
    assert.equal(h.server.grants.get(id).revoked, false);
    assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0);
    assert.equal(h.value.activeToken, handle);
    assert.equal(h.aiDrafts.size, 1);
    sameStorage(h, id);
  } finally {
    Date.now = originalNow;
    h.unmount();
  }
  groups++;
}

// Repeated session/proof 401s (even DEVICE_UNAUTHORIZED) are not definitive
// grant evidence. The bounded second failure retains the key and hook drafts,
// makes no business retry, and leaves a later focus able to recover normally.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7), descriptor = h.idb.values.get(SERVICE);
  const callback = h.value.onAuthFailure, update = h.value.onSystemUpdate;
  h.aiDrafts.set('synthetic-proof-rejection', { draft: 'retain after two failed proofs' });
  for (const rejection of [deniedDevice, unauthorized, () => new Response('synthetic non-JSON 401', { status: 401 })]) {
    h.server.restart();
    h.server.before = (r) => r.path === '/api/v1/auth/sessions' ? rejection() : undefined;
    const challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions'), readsBefore = h.server.count('system');
    h.dispatch('focus');
    await until(h, () => h.server.count('auth/sessions') === proofsBefore + 2 && h.value.session.status === 'error');
    await nextTurn(); await h.flush();
    assert.equal(h.server.count('auth/challenges'), challengesBefore + 2);
    assert.equal(h.server.count('auth/sessions'), proofsBefore + 2, 'never loop beyond one renewed challenge');
    assert.equal(h.server.count('system'), readsBefore + 1, 'no read retry until a proof actually succeeds');
    assert.equal(h.idb.values.get(SERVICE), descriptor);
    assert.equal(h.server.grants.get(id).revoked, false);
    assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0);
    assert.equal(h.value.activeToken, handle);
    assert.equal(h.value.remembered, true);
    assert.equal(h.value.onAuthFailure, callback);
    assert.equal(h.value.onSystemUpdate, update);
    assert.equal(h.aiDrafts.size, 1);
    assert.match(h.value.session.error, /连接信息已保留/);
    sameStorage(h, id);
    h.server.before = null; h.dispatch('focus');
    await until(h, () => h.server.count('auth/sessions') === proofsBefore + 3 && h.value.session.status === 'connected');
    assert.equal(h.aiDrafts.size, 1);
    await nextTurn(); await h.flush();
  }
  h.unmount(); groups++;
}

// A generic challenge 401 from a proxy/older backend also cannot establish
// revocation. Only the explicit DEVICE_UNAUTHORIZED challenge code is terminal.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7), descriptor = h.idb.values.get(SERVICE);
  h.aiDrafts.set('synthetic-generic-401', { draft: 'retain when grant status is unknown' });
  for (const rejection of [unauthorized, () => json(401, { error: { code: 'UNKNOWN_AUTH_FAILURE' } }), () => new Response(null, { status: 401 })]) {
    h.server.restart();
    h.server.before = (r) => r.path === '/api/v1/auth/challenges' ? rejection() : undefined;
    const challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions');
    h.dispatch('focus');
    await until(h, () => h.server.count('auth/challenges') === challengesBefore + 1 && h.value.session.status === 'error');
    await nextTurn(); await h.flush();
    assert.equal(h.server.count('auth/sessions'), proofsBefore, 'generic challenge failure must not attempt a proof');
    assert.equal(h.idb.values.get(SERVICE), descriptor);
    assert.equal(h.value.activeToken, handle);
    assert.equal(h.aiDrafts.size, 1);
    assert.match(h.value.session.error, /连接信息已保留/);
    assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0);
    sameStorage(h, id);
    h.server.before = null; h.dispatch('focus');
    await until(h, () => h.value.session.status === 'connected');
    await nextTurn(); await h.flush();
  }
  h.unmount(); groups++;
}

// If a grant really is revoked during a proof, the second challenge explicitly
// rejects it. This definitive evidence still invalidates the hook and deletes
// the key; retrying a proof never weakens actual revocation handling.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7);
  h.aiDrafts.set('synthetic-revoked-during-proof', { draft: 'must clear on proven revocation' });
  h.server.restart();
  h.server.before = (r) => {
    if (r.path === '/api/v1/auth/sessions') {
      h.server.grants.get(id).revoked = true;
      return deniedDevice();
    }
  };
  const challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions');
  h.dispatch('focus');
  await until(h, () => !h.value.activeToken && !h.idb.values.has(SERVICE));
  assert.equal(h.server.count('auth/challenges'), challengesBefore + 2);
  assert.equal(h.server.count('auth/sessions'), proofsBefore + 1);
  assert.equal(h.aiDrafts.size, 0);
  assert.equal(h.sessions.loadServiceSession(SERVICE).token, null);
  assert.match(h.value.session.error, /本机授权已失效/);
  assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0, 'definitive invalidation deletes locally without pretending to revoke again');
  h.unmount(); groups++;
}

// Exercise failed replacement through the actual hook connect path, not just
// pairDevice. A verified page-memory master fallback must not retire an old
// descriptor, revoke its grant, or broadcast-forget a still-connected peer tab.
{
  for (const failure of ['enrollment 503', 'proof 503', 'proof 401 twice', 'descriptor commit denied']) {
    const h = setup(), old = await pair(h), id = old.slice(7), descriptor = h.idb.values.get(SERVICE);
    const peer = setup({ idb: h.idb, server: h.server, broadcasts: h.broadcasts });
    await until(peer, () => peer.value.session.status === 'connected');
    const callback = peer.value.onAuthFailure, update = peer.value.onSystemUpdate;
    peer.aiDrafts.set('synthetic-peer-retained', { draft: 'unaffected by another tab failed re-pair' });
    const before = h.server.requests.length, broadcastsBefore = h.broadcasts.messages.length;
    h.idb.failDescriptorPut = failure === 'descriptor commit denied';
    h.server.before = (r) => {
      if (failure === 'enrollment 503' && r.path === '/api/v1/auth/devices')
        return json(503, { error: { code: 'AUTH_UNAVAILABLE' } });
      if (r.path === '/api/v1/auth/sessions' && JSON.parse(r.options.body).device_id !== id) {
        if (failure === 'proof 503') return json(503, { error: { code: 'AUTH_UNAVAILABLE' } });
        if (failure === 'proof 401 twice') return deniedDevice();
      }
    };
    assert.equal(await pair(h), MASTER, failure);
    await until(h, () => [...h.server.grants.values()].filter((g) => !g.revoked).length === 1);
    await peer.flush();
    assert.equal(h.value.session.status, 'connected');
    assert.equal(h.value.remembered, false);
    assert.match(h.value.persistenceNotice, /本次主令牌仅在当前页面内存中保留/);
    assert.match(h.value.persistenceNotice, /已有本机授权/);
    assert.equal(h.idb.values.get(SERVICE), descriptor, `${failure}: exact old descriptor and nonextractable key remain`);
    assert.equal(h.server.grants.get(id).revoked, false);
    assert.ok(!h.server.requests.slice(before).some((r) => r.method === 'DELETE' && r.path === `/api/v1/auth/devices/${id}`));
    assert.ok(!h.broadcasts.messages.slice(broadcastsBefore).some((m) => m.type === 'forgotten' && m.handle === old));
    assert.equal(peer.value.activeToken, old);
    assert.equal(peer.value.onAuthFailure, callback);
    assert.equal(peer.value.onSystemUpdate, update);
    assert.equal(peer.aiDrafts.size, 1);
    assert.equal((await business(peer, old)).status, 200);
    sameStorage(h, id); sameStorage(peer, id);
    h.server.before = null; h.idb.failDescriptorPut = false;
    h.unmount();
    const enrollmentCount = h.server.count('auth/devices', 'POST');
    const reload = setup({ idb: h.idb, server: h.server, broadcasts: h.broadcasts });
    await until(reload, () => reload.value.session.status === 'connected');
    assert.equal(reload.value.activeToken, old, 'refresh restores the retained old device, never the fallback master');
    assert.equal(reload.value.tokenDraft, '');
    assert.equal(reload.server.count('auth/devices', 'POST'), enrollmentCount);
    sameStorage(reload, id);
    reload.unmount(); peer.unmount();
  }
  // Explicitly unchecking remember remains a deliberate retirement of the old
  // browser grant, including peer notification. Failure fallback is not opt-out.
  const h = setup(), old = await pair(h), id = old.slice(7);
  const peer = setup({ idb: h.idb, server: h.server, broadcasts: h.broadcasts });
  await until(peer, () => peer.value.session.status === 'connected');
  assert.equal(await pair(h, false), MASTER);
  await until(h, () => !h.idb.values.has(SERVICE) && h.server.grants.get(id).revoked);
  await peer.flush();
  assert.equal(peer.value.activeToken, '');
  assert.ok(h.broadcasts.messages.some((m) => m.type === 'forgotten' && m.handle === old));
  peer.unmount(); h.unmount(); groups++;
}

// Even after a successful new proof, a read endpoint/proxy can return generic
// 401 again. It gets no second business retry and cannot erase the valid grant.
// The real API exposes recoverable 503; 403 still passes through unchanged.
{
  const h = setup(), handle = await pair(h), id = handle.slice(7), descriptor = h.idb.values.get(SERVICE);
  const callback = h.value.onAuthFailure, update = h.value.onSystemUpdate;
  h.aiDrafts.set('synthetic-business-401', { draft: 'keep after generic read rejection' });
  h.server.before = (r) => r.path === '/api/v1/system' ? unauthorized() : undefined;
  const readsBefore = h.server.count('system'), challengesBefore = h.server.count('auth/challenges'), proofsBefore = h.server.count('auth/sessions');
  await assert.rejects(h.api.getSystem(handle, new AbortController().signal), (e) => e.status === 503 && e.code === 'DEVICE_AUTH_UNAVAILABLE');
  assert.equal(h.server.count('system'), readsBefore + 2, 'a read gets exactly one business retry');
  assert.equal(h.server.count('auth/challenges'), challengesBefore + 1);
  assert.equal(h.server.count('auth/sessions'), proofsBefore + 1);
  h.dispatch('focus');
  await until(h, () => h.server.count('system') === readsBefore + 4 && h.value.session.status === 'error');
  await nextTurn(); await h.flush();
  assert.equal(h.server.count('system'), readsBefore + 4, 'hook recovery cannot spin on generic 401');
  assert.equal(h.server.count('auth/challenges'), challengesBefore + 3, 'next request reproves before dispatch and renews once after its rejected access');
  assert.equal(h.server.count('auth/sessions'), proofsBefore + 3);
  assert.equal(h.idb.values.get(SERVICE), descriptor);
  assert.equal(h.server.grants.get(id).revoked, false);
  assert.equal(h.server.requests.filter((r) => r.method === 'DELETE').length, 0);
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.value.onAuthFailure, callback);
  assert.equal(h.value.onSystemUpdate, update);
  assert.equal(h.aiDrafts.size, 1);
  assert.match(h.value.session.error, /连接信息已保留/);
  sameStorage(h, id);
  h.server.before = (r) => r.path === '/api/v1/system' ? json(403, { error: { code: 'DEVICE_SCOPE_DENIED' } }) : undefined;
  const beforeForbidden = h.server.count('system');
  const forbidden = await business(h, handle);
  assert.equal(forbidden.status, 403);
  assert.equal((await forbidden.json()).error.code, 'DEVICE_SCOPE_DENIED');
  assert.equal(h.server.count('system'), beforeForbidden + 1, '403 is not retried or remapped');
  h.server.before = null; h.dispatch('focus');
  await until(h, () => h.value.session.status === 'connected');
  assert.equal(h.value.activeToken, handle);
  assert.equal(h.aiDrafts.size, 1);
  h.unmount(); groups++;
}

console.log(`Device auth synthetic checks passed (${groups} groups): real P-256/WebCrypto + nonextractable structured-clone keys, strict audience/proof, restore/restart, one fresh-challenge proof retry with nonterminal ambiguous 401s, singleflight/consumer abort/late-401, stable identity/drafts, bounded read retry, never-replayed writes through all nine domain clients, no Web Storage bearers, safe replacement/rollback including actual hook fallback, truthful forget, clear/cross-tab/StrictMode races and actual App/Settings controls. Browser/Tauri acceptance remains pending.`);
