import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';

// Synthetic checks: no Telegram account, service credentials or real network.
const SERVICE_URL = 'http://127.0.0.1:17890';
const TOKEN = 'synthetic-telegram-service-token';
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const readyStatus = (overrides = {}) => ({
  configured: true, helper_available: true, state: 'identity_verified', requires_initialization: false,
  account_id: '12345', name: 'Synthetic owner', read_only: true, sending_enabled: false,
  checked_at: '', last_sync_at: '', last_error: '', ...overrides,
});
const dialogResponse = () => ({
  account_id: '12345', name: 'Synthetic owner', read_only: true, sending_enabled: false,
  items: [{ peer_id: '23456', name: 'Synthetic contact' }], limited: false,
});
const syncResponse = () => ({
  inserted: 2, duplicates: 1, conversations: 1, skipped: 0, limited: true,
  source_kind: 'telegram_user', read_only: true, sending_enabled: false,
});

const requests = [];
let response = readyStatus();
let responseStatus = 200;
const apiModule = { exports: {} };
const apiSource = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/telegram-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource)
  .replace(/^export /gm, '');
runInNewContext(apiSource + '\nmodule.exports={getTelegramStatus,verifyTelegram,getTelegramDialogs,syncTelegram};', {
  module: apiModule, AbortController, DOMException,
  transport: { SERVICE: { url: SERVICE_URL, error: null }, ServiceError },
  window: { setTimeout: () => 1, clearTimeout() {} },
  fetch: async (url, options) => {
    requests.push({ url, ...options });
    return { ok: responseStatus === 200, status: responseStatus, json: async () => structuredClone(response) };
  },
});
const api = apiModule.exports;
const signal = () => new AbortController().signal;
await api.getTelegramStatus(TOKEN, signal());
assert.equal(requests[0].url, `${SERVICE_URL}/api/v1/connections/telegram-user/status`);
assert.equal(requests[0].method, 'GET');
assert.equal(requests[0].body, undefined);
assert.equal(requests[0].headers.Authorization, `Bearer ${TOKEN}`);
assert.equal(requests[0].credentials, 'omit');
assert.equal(requests[0].redirect, 'error');
assert.equal(requests[0].cache, 'no-store');
await api.verifyTelegram(TOKEN, signal());
assert.equal(requests.at(-1).method, 'POST');
assert.equal(requests.at(-1).body, undefined, 'verification must never carry credentials or configuration');
response = dialogResponse();
await api.getTelegramDialogs(TOKEN, signal());
assert.equal(requests.at(-1).method, 'GET');
response = syncResponse();
await api.syncTelegram(TOKEN, signal(), '23456', '12345', 50);
assert.equal(requests.at(-1).method, 'POST');
assert.deepEqual(JSON.parse(requests.at(-1).body), { expected_account_id: '12345', peer_id: '23456', limit: 50 });
const beforeInvalid = requests.length;
for (const [peer, limit] of [['-1234', 50], ['123/../send', 50], ['23456', 0], ['23456', 101], ['23456', 1.5]]) {
  await assert.rejects(api.syncTelegram(TOKEN, signal(), peer, '12345', limit), (e) => e.code === 'INVALID_INPUT');
}
for (const account of [undefined, '', '-123', '01', '9223372036854775808', '12345/../credentials']) {
  await assert.rejects(api.syncTelegram(TOKEN, signal(), '23456', account, 50), (e) => e.code === 'INVALID_INPUT');
}
assert.equal(requests.length, beforeInvalid, 'invalid selection must fail before transport');
for (const invalidStatus of [readyStatus({ sending_enabled: true }), readyStatus({ api_hash: 'forbidden-secret' }), readyStatus({ account_id: '-100123' }), readyStatus({ requires_initialization: 'false' })]) {
  response = invalidStatus;
  await assert.rejects(api.getTelegramStatus(TOKEN, signal()), (e) => e.code === 'INVALID_RESPONSE');
}
response = readyStatus({ state: 'configured', account_id: '' });
await assert.rejects(api.verifyTelegram(TOKEN, signal()), (e) => e.code === 'INVALID_RESPONSE');
response = { ...dialogResponse(), items: [{ peer_id: '23456', name: 'A' }, { peer_id: '23456', name: 'B' }] };
await assert.rejects(api.getTelegramDialogs(TOKEN, signal()), (e) => e.code === 'INVALID_RESPONSE');
response = { ...syncResponse(), inserted: -1 };
await assert.rejects(api.syncTelegram(TOKEN, signal(), '23456', '12345'), (e) => e.code === 'INVALID_RESPONSE');
response = { error: { code: 'TELEGRAM_AUTH_REQUIRED', message: 'forbidden api_hash session phone password' } };
responseStatus = 503;
await assert.rejects(api.verifyTelegram(TOKEN, signal()), (e) => e.code === 'TELEGRAM_AUTH_REQUIRED' && !e.message.includes('forbidden'));
for (const code of ['TELEGRAM_REQUIRES_INITIALIZATION', 'TELEGRAM_CONFIG_INVALID', 'TELEGRAM_LOGGED_OUT', 'TELEGRAM_AUTH_FAILED', 'TELEGRAM_NETWORK_ERROR', 'TELEGRAM_TIMEOUT', 'TELEGRAM_UNSUPPORTED_PLATFORM']) {
  response = { error: { code, message: 'forbidden upstream credential detail' } };
  await assert.rejects(api.verifyTelegram(TOKEN, signal()), (e) => e.code === code && !e.message.includes('forbidden') && !e.message.includes('操作未完成'));
}
responseStatus = 401;
await assert.rejects(api.getTelegramStatus(TOKEN, signal()), (e) => e.code === 'UNAUTHORIZED');
const cancelled = new AbortController(); cancelled.abort();
const countBeforeCancel = requests.length;
await assert.rejects(api.getTelegramStatus(TOKEN, cancelled.signal), (e) => e.name === 'AbortError');
assert.ok(requests.length <= countBeforeCancel + 1, 'aborted requests must not retry');

const panelSource = readFileSync(new URL('../apps/desktop/src/TelegramPanel.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export /gm, '');
const compiledPanel = transformSync('TelegramPanel.tsx', panelSource, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(compiledPanel.errors.length, 0, 'the real panel must compile');

function panelHarness({ configured = true, deferred = null } = {}) {
  const slots = [], calls = [];
  const failures = new Map();
  let cursor = 0, dirty = true, effects = [], tree, mounted = true, lateWrites = 0;
  let token = TOKEN;
  let authFailures = 0;
  const statusValue = () => readyStatus({ configured, state: configured ? 'configured' : 'not_configured', account_id: '', name: '' });
  let cached = statusValue();
  const same = (a, b) => Array.isArray(a) && a.length === b.length && a.every((v, i) => Object.is(v, b[i]));
  const apiCall = async (kind, serviceToken, abortSignal, ...args) => {
    calls.push({ kind, token: serviceToken, signal: abortSignal, args });
    if (failures.has(kind)) { const code = failures.get(kind); failures.delete(kind); throw new ServiceError('Synthetic safe failure', code); }
    if (deferred?.kind === kind) return await deferred.promise;
    if (kind === 'status') return structuredClone(cached);
    if (kind === 'verify') { cached = readyStatus(); return structuredClone(cached); }
    if (kind === 'dialogs') return dialogResponse();
    if (kind === 'sync') return syncResponse();
    return { workspace_id: 'synthetic' };
  };
  const environment = {
    ServiceError, AbortController,
    useState(initial) {
      const i = cursor++;
      if (!(i in slots)) slots[i] = { value: typeof initial === 'function' ? initial() : initial };
      return [slots[i].value, (next) => { if (!mounted) lateWrites++; slots[i].value = typeof next === 'function' ? next(slots[i].value) : next; dirty = true; }];
    },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { value: { current: initial } }; return slots[i].value; },
    useEffect(callback, deps) {
      const i = cursor++, previous = slots[i];
      if (!previous || !same(previous.deps, deps)) effects.push(() => { previous?.cleanup?.(); slots[i] = { deps, cleanup: callback() }; });
    },
    jsx: (type, props, ...children) => ({ type, props: { ...props, children } }),
    Fragment: 'fragment',
    getTelegramStatus: (...args) => apiCall('status', ...args),
    verifyTelegram: (...args) => apiCall('verify', ...args),
    getTelegramDialogs: (...args) => apiCall('dialogs', ...args),
    syncTelegram: (...args) => apiCall('sync', ...args),
    getSystem: (...args) => apiCall('system', ...args),
  };
  const Panel = runInNewContext(`(function(){${compiledPanel.code}\nreturn TelegramPanel;})()`, environment);
  return {
    calls,
    get tree() { return tree; },
    get lateWrites() { return lateWrites; },
    get authFailures() { return authFailures; },
    setToken(next) { token = next; dirty = true; },
    failNext(kind, code) { failures.set(kind, code); },
    setCached(next) { cached = next; },
    async flush() {
      for (let i = 0; i < 30; i++) {
        if (dirty && mounted) {
          dirty = false; cursor = 0;
          tree = Panel({ token, onAuthFailure: () => authFailures++, onSystemUpdate: () => {} });
          const pendingEffects = effects; effects = []; pendingEffects.forEach((effect) => effect());
        }
        await Promise.resolve();
      }
    },
    unmount() { for (const slot of slots) slot?.cleanup?.(); mounted = false; },
  };
}
const nodes = (tree) => {
  const found = [];
  const visit = (node) => { if (Array.isArray(node)) return node.forEach(visit); if (!node || typeof node !== 'object') return; found.push(node); visit(node.props?.children); };
  visit(tree); return found;
};
const textOf = (tree) => Array.isArray(tree) ? tree.map(textOf).join('') : tree && typeof tree === 'object' ? textOf(tree.props?.children) : String(tree ?? '');
const button = (h, label) => nodes(h.tree).find((n) => n.type === 'button' && textOf(n).includes(label));
const byID = (h, id) => nodes(h.tree).find((n) => n.props?.id === id);

const h = panelHarness();
await h.flush();
assert.deepEqual(h.calls.map((c) => c.kind), ['status'], 'mount only reads cached local metadata');
assert.ok(textOf(h.tree).includes('initialize-telegram.ps1'));
assert.equal(nodes(h.tree).filter((n) => n.type === 'input').length, 1, 'only a record-count input; no login credentials in the browser');
assert.equal(button(h, '读取私聊列表').props.disabled, true);
button(h, '核验个人账号').props.onClick();
button(h, '核验个人账号').props.onClick();
await h.flush();
assert.equal(h.calls.filter((c) => c.kind === 'verify').length, 1, 'double click cannot duplicate online verification');
assert.equal(h.calls.filter((c) => c.kind === 'dialogs').length, 0, 'verification must not silently read chats');
assert.equal(button(h, '读取私聊列表').props.disabled, false);
button(h, '读取私聊列表').props.onClick();
await h.flush();
assert.equal(byID(h, 'telegram-private-peer').props.value, '', 'never preselect a private conversation');
assert.equal(button(h, '同步选定私聊').props.disabled, true);
byID(h, 'telegram-private-peer').props.onChange({ target: { value: '23456' } });
await h.flush();
assert.equal(button(h, '同步选定私聊').props.disabled, false);
byID(h, 'telegram-sync-limit').props.onChange({ target: { value: '101' } });
await h.flush();
assert.equal(button(h, '同步选定私聊').props.disabled, true);
byID(h, 'telegram-sync-limit').props.onChange({ target: { value: '50' } });
await h.flush();
button(h, '同步选定私聊').props.onClick();
await h.flush();
const sync = h.calls.find((c) => c.kind === 'sync');
assert.deepEqual(sync.args, ['23456', '12345', 50], 'sync must bind the explicitly loaded dialog list to its owner account');
assert.equal(h.calls.filter((c) => c.kind === 'sync').length, 1);
assert.ok(textOf(h.tree).includes('新增 2 条'));
h.failNext('sync', 'TELEGRAM_LOGGED_OUT');
button(h, '同步选定私聊').props.onClick();
await h.flush();
assert.equal(byID(h, 'telegram-private-peer').props.value, '', 'login failure clears the previous private peer');
assert.equal(button(h, '同步选定私聊').props.disabled, true);
assert.equal(h.calls.filter((c) => c.kind === 'sync').length, 2, 'failed sync must not automatically retry');
h.setToken('replacement-service-token');
await h.flush();
assert.equal(byID(h, 'telegram-private-peer').props.value, '');
assert.equal(button(h, '同步选定私聊').props.disabled, true, 'changing local credentials clears selected conversation');

const race = panelHarness();
await race.flush();
button(race, '核验个人账号').props.onClick(); await race.flush();
button(race, '读取私聊列表').props.onClick(); await race.flush();
byID(race, 'telegram-private-peer').props.onChange({ target: { value: '23456' } }); await race.flush();
race.failNext('sync', 'TELEGRAM_IDENTITY_MISMATCH');
button(race, '同步选定私聊').props.onClick(); await race.flush();
assert.deepEqual(race.calls.find((c) => c.kind === 'sync').args, ['23456', '12345', 50]);
assert.equal(byID(race, 'telegram-private-peer').props.value, '', 'another window changing owner invalidates the old selection');
assert.equal(button(race, '同步选定私聊').props.disabled, true);
assert.equal(race.calls.filter((c) => c.kind === 'sync').length, 1, 'account conflict cannot retry an import');
button(race, '刷新本机状态').props.onClick(); await race.flush();
button(race, '核验个人账号').props.onClick(); await race.flush();
button(race, '读取私聊列表').props.onClick(); await race.flush();
byID(race, 'telegram-private-peer').props.onChange({ target: { value: '23456' } }); await race.flush();
race.setCached(readyStatus({ account_id: '99999', name: 'Different synthetic owner' }));
button(race, '刷新本机状态').props.onClick(); await race.flush();
assert.equal(byID(race, 'telegram-private-peer').props.value, '', 'refreshed account changes clear the private dialog snapshot');
assert.equal(button(race, '同步选定私聊').props.disabled, true);
assert.equal(race.calls.filter((c) => c.kind === 'sync').length, 1);

let resolve;
const late = panelHarness({ deferred: { kind: 'status', promise: new Promise((done) => { resolve = done; }) } });
await late.flush();
late.unmount();
resolve(readyStatus());
await late.flush();
assert.equal(late.lateWrites, 0, 'late metadata response cannot update an unmounted panel');
assert.equal(late.calls.length, 1, 'navigation never retries or replays an import');

console.log('Telegram personal client synthetic checks passed (transport, read-only validation, explicit selection, cancellation).');
