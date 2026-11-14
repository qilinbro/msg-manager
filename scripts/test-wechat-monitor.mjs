import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Exercise shipped clients/components with synthetic transport and React hooks.
// No browser, real service, clipboard, credentials or account files are used.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const readResult = { inserted: 1, duplicates: 0, conversations: 1, skipped: 0, limited: false, read_at: '2026-01-01T00:00:00Z', read_only: true, sending_enabled: false };
const monitor = { enabled: false, peer_ids: ['wxid_synthetic_peer'], interval_seconds: 30, state: 'disabled', last_cycle_at: null, last_error_code: null, last_result: null, read_only: true, sending_enabled: false };
const status = { configured: true, state: 'ready', account_id: 'wxid_synthetic', last_read_at: null, last_result: null, read_only: true, sending_enabled: false, monitor };
let response = structuredClone(status);
let responseStatus = 200;
let fetchAction = null;
const requests = [];
const timeouts = new Map();
let timeoutID = 0;
const apiModule = { exports: {} };
const apiSource = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/wechat-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^export /gm, '') + '\nmodule.exports={getWechatStatus,getWechatSessions,readWechatMessages,saveWechatMonitor};';
runInNewContext(apiSource, {
  module: apiModule, AbortController, DOMException, Date, Number,
  transport: { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError },
  window: { setTimeout: (callback) => { timeouts.set(++timeoutID, callback); return timeoutID; }, clearTimeout: (id) => timeouts.delete(id) },
  fetch: async (url, options) => {
    requests.push({ url, ...options });
    if (fetchAction) return fetchAction(url, options);
    return { ok: responseStatus >= 200 && responseStatus < 300, status: responseStatus, json: async () => structuredClone(response) };
  },
}, { filename: 'wechat-api.ts' });
const api = apiModule.exports;
const signal = () => new AbortController().signal;
let legacy = structuredClone(status); delete legacy.monitor;
response = legacy;
assert.equal((await api.getWechatStatus('synthetic', signal())).monitor, null, 'old backend must retain manual access');
assert.equal(requests.at(-1).method, 'GET');
assert.equal(requests.at(-1).body, undefined);
response = structuredClone(status);
assert.equal((await api.getWechatStatus('synthetic', signal())).monitor.interval_seconds, 30);
response.monitor.sending_enabled = true;
await assert.rejects(api.getWechatStatus('synthetic', signal()), (error) => error.code === 'INVALID_RESPONSE');
response = { ...structuredClone(monitor), enabled: true, state: 'waiting' };
await api.saveWechatMonitor('synthetic', signal(), { enabled: true, peer_ids: ['wxid_synthetic_peer'], interval_seconds: 30, key: 'must-not-be-sent' });
const saved = requests.at(-1);
assert.equal(saved.method, 'POST');
assert.equal(saved.url, 'http://127.0.0.1:17890/api/v1/connections/wechat-db/monitor');
assert.deepEqual(JSON.parse(saved.body), { enabled: true, peer_ids: ['wxid_synthetic_peer'], interval_seconds: 30 });
assert.equal(saved.headers.Authorization, 'Bearer synthetic');
assert.equal(saved.credentials, 'omit');
assert.equal(saved.cache, 'no-store');
const beforeInvalid = requests.length;
for (const settings of [
  { enabled: true, peer_ids: [], interval_seconds: 30 },
  { enabled: true, peer_ids: ['wxid_synthetic_peer'], interval_seconds: 9 },
  { enabled: true, peer_ids: ['wxid_synthetic_peer', 'wxid_synthetic_peer'], interval_seconds: 30 },
  { enabled: true, peer_ids: ['../another-account'], interval_seconds: 30 },
]) await assert.rejects(api.saveWechatMonitor('synthetic', signal(), settings), (error) => error.code === 'INVALID_INPUT');
assert.equal(requests.length, beforeInvalid, 'invalid settings must not reach the service');
responseStatus = 501; response = { error: { code: 'NOT_IMPLEMENTED', message: 'untrusted upstream detail' } };
await assert.rejects(api.saveWechatMonitor('synthetic', signal(), monitor), /更新后重启后台/);
responseStatus = 503; response = { error: { code: 'WECHAT_TIMEOUT', message: 'untrusted source path or key' } };
await assert.rejects(api.getWechatSessions('synthetic', signal()), (error) => error.code === 'WECHAT_TIMEOUT' && /读取超时/.test(error.message) && !/untrusted/.test(error.message));
responseStatus = 200;
fetchAction = async () => { throw Error('synthetic connection loss'); };
const beforeLoss = requests.length;
await assert.rejects(api.saveWechatMonitor('synthetic', signal(), monitor), (error) => error.code === 'NETWORK_ERROR' && /可能已经保存/.test(error.message));
assert.equal(requests.length, beforeLoss + 1, 'uncertain mutations must not retry');
fetchAction = async (_url, options) => new Promise((_resolve, reject) => options.signal.addEventListener('abort', () => reject(new DOMException('synthetic', 'AbortError')), { once: true }));
const timed = api.saveWechatMonitor('synthetic', signal(), monitor);
const timedCheck = assert.rejects(timed, (error) => error.code === 'TIMEOUT');
for (const callback of [...timeouts.values()]) callback();
await timedCheck;
assert.equal(timeouts.size, 0);

function hookHarness(file, componentName, bindings, props) {
  const slots = []; let cursor = 0; let dirty = true; let tree; let effects = []; const intervals = new Map(); let intervalID = 0;
  const listeners = new Map();
  const addListener = (event, callback) => { if (!listeners.has(event)) listeners.set(event, new Set()); listeners.get(event).add(callback); };
  const removeListener = (event, callback) => listeners.get(event)?.delete(callback);
  const jsx = (type, supplied, ...children) => ({ type, props: { ...(supplied ?? {}), children } });
  const hookBindings = {
    useState(initial) { const index = cursor++; if (!(index in slots)) slots[index] = typeof initial === 'function' ? initial() : initial; return [slots[index], (value) => { const next = typeof value === 'function' ? value(slots[index]) : value; if (!Object.is(next, slots[index])) { slots[index] = next; dirty = true; } }]; },
    useRef(initial) { const index = cursor++; if (!(index in slots)) slots[index] = { current: initial }; return slots[index]; },
    useEffect(callback, dependencies) { const index = cursor++; const previous = slots[index]; if (!previous || dependencies.some((value, i) => !Object.is(value, previous.dependencies[i]))) { effects.push(() => { previous?.cleanup?.(); slots[index] = { dependencies, cleanup: callback() }; }); } },
  };
  hookBindings.useLayoutEffect = hookBindings.useEffect;
  const source = readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8')
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
    .replace(`export default function ${componentName}`, `function ${componentName}`);
  const transformed = transformSync(file, source, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(transformed.errors.length, 0);
  const componentModule = { exports: {} };
  runInNewContext(transformed.code + `\nmodule.exports=${componentName};`, {
    ...bindings, ...hookBindings, jsx, Fragment: 'fragment', module: componentModule, AbortController, ServiceError,
    document: { visibilityState: 'visible', addEventListener: addListener, removeEventListener: removeListener },
    window: { addEventListener: addListener, removeEventListener: removeListener, setInterval: (callback, ms) => { intervals.set(++intervalID, { callback, ms }); return intervalID; }, clearInterval: (id) => intervals.delete(id), setTimeout: (callback) => { callback(); return 1; }, clearTimeout() {} },
  }, { filename: file });
  return {
    async flush() { for (let pass = 0; pass < 20; pass++) { if (dirty) { dirty = false; cursor = 0; tree = componentModule.exports(props); const scheduled = effects; effects = []; for (const effect of scheduled) effect(); } await Promise.resolve(); } },
    get tree() { return tree; },
    intervals,
    dispatch(event) { for (const listener of listeners.get(event) ?? []) listener(); },
    unmount() { for (const slot of slots) slot?.cleanup?.(); },
  };
}
function nodes(tree) { if (!tree || typeof tree !== 'object') return []; return [tree, ...tree.props.children.flat(Infinity).flatMap(nodes)]; }
function text(tree) { if (Array.isArray(tree)) return tree.map(text).join(''); return tree && typeof tree === 'object' ? tree.props.children.map(text).join('') : String(tree ?? ''); }
function find(harness, predicate) { const node = nodes(harness.tree).find(predicate); assert.ok(node, 'Expected rendered control is missing'); return node; }

let componentStatus = structuredClone(status);
const calls = [];
const saves = [];
let softRefreshes = 0;
const panel = hookHarness('WechatPanel.tsx', 'WechatPanel', {
  getWechatStatus: async (_token, requestSignal) => { calls.push({ action: 'status', signal: requestSignal }); return structuredClone(componentStatus); },
  getWechatSessions: async () => { calls.push({ action: 'sessions' }); return [{ peer_id: 'wxid_synthetic_peer', name: '合成联系人' }]; },
  readWechatMessages: async () => { calls.push({ action: 'read' }); return structuredClone(readResult); },
  saveWechatMonitor: async (_token, _signal, settings) => { saves.push(structuredClone(settings)); componentStatus.monitor = { ...componentStatus.monitor, ...settings, state: settings.enabled ? 'waiting' : 'disabled' }; return structuredClone(componentStatus.monitor); },
  wechatMonitorError: () => 'synthetic error',
}, { token: 'synthetic', storageEnabled: true, revision: 0, onSynced() {}, onBackgroundSynced() { softRefreshes++; }, onAuthFailure() { throw Error('unexpected authentication failure'); } });
await panel.flush();
const tick = async () => { for (const timer of panel.intervals.values()) { assert.equal(timer.ms, 5000); timer.callback(); } await panel.flush(); };
await tick();
assert.ok(calls.every((call) => call.action === 'status'), 'mount and polling must never start a source read');
assert.equal(saves.length, 0, 'mount must not enable automation');
const intervalInput = find(panel, (node) => node.props.id === 'wechat-monitor-interval');
intervalInput.props.onChange({ target: { value: '60' } }); await panel.flush(); await tick();
assert.equal(find(panel, (node) => node.props.id === 'wechat-monitor-interval').props.value, '60', 'polling must preserve unsaved settings');
componentStatus.monitor.last_result = structuredClone(readResult); componentStatus.monitor.last_cycle_at = readResult.read_at;
await tick(); await tick();
assert.equal(softRefreshes, 1, 'an unchanged result must not repeatedly refresh messages');
componentStatus.monitor.last_result = { ...readResult, inserted: 0, read_at: '2026-01-01T00:01:00Z' };
await tick(); assert.equal(softRefreshes, 1, 'zero new messages must preserve the reading view');
panel.dispatch('visibilitychange'); await panel.flush();
assert.equal(softRefreshes, 2, 'returning to a throttled tab must reconcile messages even after a duplicate-only cycle');
assert.ok(calls.every((call) => call.action === 'status'), 'returning to the page must only read cached status');
componentStatus.state = 'reading'; componentStatus.monitor.enabled = true; componentStatus.monitor.state = 'reading';
await tick();
const pause = find(panel, (node) => node.type === 'button' && text(node) === '暂停后台读取');
assert.equal(pause.props.disabled, false, 'background reads must not disable the pause control');
await pause.props.onClick(); await panel.flush();
assert.equal(saves.length, 1); assert.equal(saves[0].enabled, false);
panel.unmount();
assert.equal(panel.intervals.size, 0);
assert.equal(saves.length, 1, 'leaving the page must never mutate monitor settings');

let inboxMessages = [{ id: 'message-1', conversation_id: 'conversation-1', external_id: 'synthetic-1', direction: 'incoming', body: 'synthetic initial message', occurred_at: '2026-01-01T00:00:00Z', read: false, source: 'wechatdb' }];
const conversation = { id: 'conversation-1', connection_id: 'connection-1', platform: 'weixin', account_id: 'wxid_synthetic', peer_id: 'wxid_synthetic_peer', title: 'Synthetic contact', last_message: 'synthetic initial message', last_message_at: '2026-01-01T00:00:00Z', unread_count: 1, pinned: false, archived: false };
let historyReads = 0;
const inbox = hookHarness('Inbox.tsx', 'Inbox', {
  getInboxInfo: async () => ({ storage_enabled: true, source_mode: 'mixed', platforms: [{ id: 'weixin', name: '微信', live_status: 'configured', message_count: inboxMessages.length }], import_limit: 1000 }),
  getConversations: async () => ({ items: [structuredClone(conversation)], next_cursor: 'synthetic-list-cursor' }),
  getConversationMessages: async () => { historyReads++; return { items: structuredClone(inboxMessages), next_cursor: 'synthetic-message-cursor' }; },
  updateConversation: async () => structuredClone(conversation),
  importMessages() {}, importTelegram() {}, parseImport() {},
  PLATFORM_NAMES: { qq: 'QQ', weixin: '微信', telegram: 'Telegram', shanliao: '闪聊', email: '邮箱' },
  EmailPanel: 'EmailPanelStub', WechatPanel: 'WechatPanelStub', AIPanel: 'AIPanelStub',
}, { token: 'synthetic', aiDrafts: new Map(), onSettings() {}, onAuthFailure() { throw Error('unexpected authentication failure'); } });
await inbox.flush();
find(inbox, (node) => node.type === 'button' && node.props.className === 'inbox-conversation ').props.onClick({ currentTarget: { isConnected: true } });
await inbox.flush();
const viewportNode = find(inbox, (node) => node.props.ref && node.props.className === 'inbox-history');
const viewport = { scrollHeight: 1000, scrollTop: 800, clientHeight: 200 };
viewportNode.props.ref.current = viewport;
const reconcile = async () => { find(inbox, (node) => node.type === 'WechatPanelStub').props.onBackgroundSynced(); await inbox.flush(); };
inboxMessages.push({ ...inboxMessages[0], id: 'message-2', external_id: 'synthetic-2', body: 'synthetic new message' });
await reconcile();
assert.ok(text(inbox.tree).includes('synthetic new message'), 'latest view must display newly saved messages');
assert.equal(find(inbox, (node) => node.type === 'button' && String(node.props.className).includes('inbox-conversation selected')).props['aria-current'], 'true', 'soft refresh must retain the selected conversation');
viewport.scrollTop = 100;
const beforeScrolled = historyReads;
inboxMessages.push({ ...inboxMessages[0], id: 'message-3', external_id: 'synthetic-3', body: 'synthetic later message' });
await reconcile();
assert.equal(historyReads, beforeScrolled, 'background refresh must not replace scrolled history');
assert.equal(viewport.scrollTop, 100, 'background refresh must preserve reading position');
assert.ok(!text(inbox.tree).includes('synthetic later message'));
assert.ok(text(inbox.tree).includes('查看最新消息'));
viewport.scrollTop = 800;
find(inbox, (node) => node.type === 'button' && node.props['aria-controls'] === 'ai-management-conversation-1').props.onClick();
await inbox.flush();
const beforeEditing = historyReads;
await reconcile();
assert.equal(historyReads, beforeEditing, 'background refresh must not replace messages under an open AI editor');
assert.ok(find(inbox, (node) => node.type === 'AIPanelStub'), 'AI editor must remain mounted');
inbox.unmount();

console.log('WeChat monitor client/component and inbox soft-refresh checks passed: authenticated transport, old service compatibility, uncertain-result handling, cached polling, settings preservation, result deduplication, focus recovery, pause/unmount, selected chat and reading/editor preservation. Synthetic data only.');
