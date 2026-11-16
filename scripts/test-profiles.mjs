import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Real shipped clients/components, synthetic HTTP and hooks only.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const status = { model_configured: true, search_configured: true, model_config_id: 'synthetic-model', model: 'synthetic', provider: 'compatible', endpoint_host: 'model.example.test', daily_limit: 20, used_today: 0, search_daily_limit: 10, search_used_today: 0, sending_enabled: false };
const profile = {
  conversation_id: 'conversation-1', contact_id: 'contact-1', display_name: 'Synthetic contact', platform: 'weixin',
  public_name: '', company: '', role: '', manual_notes: '', category_override: '', auto_organize: false, public_search_enabled: false,
  model_config_id: '', revision: 1, processed_seq: 0, max_seq: 20, category: 'unclassified', ai_category: 'unclassified', tags: [], summary: '', evidence_ids: [],
  facts: [{ id: 'fact-1', field: 'company', value: 'Synthetic company', state: 'unverified', origin: 'public', message_ids: [], source_ids: ['source-1'] }],
  sources: [{ id: 'source-1', url: 'https://public.example.test/about', title: 'Synthetic public source', snippet: 'Synthetic source excerpt', content_hash: 'synthetic-hash', retrieved_at: '2026-01-01T00:00:00Z' }],
  jobs: [], updated_at: null, document_markdown: '# Synthetic document\n\n待核实', truncated: false, ai_paused: false,
};
const job = { id: 'job-1', conversation_id: 'conversation-1', kind: 'organize', state: 'queued', attempts: 0, next_attempt_at: null, lease_until: null, model_config_id: 'synthetic-model', settings_revision: 1, max_seq: 20, last_error_code: null, updated_at: '2026-01-01T00:00:00Z' };
const service = { url: 'http://127.0.0.1:17890', error: null };
let response = structuredClone(profile), responseStatus = 200, fetchAction = null;
const requests = [], timeouts = new Map(); let timeoutID = 0;
const transport = { SERVICE: service, ServiceError };
function client(file, exports, extras = {}) {
  const module = { exports: {} };
  const source = stripTypeScriptTypes(readFileSync(new URL(`../apps/desktop/src/${file}`, import.meta.url), 'utf8'))
    .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
    .replace(/^import .* from '\.\/profile-api';\r?\n/m, 'const {isProfileCategory}=profileDependency;\n')
    .replace(/^export /gm, '') + `\nmodule.exports={${exports}};`;
  runInNewContext(source, { module, transport, TextEncoder, URL, URLSearchParams, AbortController, DOMException,
    window: { setTimeout: (callback) => { timeouts.set(++timeoutID, callback); return timeoutID; }, clearTimeout: (id) => timeouts.delete(id) },
    fetch: async (url, options) => { requests.push({ url, ...options }); if (fetchAction) return fetchAction(url, options); return { ok: responseStatus >= 200 && responseStatus < 300, status: responseStatus, json: async () => structuredClone(response) }; }, ...extras,
  }, { filename: file });
  return module.exports;
}
const api = client('profile-api.ts', 'getProfileStatus,getProfile,saveProfile,generateProfile,enrichProfile,setProfileFactState,safeProfileSourceURL,isProfileCategory,PROFILE_CATEGORIES,CATEGORY_NAMES,profileErrorMessage,profileJobError');
const signal = () => new AbortController().signal;
response = status;
assert.equal((await api.getProfileStatus('synthetic-token', signal())).model_config_id, 'synthetic-model');
assert.equal(requests.at(-1).method, 'GET');
response = profile;
assert.equal((await api.getProfile('synthetic-token', signal(), 'conversation-1')).facts[0].state, 'unverified', 'public matches must never become confirmed on read');
assert.equal(requests.at(-1).headers.Authorization, 'Bearer synthetic-token');
assert.equal(requests.at(-1).credentials, 'omit');
assert.equal(requests.at(-1).cache, 'no-store');
response = { ...profile, conversation_id: 'other-conversation' };
await assert.rejects(api.getProfile('synthetic-token', signal(), 'conversation-1'), (error) => error.code === 'INVALID_RESPONSE');
const beforeBadID = requests.length;
await assert.rejects(api.getProfile('synthetic-token', signal(), '../other'), (error) => error.code === 'INVALID_INPUT');
assert.equal(requests.length, beforeBadID);
const settings = { public_name: 'Synthetic name', company: 'Synthetic company', role: '', manual_notes: 'Synthetic note', category_override: 'colleague', auto_organize: false, public_search_enabled: false, expected_revision: 1, confirmed: false, model_config_id: '' };
response = { ...profile, ...settings };
await api.saveProfile('synthetic-token', signal(), 'conversation-1', { ...settings, api_key: 'must-not-be-sent' });
assert.deepEqual(JSON.parse(requests.at(-1).body), settings);
assert.equal(requests.at(-1).method, 'PATCH');
response = job; responseStatus = 202;
await api.generateProfile('synthetic-token', signal(), 'conversation-1', { expected_revision: 1, confirmed: true, model_config_id: 'synthetic-model', key: 'must-not-be-sent' });
assert.deepEqual(JSON.parse(requests.at(-1).body), { expected_revision: 1, confirmed: true, model_config_id: 'synthetic-model' });
assert.match(requests.at(-1).url, /\/profile\/generate$/);
await api.enrichProfile('synthetic-token', signal(), 'conversation-1', { expected_revision: 1, confirmed: true, model_config_id: 'synthetic-model' });
assert.match(requests.at(-1).url, /\/profile\/enrich$/);
responseStatus = 200; response = { ...profile, facts: [{ ...profile.facts[0], state: 'confirmed' }] };
await api.setProfileFactState('synthetic-token', signal(), 'conversation-1', 'fact-1', 'confirmed', 1);
assert.deepEqual(JSON.parse(requests.at(-1).body), { state: 'confirmed', expected_revision: 1 });
assert.match(requests.at(-1).url, /\/profile\/facts\/fact-1$/);
for (const unsafe of ['javascript:alert(1)', 'data:text/html,test', 'file:///secret', 'http://127.0.0.1:17890/private', 'http://host.local/private']) assert.equal(api.safeProfileSourceURL(unsafe), null);
assert.equal(api.safeProfileSourceURL('https://public.example.test/about'), 'https://public.example.test/about');
responseStatus = 501; response = { error: { code: 'NOT_IMPLEMENTED', message: 'private upstream detail' } };
await assert.rejects(api.getProfile('synthetic-token', signal(), 'conversation-1'), (error) => error.code === 'PROFILE_UNSUPPORTED' && /重启后台/.test(error.message) && !/private/.test(error.message));
responseStatus = 409; response = { error: { code: 'PROFILE_REVISION_CONFLICT', message: 'private upstream detail' } };
await assert.rejects(api.saveProfile('synthetic-token', signal(), 'conversation-1', settings), (error) => /输入已保留/.test(error.message));
responseStatus = 404; response = { error: { code: 'PROFILE_NOT_FOUND', message: 'private upstream detail' } };
await assert.rejects(api.getProfile('synthetic-token', signal(), 'conversation-1'), (error) => error.code === 'PROFILE_NOT_FOUND' && !/重启后台/.test(error.message));
fetchAction = async (_url, options) => new Promise((_resolve, reject) => options.signal.addEventListener('abort', () => reject(new DOMException('synthetic', 'AbortError')), { once: true }));
const beforeTimeout = requests.length;
const timeout = api.generateProfile('synthetic-token', signal(), 'conversation-1', { expected_revision: 1, confirmed: true, model_config_id: 'synthetic-model' });
const timeoutCheck = assert.rejects(timeout, (error) => error.code === 'TIMEOUT' && /后台可能/.test(error.message));
for (const callback of [...timeouts.values()]) callback();
await timeoutCheck;
assert.equal(requests.length, beforeTimeout + 1, 'uncertain job submission must not retry');
fetchAction = null; responseStatus = 200;
const inboxAPI = client('inbox-api.ts', 'getConversations', { profileDependency: api });
const conversation = { id: 'conversation-1', connection_id: 'connection-1', platform: 'weixin', account_id: 'synthetic-account', peer_id: 'synthetic-peer', title: 'Synthetic contact', last_message: '', last_message_at: '2026-01-01T00:00:00Z', unread_count: 0, pinned: false, archived: false };
response = { items: [conversation], next_cursor: null };
assert.equal((await inboxAPI.getConversations('synthetic-token', signal(), { category: 'friend', archived: false })).items[0].category, undefined, 'old service categories remain compatible');
assert.equal(new URL(requests.at(-1).url).searchParams.get('category'), 'friend');
response = { items: [{ ...conversation, category: 'unknown-category' }], next_cursor: null };
await assert.rejects(inboxAPI.getConversations('synthetic-token', signal(), { archived: false }), (error) => error.code === 'INVALID_RESPONSE');

function harness(bindings, props) {
  const slots = []; let cursor = 0, dirty = true, tree; let effects = []; const intervals = new Map(), listeners = new Map(); let timerID = 0; let afterUnmount = false;
  const add = (event, callback) => { if (!listeners.has(event)) listeners.set(event, new Set()); listeners.get(event).add(callback); };
  const remove = (event, callback) => listeners.get(event)?.delete(callback);
  const hooks = {
    useState(initial) { const i = cursor++; if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial; return [slots[i], (value) => { assert.equal(afterUnmount, false, 'unmounted panel cannot update state'); const next = typeof value === 'function' ? value(slots[i]) : value; if (!Object.is(next, slots[i])) { slots[i] = next; dirty = true; } }]; },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { current: initial }; return slots[i]; },
    useEffect(callback, dependencies) { const i = cursor++, previous = slots[i]; if (!previous || dependencies.some((value, j) => !Object.is(value, previous.dependencies[j]))) effects.push(() => { previous?.cleanup?.(); slots[i] = { dependencies, cleanup: callback() }; }); },
  };
  const source = readFileSync(new URL('../apps/desktop/src/ProfilePanel.tsx', import.meta.url), 'utf8').replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '').replace(/^import ['"][^'"]+['"];?\r?$/gm, '').replace('export default function ProfilePanel', 'function ProfilePanel');
  const transformed = transformSync('ProfilePanel.tsx', source, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(transformed.errors.length, 0);
  const component = { exports: {} };
  runInNewContext(transformed.code + '\nmodule.exports=ProfilePanel;', { ...bindings, ...hooks, ServiceError, TextEncoder, AbortController, module: component, Fragment: 'fragment', jsx: (type, supplied, ...children) => { const props = { ...(supplied ?? {}), children }; return typeof type === 'function' ? type(props) : { type, props }; }, document: { visibilityState: 'visible', addEventListener: add, removeEventListener: remove }, window: { addEventListener: add, removeEventListener: remove, setInterval: (callback, ms) => { intervals.set(++timerID, { callback, ms }); return timerID; }, clearInterval: (id) => intervals.delete(id) } }, { filename: 'ProfilePanel.tsx' });
  return { get tree() { return tree; }, intervals, async flush() { for (let i = 0; i < 25; i++) { if (dirty) { cursor = 0; dirty = false; tree = component.exports(props); const queue = effects; effects = []; for (const effect of queue) effect(); } await Promise.resolve(); } }, unmount() { for (const slot of slots) slot?.cleanup?.(); afterUnmount = true; } };
}
function nodes(tree) { if (!tree || typeof tree !== 'object') return []; return [tree, ...tree.props.children.flat(Infinity).flatMap(nodes)]; }
function textOf(tree) { if (Array.isArray(tree)) return tree.map(textOf).join(''); return tree && typeof tree === 'object' ? tree.props.children.map(textOf).join('') : String(tree ?? ''); }
function find(panel, predicate) { const result = nodes(panel.tree).find(predicate); assert.ok(result, 'Expected profile control missing'); return result; }
let currentProfile = structuredClone(profile), currentStatus = structuredClone(status);
const calls = [], savedSettings = [], factChanges = [];
const panel = harness({ ...api, ProfileLinksPanel: () => null,
  getProfile: async (_token, _signal, id) => { assert.equal(id, 'conversation-1'); calls.push('profile'); return structuredClone(currentProfile); },
  getProfileStatus: async () => { calls.push('status'); return structuredClone(currentStatus); },
  saveProfile: async (_token, _signal, _id, input) => { calls.push('save'); savedSettings.push(structuredClone(input)); currentProfile = { ...currentProfile, ...input, revision: currentProfile.revision + 1 }; return structuredClone(currentProfile); },
  generateProfile: async () => { calls.push('generate'); currentProfile.jobs = [structuredClone(job)]; return structuredClone(job); },
  enrichProfile: async () => { calls.push('enrich'); return { ...structuredClone(job), kind: 'enrich' }; },
  setProfileFactState: async (_token, _signal, _id, factID, state) => { calls.push('fact'); factChanges.push({ factID, state }); currentProfile = { ...currentProfile, revision: currentProfile.revision + 1, facts: currentProfile.facts.map((fact) => fact.id === factID ? { ...fact, state } : fact) }; return structuredClone(currentProfile); },
}, { token: 'synthetic-token', conversationId: 'conversation-1', displayName: 'Synthetic contact', onAuthFailure() { throw Error('unexpected auth failure'); }, onChanged() {} });
await panel.flush();
assert.deepEqual([...new Set(calls)].sort(), ['profile', 'status'], 'mount must only read cached data');
assert.ok(textOf(panel.tree).includes('不在场回执'), 'profile must show unattended receipt');
assert.ok(textOf(panel.tree).includes('尚未授权后台任务'), 'receipt must explain idle mode');
assert.equal(find(panel, (node) => node.type === 'input' && node.props.type !== 'checkbox').props.value, '', 'display names must not silently become public search identities');
find(panel, (node) => node.type === 'textarea').props.onChange({ target: { value: 'unsaved synthetic notes' } });
await panel.flush();
currentProfile = { ...currentProfile, revision: 2, manual_notes: 'remote update', summary: 'Synthetic updated summary' };
for (const timer of panel.intervals.values()) { assert.equal(timer.ms, 5000); timer.callback(); }
await panel.flush();
assert.equal(find(panel, (node) => node.type === 'textarea').props.value, 'unsaved synthetic notes', 'background updates must preserve dirty profile input');
assert.ok(textOf(panel.tree).includes('Synthetic updated summary'), 'background results should still update');
find(panel, (node) => node.type === 'button' && textOf(node) === '重新载入设置').props.onClick();
await panel.flush();
find(panel, (node) => node.type === 'textarea').props.onChange({ target: { value: 'saved synthetic notes' } });
await panel.flush();
await find(panel, (node) => node.type === 'form').props.onSubmit({ preventDefault() {} });
await panel.flush();
assert.equal(savedSettings.at(-1).confirmed, false, 'manual-only edits must not authorize a model');
assert.equal(savedSettings.at(-1).model_config_id, '');
assert.equal(savedSettings.at(-1).expected_revision, 2);
const checkboxes = () => nodes(panel.tree).filter((node) => node.type === 'input' && node.props.type === 'checkbox');
checkboxes()[2].props.onChange({ target: { checked: true } });
await panel.flush();
find(panel, (node) => node.type === 'button' && textOf(node) === '整理聊天与分类').props.onClick();
await panel.flush();
assert.equal(calls.filter((kind) => kind === 'generate').length, 1);
assert.ok(textOf(panel.tree).includes('等待后台处理'), 'queued job must be visible in receipt');
assert.ok(textOf(panel.tree).includes('离开页面后会继续'), 'receipt must explain unattended continuation');
assert.equal(currentProfile.facts[0].state, 'unverified');
find(panel, (node) => node.type === 'button' && textOf(node) === '确认信息').props.onClick();
await panel.flush();
assert.deepEqual(factChanges, [{ factID: 'fact-1', state: 'confirmed' }]);
assert.equal(nodes(panel.tree).filter((node) => node.type === 'button' && textOf(node) === '确认信息').length, 0, 'confirmed facts must leave the candidate action area');
currentProfile.ai_paused = true; currentProfile.auto_organize = true;
for (const timer of panel.intervals.values()) timer.callback();
await panel.flush();
assert.equal(find(panel, (node) => node.type === 'button' && textOf(node) === '整理聊天与分类').props.disabled, true);
find(panel, (node) => node.type === 'textarea').props.onChange({ target: { value: 'manual edit while AI paused' } });
await panel.flush();
assert.equal(find(panel, (node) => node.type === 'button' && textOf(node) === '保存档案设置').props.disabled, false, 'AI pause must not block local manual edits');
find(panel, (node) => node.type === 'form').props.onSubmit({ preventDefault() {} });
await panel.flush();
assert.equal(savedSettings.at(-1).confirmed, false);
assert.equal(savedSettings.at(-1).auto_organize, true, 'local edits during pause must not silently reconfigure existing authorization');
const beforeUnmount = calls.length;
panel.unmount();
assert.equal(panel.intervals.size, 0);
assert.equal(calls.length, beforeUnmount, 'unmount must not cancel queued jobs or submit writes');
console.log('Profile client/component synthetic checks passed: scoped authenticated APIs, explicit consent, candidate confirmation, safe sources, unknown results, categories, dirty preservation, paused manual edits and background-job lifecycle.');
