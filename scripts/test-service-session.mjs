import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { pathToFileURL } from 'node:url';
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
  const values = new Map(initial), reads = [], writes = [], removals = [];
  return {
    values, reads, writes, removals,
    denied: false,
    getItem(key) { reads.push(key); if (this.denied) throw Error('synthetic storage denial'); return values.get(key) ?? null; },
    setItem(key, value) { writes.push({ key, value }); if (this.denied) throw Error('synthetic storage denial'); values.set(key, String(value)); },
    removeItem(key) { removals.push(key); if (this.denied) throw Error('synthetic storage denial'); values.delete(key); },
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

function browserHarness({ storage = syntheticStorage(), mode = 'hook', autoReply = system(), authBrowser = null } = {}) {
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
  if (authBrowser) Object.defineProperties(fakeWindow, Object.getOwnPropertyDescriptors(authBrowser.window));
  const fakeDocument = { visibilityState: 'visible', addEventListener: addListener, removeEventListener: removeListener };
  const environment = {
    ...(authBrowser?.globals ?? {}),
    ...hooks, jsx, Fragment: 'fragment',
    MilkyPanel: 'MilkyPanelStub', TelegramPanel: 'TelegramPanelStub', AIModelPanel: 'AIModelPanelStub', DigestPanel: 'DigestPanelStub',
    module: { exports: {} },
    window: fakeWindow, document: fakeDocument,
    URL, AbortController, DOMException, Date, Map,
    fetch(url, options) {
      if (authBrowser) return authBrowser.fetch(url, options);
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
  const auth = evaluate('device-auth.ts', ['DeviceAuthError', 'authorizedFetch', 'authErrorMessage', 'deviceAuthAvailable', 'isDeviceHandle', 'loadDeviceSession', 'pairDevice', 'forgetDevice', 'subscribeDeviceForget']);
  const api = evaluate('api.ts', ['SERVICE', 'ServiceError', 'getSystem', 'getCollection']);
  const sessions = evaluate('service-session.ts', ['SERVICE_SESSION_KEY', 'loadServiceSession', 'saveServiceSession', 'clearServiceSession']);
  const { useServiceSession } = evaluate('useServiceSession.ts', ['useServiceSession']);
  let render = () => useServiceSession(aiDrafts);
  if (mode === 'app') {
    environment.Inbox = 'InboxStub';
    const { App } = evaluate('App.tsx', ['App']);
    render = () => App();
  }
  return {
    storage, api, auth, sessions, aiDrafts, requests, timers, evaluate,
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

export { browserHarness, syntheticStorage, system, nodes, find, settings };

// New device tests reuse this unchanged React lifecycle harness. Importing it
// does not rerun the original eleven groups; its standalone command still does.
if (process.argv[1] && pathToFileURL(process.argv[1]).href === import.meta.url) {
// The fallback is exact-origin-bound page memory, never Web Storage. Startup
// purges the legacy key without reading/importing any obsolete master token.
{
  const key = 'social-intelligence.service-session.v1';
  const legacy = { version: 1, service_url: SERVICE_URL, token: TOKEN_A };
  const storage = syntheticStorage(new Map([[key, JSON.stringify(legacy)], ['unrelated-session', 'keep']]));
  const h = browserHarness({ storage });
  const { SERVICE_SESSION_KEY, loadServiceSession: load, saveServiceSession: save, clearServiceSession: clear } = h.sessions;
  assert.equal(SERVICE_SESSION_KEY, key);
  assert.equal(storage.values.has(key), false, 'legacy master must be removed immediately at module startup');
  assert.equal(storage.values.get('unrelated-session'), 'keep');
  assert.equal(load(SERVICE_URL).token, null, 'legacy content is never accepted as a credential');
  assert.equal(save(SERVICE_URL, TOKEN_A), true);
  assert.equal(load(SERVICE_URL).token, TOKEN_A);
  assert.equal(storage.values.has(key), false);
  assert.equal(load('http://localhost:17890').token, null, 'other service URLs must not inherit memory credentials');
  assert.equal(clear(SERVICE_URL, TOKEN_B), true);
  assert.equal(load(SERVICE_URL).token, TOKEN_A, 'a stale token must not erase the current memory credential');
  for (const invalid of ['', ' leading', 'trailing ', 'two words', 'line\nbreak', '\u0000', '\u007f', '中文', 'x'.repeat(4097)]) assert.equal(save(SERVICE_URL, invalid), false);
  assert.equal(load(SERVICE_URL).token, TOKEN_A, 'invalid input must not replace a verified credential');
  for (const value of [JSON.stringify(legacy), '{', 'null', '[]', JSON.stringify({ ...legacy, version: 2 }), JSON.stringify({ ...legacy, service_url: 'https://untrusted.invalid' }), JSON.stringify({ ...legacy, token: 'unsafe\nvalue' })]) {
    storage.values.set(key, value);
    assert.equal(load(SERVICE_URL).token, TOKEN_A, 'legacy contents cannot overwrite the page-memory credential');
    assert.equal(storage.values.has(key), false);
  }
  assert.equal(clear(SERVICE_URL), true);
  assert.equal(load(SERVICE_URL).token, null);
  storage.denied = true;
  assert.equal(load(SERVICE_URL).available, false, 'cleanup denial is reported truthfully');
  assert.equal(save(SERVICE_URL, TOKEN_A), false);
  assert.equal(load(SERVICE_URL).token, TOKEN_A, 'memory connection still works when Web Storage is denied');
  assert.equal(clear(SERVICE_URL), false);
  assert.equal(load(SERVICE_URL).token, null, 'Web Storage denial cannot stop an in-memory clear');
  assert.equal(storage.reads.length, 0);
  assert.equal(storage.writes.length, 0);
  assert.ok(storage.removals.every((entry) => entry === key), 'only our obsolete session key may be removed');
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

// Reload cannot restore a master; a same-page module-memory remount validates
// again. Focus/visibility share one metadata request without blanking the UI.
{
  const storage = syntheticStorage();
  const previousPage = browserHarness({ storage });
  previousPage.sessions.saveServiceSession(SERVICE_URL, TOKEN_A);
  previousPage.unmount();
  const reloadedPage = browserHarness({ storage });
  await reloadedPage.flush();
  assert.equal(reloadedPage.requests.length, 0, 'a reload must not recover any bearer from Web Storage');
  assert.equal(reloadedPage.value.activeToken, '');
  assert.equal(reloadedPage.value.tokenDraft, '');
  reloadedPage.unmount();
  const h = browserHarness({ storage, autoReply: null });
  // Explicitly seed this VM's module-memory cache, as a same-page remount does.
  h.sessions.saveServiceSession(SERVICE_URL, TOKEN_A);
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

// A stale child callback from an unmounted app must not clear the memory cache
// after a same-page replacement app verifies the same authenticated connection.
{
  const old = browserHarness();
  await old.flush(); await connect(old, TOKEN_A);
  const oldAuthFailure = old.value.onAuthFailure;
  const oldSystemUpdate = old.value.onSystemUpdate;
  old.unmount();
  const current = browserHarness({ storage: old.storage, autoReply: system('replacement-app') });
  current.sessions.saveServiceSession(SERVICE_URL, TOKEN_A); // same-page memory cache, not disk/sessionStorage
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

console.log('Service session synthetic checks passed (11 groups): exact-URL page-memory cache, no Web Storage reads/writes, legacy-master purge without migration, reload requires reconnect, verified same-page restoration, metadata-only focus recovery, transient retention, 401/clear invalidation, stale/unmounted callbacks, same-token epochs, StrictMode replay, draft preservation, cleanup denial and actual app navigation.');
}
