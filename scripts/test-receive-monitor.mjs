import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';

// All data and credentials here are synthetic; no service or platform is used.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const state = (channel = 'email-imap', overrides = {}) => ({
  enabled: false, interval_seconds: 60, state: 'disabled', last_success_at: null,
  last_error_code: null, read_only: true, sending_enabled: false,
  ...(channel === 'telegram-user' ? { peer_ids: [], expected_account_id: '12345' } : {}), ...overrides,
});
function compile(file) {
  const source = readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8')
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '').replace(/^export /gm, '');
  const result = transformSync(file, source, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(result.errors.length, 0); return result.code;
}
const requests = [];
const timeouts = [];
let payload = state(), status = 200;
const api = runInNewContext(`(function(){${deviceAuthSource}\n${compile('receive-monitor-api.ts')}\nreturn {getReceiveMonitor,saveReceiveMonitor,receiveMonitorError};})()`, {
  SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError, Error, AbortController, DOMException,
  window: { setTimeout: (_callback, ms) => { timeouts.push(ms); return 1; }, clearTimeout() {} },
  fetch: async (url, options) => { requests.push({ url, ...options }); return { ok: status === 200, status, json: async () => structuredClone(payload) }; },
});
const signal = () => new AbortController().signal;
await api.getReceiveMonitor('email-imap', 'synthetic-token', signal());
assert.equal(timeouts.at(-1), 10_000);
assert.equal(requests.at(-1).url, 'http://127.0.0.1:17890/api/v1/connections/email-imap/monitor');
assert.equal(requests.at(-1).method, 'GET');
assert.equal(requests.at(-1).body, undefined);
assert.equal(requests.at(-1).credentials, 'omit'); assert.equal(requests.at(-1).cache, 'no-store'); assert.equal(requests.at(-1).redirect, 'error');
assert.equal(requests.at(-1).headers.Authorization, 'Bearer synthetic-token');
await api.saveReceiveMonitor('email-imap', 'synthetic-token', signal(), { enabled: true });
assert.equal(timeouts.at(-1), 40_000, 'activation allows the backend mailbox baseline probe to finish');
assert.deepEqual(JSON.parse(requests.at(-1).body), { enabled: true, interval_seconds: 60 });
payload = state('telegram-user');
await api.saveReceiveMonitor('telegram-user', 'synthetic-token', signal(), { enabled: true, interval_seconds: 90, expected_account_id: '12345', peer_ids: ['23456', '34567'] });
assert.equal(requests.at(-1).url.endsWith('/telegram-user/monitor'), true);
assert.deepEqual(JSON.parse(requests.at(-1).body), { enabled: true, interval_seconds: 90, expected_account_id: '12345', peer_ids: ['23456', '34567'] });
await api.saveReceiveMonitor('telegram-user', 'synthetic-token', signal(), { enabled: false, expected_account_id: '', peer_ids: ['bad'], interval_seconds: 1 });
assert.deepEqual(JSON.parse(requests.at(-1).body), { enabled: false }, 'pause must not require an account, selection or valid interval');
const invalidStart = requests.length;
for (const seconds of [29, 901, 30.5, NaN]) await assert.rejects(async () => api.saveReceiveMonitor('email-imap', 'synthetic-token', signal(), { enabled: true, interval_seconds: seconds }), e => e.code === 'INVALID_REQUEST');
for (const peers of [[], ['23456', '23456'], ['-123'], ['9223372036854775808'], Array.from({ length: 11 }, (_, i) => String(i + 1))]) await assert.rejects(async () => api.saveReceiveMonitor('telegram-user', 'synthetic-token', signal(), { enabled: true, expected_account_id: '12345', peer_ids: peers }), e => e.code === 'INVALID_REQUEST');
await assert.rejects(async () => api.saveReceiveMonitor('telegram-user', 'synthetic-token', signal(), { enabled: true, peer_ids: ['23456'] }), e => e.code === 'INVALID_REQUEST');
assert.equal(requests.length, invalidStart, 'invalid configuration must fail before transport');
for (const invalid of [state('email-imap', { sending_enabled: true }), state('email-imap', { read_only: false }), state('email-imap', { last_error_code: 'unsafe secret message' }), state('email-imap', { interval_seconds: 0 }), state('email-imap', { skipped: -1 }), state('email-imap', { api_hash: 'do-not-echo' })]) {
  payload = invalid; await assert.rejects(api.getReceiveMonitor('email-imap', 'synthetic-token', signal()), e => e.code === 'INVALID_RESPONSE');
}
payload = state('email-imap', { state: 'future_state', last_error_code: 'NEW_SAFE_CODE' });
assert.equal((await api.getReceiveMonitor('email-imap', 'synthetic-token', signal())).state, 'future_state', 'new sanitized states must be compatible');
assert.ok(!api.receiveMonitorError('NEW_SAFE_CODE').includes('NEW_SAFE_CODE'));
status = 503; payload = { error: { code: 'EMAIL_AUTH_FAILED', message: 'private password must never appear' } };
await assert.rejects(api.getReceiveMonitor('email-imap', 'synthetic-token', signal()), e => e.code === 'EMAIL_AUTH_FAILED' && !e.message.includes('private'));
status = 404;
await assert.rejects(api.getReceiveMonitor('email-imap', 'synthetic-token', signal()), e => e.code === 'MONITOR_UNSUPPORTED');
status = 401;
await assert.rejects(api.getReceiveMonitor('email-imap', 'synthetic-token', signal()), e => e.code === 'UNAUTHORIZED');

function harness(initial = {}, options = {}) {
  let props = { channel: 'email-imap', token: 'synthetic-token', canEnable: true, onAuthFailure() {}, ...initial };
  let cached = state(props.channel, options.monitor ?? {});
  const slots = [], calls = [], timers = new Map(), listeners = new Map();
  let cursor = 0, dirty = true, effects = [], tree, mounted = true, lateWrites = 0;
  const document = { visibilityState: 'visible', addEventListener: add, removeEventListener: remove };
  function add(event, handler) { if (!listeners.has(event)) listeners.set(event, new Set()); listeners.get(event).add(handler); }
  function remove(event, handler) { listeners.get(event)?.delete(handler); }
  const same = (a, b) => Array.isArray(a) && a.length === b.length && a.every((v, i) => Object.is(v, b[i]));
  const hooks = {
    useState(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { value: typeof initial === 'function' ? initial() : initial }; return [slots[i].value, next => { if (!mounted) lateWrites++; const value = typeof next === 'function' ? next(slots[i].value) : next; if (!Object.is(value, slots[i].value)) { slots[i].value = value; dirty = true; } }]; },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { value: { current: initial } }; return slots[i].value; },
    useEffect(callback, deps) { const i = cursor++, prior = slots[i]; if (!prior || !same(prior.deps, deps)) effects.push(() => { prior?.cleanup?.(); slots[i] = { deps, cleanup: callback() }; }); },
  };
  const Component = runInNewContext(`(function(){${compile('ReceiveMonitor.tsx')}\nreturn ReceiveMonitor;})()`, {
    ...hooks, ServiceError, Error, AbortController, document,
    window: { setInterval: (callback, ms) => { const id = timers.size + 1; timers.set(id, { callback, ms }); return id; }, clearInterval: id => timers.delete(id), addEventListener: add, removeEventListener: remove },
    jsx: (type, supplied, ...children) => ({ type, props: { ...supplied, children } }), Fragment: 'fragment',
    receiveMonitorError: api.receiveMonitorError,
    getReceiveMonitor: async (channel, token, signal) => { calls.push({ action: 'get', channel, token, signal }); if (options.getError) throw options.getError; if (options.deferred) return options.deferred; return structuredClone(cached); },
    saveReceiveMonitor: async (channel, token, signal, settings) => { calls.push({ action: 'post', channel, token, signal, settings: structuredClone(settings) }); if (options.saveError) throw options.saveError; cached = { ...cached, ...settings, state: settings.enabled ? 'starting' : 'disabled' }; return structuredClone(cached); },
  });
  return {
    calls, timers, document,
    get tree() { return tree; }, get lateWrites() { return lateWrites; },
    update(next) { props = { ...props, ...next }; dirty = true; },
    setCached(next) { cached = { ...cached, ...next }; },
    dispatch(event) { for (const callback of listeners.get(event) ?? []) callback(); },
    async tick() { for (const timer of timers.values()) timer.callback(); await this.flush(); },
    async flush() { for (let n = 0; n < 30; n++) { if (dirty && mounted) { dirty = false; cursor = 0; tree = Component(props); const ready = effects; effects = []; ready.forEach(effect => effect()); } await Promise.resolve(); } },
    unmount() { for (const slot of slots) slot?.cleanup?.(); mounted = false; },
  };
}
const nodes = tree => Array.isArray(tree) ? tree.flatMap(nodes) : tree && typeof tree === 'object' ? [tree, ...nodes(tree.props?.children)] : [];
const text = tree => Array.isArray(tree) ? tree.map(text).join('') : tree && typeof tree === 'object' ? text(tree.props?.children) : String(tree ?? '');
const find = (h, predicate) => { const node = nodes(h.tree).find(predicate); assert.ok(node, 'expected monitor control'); return node; };
const button = (h, label) => find(h, n => n.type === 'button' && text(n).includes(label));
const intervalInput = h => find(h, n => n.type === 'input' && n.props.type === 'number');

const email = harness(); await email.flush();
assert.equal(email.calls.filter(c => c.action === 'post').length, 0);
assert.equal(intervalInput(email).props.value, '60');
intervalInput(email).props.onChange({ target: { value: '90' } }); await email.flush();
email.setCached({ interval_seconds: 300 }); await email.tick();
assert.equal(intervalInput(email).props.value, '90', 'metadata polling preserves unsaved interval');
button(email, '保存并开启').props.onClick(); await email.flush();
assert.deepEqual(email.calls.find(c => c.action === 'post').settings, { enabled: true, interval_seconds: 90 });
assert.ok(text(email.tree).includes('正在建立新消息起点'));
assert.ok(text(email.tree).includes('尚无成功接收检查记录'));
email.update({ canEnable: false }); await email.flush();
assert.equal(button(email, '暂停后台接收').props.disabled, false, 'pause works even after configuration becomes unavailable');
button(email, '暂停后台接收').props.onClick(); await email.flush();
assert.deepEqual(email.calls.filter(c => c.action === 'post').at(-1).settings, { enabled: false });
const beforeHidden = email.calls.length;
email.document.visibilityState = 'hidden'; email.dispatch('visibilitychange'); await email.tick();
assert.equal(email.calls.length, beforeHidden);
email.document.visibilityState = 'visible'; email.dispatch('visibilitychange'); await email.flush();
assert.ok(email.calls.length > beforeHidden);
email.unmount(); assert.equal(email.timers.size, 0);

const telegram = harness({ channel: 'telegram-user', verifiedAccountID: '12345', dialogsAccountID: '12345', dialogs: [{ peer_id: '23456', name: 'Synthetic A' }, { peer_id: '34567', name: 'Synthetic B' }] });
await telegram.flush();
assert.equal(button(telegram, '保存并开启').props.disabled, true, 'no private chat may be preselected');
for (const box of nodes(telegram.tree).filter(n => n.type === 'input' && n.props.type === 'checkbox')) box.props.onChange({ target: { checked: true } });
await telegram.flush();
const staleEnable = button(telegram, '保存并开启').props.onClick;
button(telegram, '保存并开启').props.onClick(); await telegram.flush();
assert.deepEqual(telegram.calls.find(c => c.action === 'post').settings, { enabled: true, interval_seconds: 60, expected_account_id: '12345', peer_ids: ['23456', '34567'] });
telegram.update({ verifiedAccountID: '99999', dialogsAccountID: '99999', dialogs: [{ peer_id: '45678', name: 'Different synthetic scope' }] }); await telegram.flush();
const beforeScope = telegram.calls.length; staleEnable(); await telegram.flush();
assert.equal(telegram.calls.length, beforeScope, 'stale account callbacks cannot authorize new-owner receiving');
assert.equal(button(telegram, '保存并开启').props.disabled, true);
telegram.unmount();

const older = harness({}, { getError: new ServiceError('old service', 'MONITOR_UNSUPPORTED', 404) }); await older.flush();
assert.ok(text(older.tree).includes('手动读取仍可使用'));
assert.equal(nodes(older.tree).some(n => n.type === 'input'), false);
const oldReads = older.calls.length; await older.tick(); assert.equal(older.calls.length, oldReads, 'unsupported routes are not polled repeatedly');
older.unmount();
const future = harness({}, { monitor: { enabled: true, state: 'future_state', last_error_code: 'NEW_SAFE_CODE', last_success_at: '2026-11-18T01:00:00Z', retry_not_before: '2026-11-18T01:01:00Z', skipped: 2, missing_uid_count: 1 } }); await future.flush();
assert.ok(text(future.tree).includes('执行状态待确认'));
assert.ok(text(future.tree).includes('UID 空位 1 个'));
assert.ok(!text(future.tree).includes('NEW_SAFE_CODE'));
assert.ok(text(future.tree).includes('2026'));
assert.ok(text(future.tree).includes('最早再次检查'));
future.unmount();
let resolve;
const late = harness({}, { deferred: new Promise(done => { resolve = done; }) }); await late.flush(); late.unmount(); resolve(state()); await late.flush();
assert.equal(late.lateWrites, 0); assert.equal(late.timers.size, 0);
const uncertain = harness({}, { saveError: new ServiceError('settings may have been saved', 'TIMEOUT') }); await uncertain.flush();
button(uncertain, '保存并开启').props.onClick(); await uncertain.flush();
assert.equal(uncertain.calls.filter(c => c.action === 'post').length, 1);
assert.equal(button(uncertain, '保存并开启').props.disabled, true, 'ambiguous writes wait for a successful cache read');
assert.equal(button(uncertain, '暂停后台接收').props.disabled, false);
uncertain.unmount();
console.log('Receive monitor synthetic checks passed: fixed authenticated routes, explicit enable/pause, Telegram owner/peer scope, default-off controls, metadata-only visible polling, old-server fallback, dirty edits, gaps, uncertain writes and cleanup.');
