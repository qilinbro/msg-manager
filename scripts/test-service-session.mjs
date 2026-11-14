import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Execute the shipped storage, connection hook, API client and app with fake
// browser APIs. No real service, browser storage, credentials or chat is used.
const SERVICE_URL = 'http://127.0.0.1:17890';
const TOKEN_A = 'synthetic-service-token-A-0123456789abcdef';
const TOKEN_B = 'synthetic-service-token-B-0123456789abcdef';
const system = (version = 'synthetic') => ({
  version,
  stage: 'unified_inbox',
  workspace_id: 'local-default',
  automation: { paused: true, auto_send_enabled: false },
  storage: { configured: true, driver: 'sqlcipher' },
  connectors: [],
});

function syntheticStorage(initial = new Map()) {
  const values = new Map(initial);
  return {
    values,
    denied: false,
    getItem(key) { if (this.denied) throw Error('synthetic storage denial'); return values.get(key) ?? null; },
    setItem(key, value) { if (this.denied) throw Error('synthetic storage denial'); values.set(key, String(value)); },
    removeItem(key) { if (this.denied) throw Error('synthetic storage denial'); values.delete(key); },
  };
}

function transpile(file, source) {
  const result = transformSync(file, source, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(result.errors.length, 0, `${file} must compile for synthetic execution`);
  return result.code;
}

function sourceFor(file) {
  return readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8')
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^export default /gm, '')
    .replace(/^export /gm, '');
}

function browserHarness({ storage = syntheticStorage(), mode = 'hook', autoReply = system() } = {}) {
  const slots = [];
  let cursor = 0;
  let dirty = true;
  let effects = [];
  let rendered;
  let mounted = true;
  let postUnmountStateWrites = 0;
  let localStorageReads = 0;
  let locationReads = 0;
  const listeners = new Map();
  const timers = new Map();
  const requests = [];
  let nextTimer = 0;
  let responder = autoReply === null ? null : () => ({ status: 200, payload: autoReply });
  const aiDrafts = new Map();
  const sameDeps = (a, b) => Array.isArray(a) && Array.isArray(b) && a.length === b.length && a.every((value, i) => Object.is(value, b[i]));
  const addListener = (event, callback) => { if (!listeners.has(event)) listeners.set(event, new Set()); listeners.get(event).add(callback); };
  const removeListener = (event, callback) => listeners.get(event)?.delete(callback);
  const jsx = (type, props, ...children) => ({ type, props: { ...(props ?? {}), children } });
  const hooks = {
    useState(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = { kind: 'state', value: typeof initial === 'function' ? initial() : initial };
      return [slots[index].value, (value) => {
        if (!mounted) postUnmountStateWrites++;
        const next = typeof value === 'function' ? value(slots[index].value) : value;
        if (!Object.is(next, slots[index].value)) { slots[index].value = next; dirty = true; }
      }];
    },
    useRef(initial) {
      const index = cursor++;
      if (!(index in slots)) slots[index] = { kind: 'ref', value: { current: initial } };
      return slots[index].value;
    },
    useEffect(callback, dependencies) {
      const index = cursor++;
      const previous = slots[index];
      if (!previous || !sameDeps(previous.dependencies, dependencies)) {
        effects.push(() => {
          previous?.cleanup?.();
          slots[index] = { kind: 'effect', dependencies, callback, cleanup: callback() };
        });
      }
    },
    useMemo(factory, dependencies) {
      const index = cursor++;
      if (!slots[index] || !sameDeps(slots[index].dependencies, dependencies)) slots[index] = { kind: 'memo', dependencies, value: factory() };
      return slots[index].value;
    },
    useCallback(callback, dependencies) { return hooks.useMemo(() => callback, dependencies); },
  };
  hooks.useLayoutEffect = hooks.useEffect;
  const fakeWindow = {
    get sessionStorage() { if (storage.denied) throw Error('synthetic storage denial'); return storage; },
    get localStorage() { localStorageReads++; throw Error('Persistent localStorage must not hold a service credential.'); },
    get location() { locationReads++; throw Error('Location must not carry a service credential.'); },
    addEventListener: addListener,
    removeEventListener: removeListener,
    setTimeout(callback, ms) { timers.set(++nextTimer, { callback, ms }); return nextTimer; },
    clearTimeout(id) { timers.delete(id); },
  };
  const fakeDocument = { visibilityState: 'visible', addEventListener: addListener, removeEventListener: removeListener };
  const environment = {
    ...hooks, jsx, Fragment: 'fragment',
    module: { exports: {} },
    window: fakeWindow, document: fakeDocument,
    URL, AbortController, DOMException, Date, Map,
    fetch(url, options) {
      const request = { url, options, settled: false };
      requests.push(request);
      assert.equal(url, `${SERVICE_URL}/api/v1/system`, 'restoring a session must only inspect local system metadata');
      assert.equal(options.method, 'GET', 'restoring or focusing must never replay a mutation');
      assert.equal(options.body, undefined);
      assert.equal(options.credentials, 'omit');
      assert.equal(options.cache, 'no-store');
      const promise = new Promise((resolve, reject) => {
        request.reply = (status = 200, payload = system()) => {
          request.settled = true;
          resolve({ ok: status >= 200 && status < 300, status, json: async () => structuredClone(payload) });
        };
        request.reject = (error = Error('synthetic connection loss')) => { request.settled = true; reject(error); };
      });
      // Deliberately allow a late response after abort. The real hook must
      // protect against transports that finish after navigation or clearing.
      if (responder) {
        const response = responder(request);
        if (response) request.reply(response.status, response.payload);
      }
      return promise;
    },
  };
  function evaluate(file, exports) {
    environment.module = { exports: {} };
    let source = sourceFor(file);
    if (file === 'api.ts') source = source.replaceAll('import.meta.env.VITE_SOCIALD_URL', 'undefined');
    const result = runInNewContext(`(function(){\n${transpile(file, source)}\nreturn {${exports.join(',')}};\n})()`, environment, { filename: file });
    Object.assign(environment, result);
    return result;
  }
  const api = evaluate('api.ts', ['SERVICE', 'ServiceError', 'getSystem', 'getCollection', 'getQQVerification']);
  const sessions = evaluate('service-session.ts', ['SERVICE_SESSION_KEY', 'loadServiceSession', 'saveServiceSession', 'clearServiceSession']);
  const { useServiceSession } = evaluate('useServiceSession.ts', ['useServiceSession']);
  let render = () => useServiceSession(aiDrafts);
  if (mode === 'app') {
    environment.Inbox = 'InboxStub';
    const { App } = evaluate('App.tsx', ['App']);
    render = () => App();
  }
  return {
    storage, api, sessions, aiDrafts, requests, timers,
    get value() { return rendered; },
    get localStorageReads() { return localStorageReads; },
    get postUnmountStateWrites() { return postUnmountStateWrites; },
    get locationReads() { return locationReads; },
    get listenerCount() { return [...listeners.values()].reduce((count, callbacks) => count + callbacks.size, 0); },
    setResponder(next) { responder = next; },
    dispatch(event, visibility = 'visible') {
      fakeDocument.visibilityState = visibility;
      for (const listener of listeners.get(event) ?? []) listener({});
    },
    async flush() {
      for (let pass = 0; pass < 30; pass++) {
        if (dirty && mounted) {
          dirty = false;
          cursor = 0;
          rendered = render();
          const scheduled = effects; effects = [];
          for (const effect of scheduled) effect();
        }
        await Promise.resolve();
      }
    },
    strictReplayEffects() {
      const active = slots.filter((slot) => slot.kind === 'effect');
      for (const effect of active) effect.cleanup?.();
      for (const effect of active) effect.cleanup = effect.callback();
    },
    unmount() {
      for (const slot of slots) if (slot.kind === 'effect') slot.cleanup?.();
      mounted = false;
    },
  };
}

function nodes(tree) {
  if (!tree || typeof tree !== 'object') return [];
  return [tree, ...((tree.props?.children ?? []).flat(Infinity).flatMap(nodes))];
}
function find(harness, predicate) {
  const result = nodes(harness.value).find(predicate);
  assert.ok(result, 'Expected rendered control is missing.');
  return result;
}
function settings(harness) { return find(harness, (node) => typeof node.type === 'function' && node.type.name === 'Settings').props; }
const submitEvent = { preventDefault() {} };
async function connect(harness, token) {
  harness.value.setTokenDraft(token);
  await harness.flush();
  const operation = harness.value.connect(submitEvent);
  await harness.flush();
  return { operation };
}
const connectionToken = (harness) => harness.requests.at(-1)?.options.headers.Authorization;

// Storage is versioned, origin-bound and restricted to this tab session.
{
  const h = browserHarness();
  const { SERVICE_SESSION_KEY: key, loadServiceSession: load, saveServiceSession: save, clearServiceSession: clear } = h.sessions;
  assert.equal(key, 'social-intelligence.service-session.v1');
  assert.equal(load(SERVICE_URL).token, null);
  assert.equal(save(SERVICE_URL, TOKEN_A), true);
  assert.equal(load(SERVICE_URL).token, TOKEN_A);
  assert.equal(load('http://localhost:17890').token, null, 'other service origins must not inherit credentials');
  assert.equal(clear(SERVICE_URL, TOKEN_B), true);
  assert.equal(load(SERVICE_URL).token, TOKEN_A, 'a stale token must not erase the current saved credential');
  for (const invalid of ['', ' leading', 'trailing ', 'two words', 'line\nbreak', '\u0000', '\u007f', '中文', 'x'.repeat(4097)]) assert.equal(save(SERVICE_URL, invalid), false);
  assert.equal(load(SERVICE_URL).token, TOKEN_A, 'invalid input must not replace a verified credential');
  const valid = JSON.parse(h.storage.values.get(key));
  for (const value of ['{', 'null', '[]', JSON.stringify({ ...valid, version: 2 }), JSON.stringify({ ...valid, service_url: 'https://untrusted.invalid' }), JSON.stringify({ ...valid, token: 'unsafe\nvalue' })]) {
    h.storage.values.set(key, value);
    assert.equal(load(SERVICE_URL).token, null, 'malformed saved state must never become a credential');
  }
  h.storage.values.set(key, JSON.stringify(valid));
  assert.equal(clear(SERVICE_URL), true);
  assert.equal(load(SERVICE_URL).token, null);
  h.storage.denied = true;
  assert.equal(load(SERVICE_URL).available, false);
  assert.equal(save(SERVICE_URL, TOKEN_A), false);
  assert.equal(clear(SERVICE_URL), false);
  assert.equal(h.localStorageReads, 0);
  assert.equal(h.locationReads, 0);
  h.unmount();
}

// Connecting once survives navigation and a same-token health check without
// clearing unsaved AI work. No credential is saved before actual verification.
{
  const h = browserHarness({ autoReply: null });
  await h.flush();
  assert.equal(h.requests.length, 0);
  const first = await connect(h, TOKEN_A);
  assert.equal(h.value.activeToken, '');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, null);
  h.requests.at(-1).reply(); await first.operation; await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.value.session.status, 'connected');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  h.aiDrafts.set('synthetic-chat', { draft: 'synthetic unsaved draft' });
  const second = await connect(h, TOKEN_A);
  assert.equal(h.value.activeToken, TOKEN_A, 'same-token verification must not blank the current session');
  assert.equal(h.aiDrafts.size, 1);
  h.requests.at(-1).reply(); await second.operation; await h.flush();
  assert.equal(h.aiDrafts.size, 1, 'same-token verification must retain AI edits');
  h.unmount();
}

// Reload restores only after validation; focus and visibility coalesce into one
// metadata request while leaving the valid connected screen available.
{
  const storage = syntheticStorage();
  let h = browserHarness({ storage });
  h.sessions.saveServiceSession(SERVICE_URL, TOKEN_A);
  h = browserHarness({ storage, autoReply: null });
  await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.requests.length, 1);
  assert.equal(connectionToken(h), `Bearer ${TOKEN_A}`);
  h.requests[0].reply(); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A);
  h.aiDrafts.set('synthetic-chat', { draft: 'synthetic unsaved draft' });
  const beforeFocus = h.requests.length;
  h.dispatch('focus'); h.dispatch('visibilitychange'); await h.flush();
  assert.equal(h.requests.length, beforeFocus + 1, 'focus and visibility must share an in-flight verification');
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.aiDrafts.size, 1);
  h.requests.at(-1).reply(); await h.flush();
  h.dispatch('visibilitychange', 'hidden'); await h.flush();
  assert.equal(h.requests.length, beforeFocus + 1, 'hidden tabs must not force a verification');
  h.unmount();
  assert.equal(h.listenerCount, 0, 'unmount must remove session listeners');
}

// Temporary service/network failures keep the credential and drafts; a later
// return retries metadata only. A real 401 invalidates this session.
{
  const h = browserHarness();
  await h.flush();
  await connect(h, TOKEN_A);
  h.aiDrafts.set('synthetic-chat', { draft: 'synthetic unsaved draft' });
  h.setResponder(() => ({ status: 503, payload: { error: { code: 'TEMPORARY', message: 'untrusted detail' } } }));
  h.dispatch('focus'); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  assert.equal(h.aiDrafts.size, 1);
  h.setResponder(null);
  h.dispatch('focus'); await h.flush();
  h.requests.at(-1).reject(); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  assert.equal(h.aiDrafts.size, 1);
  h.dispatch('focus'); await h.flush();
  const timeoutRequest = h.requests.at(-1);
  timeoutRequest.options.signal.addEventListener('abort', () => timeoutRequest.reject(new DOMException('synthetic timeout', 'AbortError')), { once: true });
  assert.equal(h.timers.size, 1, 'one bounded metadata timeout is expected');
  for (const timer of [...h.timers.values()]) timer.callback();
  await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A, 'a metadata timeout must not erase the verified credential');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  assert.equal(h.aiDrafts.size, 1);
  h.setResponder(() => ({ status: 200, payload: system('recovered') }));
  h.dispatch('focus'); await h.flush();
  assert.equal(h.value.session.status, 'connected');
  assert.equal(h.value.session.system.version, 'recovered');
  h.setResponder(() => ({ status: 401, payload: { error: { code: 'UNAUTHORIZED' } } }));
  h.dispatch('focus'); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, null);
  assert.equal(h.aiDrafts.size, 0);
  assert.equal(h.value.session.status, 'error');
  h.unmount();
}

// Late source callbacks and completed requests may not wipe or resurrect a
// newer credential. A user clear wins even when an old request ignores abort.
{
  const h = browserHarness();
  await h.flush(); await connect(h, TOKEN_A);
  const staleAuthFailure = h.value.onAuthFailure;
  const staleSystemUpdate = h.value.onSystemUpdate;
  h.setResponder(null);
  h.dispatch('focus'); await h.flush();
  const staleRequest = h.requests.at(-1);
  const replacement = await connect(h, TOKEN_B);
  const replacementRequest = h.requests.at(-1);
  replacementRequest.reply(200, system('new-connection'));
  await replacement.operation; await h.flush();
  staleRequest.reply(200, system('stale-connection')); await h.flush();
  staleAuthFailure(); await h.flush();
  staleSystemUpdate(system('stale-child-connection')); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_B, 'old-token authentication failure must not clear the new connection');
  assert.equal(h.value.session.system.version, 'new-connection', 'a late old response must not replace current metadata');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_B);
  h.dispatch('focus'); await h.flush();
  const requestBeforeClear = h.requests.at(-1);
  h.value.clear(); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.value.tokenDraft, '');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, null);
  requestBeforeClear.reply(200, system('late-after-clear')); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, null);
  h.unmount();
}

// React StrictMode replays mount effects during development. An aborted first
// restoration must not invalidate the successful replacement verification.
{
  const h = browserHarness({ autoReply: null });
  h.sessions.saveServiceSession(SERVICE_URL, TOKEN_A);
  await h.flush();
  assert.equal(h.requests.length, 1);
  const old = h.requests[0];
  h.strictReplayEffects(); await h.flush();
  assert.equal(h.requests.length, 2);
  assert.equal(old.options.signal.aborted, true);
  h.requests[1].reply(200, system('strict-mode-current')); await h.flush();
  old.reply(401, { error: { code: 'UNAUTHORIZED' } }); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.value.session.system.version, 'strict-mode-current');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  h.unmount();
  assert.equal(h.listenerCount, 0);
}

// An explicit clear also revokes callbacks from the old authenticated UI when
// the user later reconnects using the exact same service token.
{
  const h = browserHarness();
  await h.flush(); await connect(h, TOKEN_A);
  const oldAuthFailure = h.value.onAuthFailure;
  const oldSystemUpdate = h.value.onSystemUpdate;
  h.value.clear(); await h.flush();
  h.setResponder(() => ({ status: 200, payload: system('same-token-new-session') }));
  await connect(h, TOKEN_A);
  oldSystemUpdate(system('stale-same-token-session')); await h.flush();
  assert.equal(h.value.session.system.version, 'same-token-new-session', 'callbacks revoked by clear must not overwrite a later same-token session');
  oldAuthFailure(); await h.flush();
  assert.equal(h.value.activeToken, TOKEN_A, 'callbacks revoked by clear must not invalidate a later same-token session');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  h.unmount();
}

// Invalidating an old session must retain a replacement credential still being
// entered. Only an explicit user clear should erase that separate input.
{
  const h = browserHarness();
  await h.flush(); await connect(h, TOKEN_A);
  h.value.setTokenDraft(TOKEN_B); await h.flush();
  h.value.onAuthFailure(); await h.flush();
  assert.equal(h.value.activeToken, '');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, null);
  assert.equal(h.value.tokenDraft, TOKEN_B, 'an old session failure must not erase a replacement token the user is still entering');
  h.unmount();
}

// A stale child callback from an unmounted app must not clear the tab record
// after a replacement app has restored the same authenticated connection.
{
  const old = browserHarness();
  await old.flush(); await connect(old, TOKEN_A);
  const oldAuthFailure = old.value.onAuthFailure;
  const oldSystemUpdate = old.value.onSystemUpdate;
  old.unmount();
  const current = browserHarness({ storage: old.storage, autoReply: system('replacement-app') });
  await current.flush();
  assert.equal(current.value.activeToken, TOKEN_A);
  oldSystemUpdate(system('unmounted-app-response'));
  oldAuthFailure(); await current.flush();
  assert.equal(old.postUnmountStateWrites, 0, 'unmounted callbacks must not attempt state writes');
  assert.equal(current.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A, 'unmounted callbacks must not erase a replacement app session');
  assert.equal(current.value.session.system.version, 'replacement-app');
  current.unmount();
}

// The memory fallback remains useful when storage is blocked.
{
  const storage = syntheticStorage(); storage.denied = true;
  const h = browserHarness({ storage });
  await h.flush(); await connect(h, TOKEN_A);
  assert.equal(h.value.activeToken, TOKEN_A);
  assert.equal(h.value.storageAvailable, false);
  assert.equal(h.localStorageReads, 0);
  h.value.clear(); await h.flush();
  assert.equal(h.value.activeToken, '');
  h.unmount();
}

// Exercise the actual application shell to catch accidental credential loss
// between its own navigation pages, not only the isolated hook.
{
  const h = browserHarness({ mode: 'app' });
  await h.flush();
  find(h, (node) => node.type === 'button' && node.props['aria-label'] === '设置').props.onClick();
  await h.flush();
  settings(h).onDraftChange(TOKEN_A); await h.flush();
  await settings(h).onConnect(submitEvent); await h.flush();
  const requestsAfterConnect = h.requests.length;
  const navigation = (label) => find(h, (node) => node.type === 'button' && node.props['aria-label'] === label).props.onClick();
  navigation('消息'); await h.flush();
  const drafts = find(h, (node) => node.type === 'InboxStub').props.aiDrafts;
  drafts.set('synthetic-chat', { draft: 'synthetic navigation draft' });
  for (const page of ['联系人', '待审批', '概览', '设置', '消息']) { navigation(page); await h.flush(); }
  const inbox = find(h, (node) => node.type === 'InboxStub');
  assert.equal(inbox.props.token, TOKEN_A);
  assert.equal(inbox.props.aiDrafts, drafts, 'navigation must retain the authenticated AI buffer');
  assert.equal(drafts.get('synthetic-chat').draft, 'synthetic navigation draft');
  assert.equal(h.requests.length, requestsAfterConnect, 'internal page navigation must not reconnect or replay service requests');
  assert.equal(h.sessions.loadServiceSession(SERVICE_URL).token, TOKEN_A);
  assert.equal(h.localStorageReads, 0);
  assert.equal(h.locationReads, 0);
  h.unmount();
}

console.log('Service session synthetic checks passed: origin-bound tab storage, verified restoration, metadata-only focus recovery, transient failure retention, 401/clear invalidation, stale and unmounted callbacks, same-token authentication epochs, StrictMode replay, draft preservation, storage denial and actual app navigation.');
