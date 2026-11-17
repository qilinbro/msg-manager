import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const base = { conversation_id: 'source-conversation', candidates: [{ conversation_id: 'target-conversation', display_name: 'Synthetic contact', peer_id: 'synthetic-peer', platform: 'qq', reason: 'same_nickname' }], linked: [], document_markdown: '# Synthetic source document', truncated: false };
let response = structuredClone(base), responseStatus = 200;
const requests = [];
const source = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/profile-links-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm, '') + '\nmodule.exports={getProfileLinks,confirmProfileLink,removeProfileLink};';
const module = { exports: {} };
runInNewContext(source, { module, AbortController, DOMException, transport: { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError },
  window: { setTimeout: () => 1, clearTimeout() {} },
  fetch: async (url, options) => {
    requests.push({ url, ...options });
    const result = options.method === 'POST'
      ? { ...base, linked: [{ ...base.candidates[0], reason: 'confirmed' }], candidates: [] }
      : options.method === 'DELETE' ? structuredClone(base) : structuredClone(response);
    return { ok: responseStatus < 300, status: responseStatus, json: async () => structuredClone(result) };
  },
});
const api = module.exports;
const signal = () => new AbortController().signal;
assert.equal((await api.getProfileLinks('synthetic-token', signal(), base.conversation_id)).candidates[0].reason, 'same_nickname');
assert.equal(requests.at(-1).method, 'GET');
assert.equal(requests.at(-1).headers.Authorization, 'Bearer synthetic-token');
assert.equal(requests.at(-1).credentials, 'omit');
assert.equal(requests.at(-1).cache, 'no-store');
await api.confirmProfileLink('synthetic-token', signal(), base.conversation_id, 'target-conversation');
assert.equal(requests.at(-1).method, 'POST');
assert.deepEqual(JSON.parse(requests.at(-1).body), { target_conversation_id: 'target-conversation', confirmed: true });
await api.removeProfileLink('synthetic-token', signal(), base.conversation_id, 'target-conversation');
assert.match(requests.at(-1).url, /profile-links\/target-conversation$/);
assert.equal(requests.at(-1).method, 'DELETE');
const beforeInvalid = requests.length;
await assert.rejects(api.confirmProfileLink('synthetic-token', signal(), base.conversation_id, '../foreign'), error => error.code === 'INVALID_INPUT');
assert.equal(requests.length, beforeInvalid);
response = { ...base, conversation_id: 'foreign' };
await assert.rejects(api.getProfileLinks('synthetic-token', signal(), base.conversation_id), error => error.code === 'INVALID_RESPONSE');
responseStatus = 401; response = { error: { code: 'UNAUTHORIZED', message: 'private upstream details' } };
await assert.rejects(api.getProfileLinks('synthetic-token', signal(), base.conversation_id), error => error.code === 'UNAUTHORIZED' && !error.message.includes('private'));

function createPanel(bindings) {
  const slots = []; let cursor = 0, dirty = true, tree, effects = [], dead = false;
  const hooks = {
    useState(initial) { const index = cursor++; if (!(index in slots)) slots[index] = initial; return [slots[index], value => { assert.equal(dead, false, 'unmounted links UI cannot set state'); const next = typeof value === 'function' ? value(slots[index]) : value; if (!Object.is(next, slots[index])) { slots[index] = next; dirty = true; } }]; },
    useRef(initial) { const index = cursor++; if (!(index in slots)) slots[index] = { current: initial }; return slots[index]; },
    useEffect(callback, dependencies) { const index = cursor++, previous = slots[index]; if (!previous || dependencies.some((value, i) => !Object.is(value, previous.dependencies[i]))) effects.push(() => { previous?.cleanup?.(); slots[index] = { dependencies, cleanup: callback() }; }); },
  };
  const raw = readFileSync(new URL('../apps/desktop/src/ProfileLinksPanel.tsx', import.meta.url), 'utf8').replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '').replace('export default function ProfileLinksPanel', 'function ProfileLinksPanel');
  const transformed = transformSync('ProfileLinksPanel.tsx', raw, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(transformed.errors.length, 0);
  const component = { exports: {} };
  runInNewContext(transformed.code + '\nmodule.exports=ProfileLinksPanel;', { module: component, ...hooks, ...bindings, ServiceError, AbortController, PLATFORM_NAMES: { qq: 'QQ' }, Fragment: 'fragment', jsx: (type, supplied, ...children) => ({ type, props: { ...(supplied ?? {}), children } }) });
  const props = { token: 'synthetic-token', conversationId: base.conversation_id, onAuthFailure() { throw Error('unexpected auth failure'); } };
  return { get tree() { return tree; }, async flush() { for (let i = 0; i < 30; i++) { if (dirty) { cursor = 0; dirty = false; tree = component.exports(props); const queue = effects; effects = []; for (const effect of queue) effect(); } await Promise.resolve(); } }, unmount() { for (const slot of slots) slot?.cleanup?.(); dead = true; } };
}
const nodes = tree => !tree || typeof tree !== 'object' ? [] : [tree, ...tree.props.children.flat(Infinity).flatMap(nodes)];
const text = tree => Array.isArray(tree) ? tree.map(text).join('') : tree && typeof tree === 'object' ? tree.props.children.map(text).join('') : String(tree ?? '');
const find = (panel, predicate) => { const result = nodes(panel.tree).find(predicate); assert.ok(result, 'missing profile-link control'); return result; };
let current = structuredClone(base), writes = 0, pendingResolve;
const calls = [];
const panel = createPanel({
  getProfileLinks: async () => { calls.push('GET'); return structuredClone(current); },
  confirmProfileLink: async (_token, _signal, source, target) => { writes++; assert.equal(source, base.conversation_id); assert.equal(target, 'target-conversation'); current = { ...current, linked: [{ ...base.candidates[0], reason: 'confirmed' }], candidates: [] }; return structuredClone(current); },
  removeProfileLink: async () => { writes++; current = structuredClone(base); return structuredClone(current); },
});
await panel.flush();
assert.equal(writes, 0, 'loading same-name suggestions cannot associate identities');
assert.ok(text(panel.tree).includes('相同昵称只是建议'));
const submit = () => find(panel, node => node.type === 'button' && text(node) === '确认关联聊天档案');
assert.equal(submit().props.disabled, true);
find(panel, node => node.type === 'select').props.onChange({ target: { value: 'target-conversation' } });
await panel.flush();
assert.equal(submit().props.disabled, true, 'selecting a suggestion is not confirmation');
find(panel, node => node.type === 'input' && node.props.type === 'checkbox').props.onChange({ target: { checked: true } });
await panel.flush();
assert.equal(submit().props.disabled, false);
find(panel, node => node.type === 'form').props.onSubmit({ preventDefault() {} });
await panel.flush();
assert.equal(writes, 1);
assert.ok(text(panel.tree).includes('原聊天和各自AI权限仍独立保留'));
find(panel, node => node.type === 'button' && text(node) === '解除关联').props.onClick();
await panel.flush();
assert.equal(writes, 2);
assert.ok(text(panel.tree).includes('已解除关联'));
panel.unmount();
const delayed = createPanel({ getProfileLinks: async () => new Promise(resolve => { pendingResolve = resolve; }) });
await delayed.flush();
delayed.unmount(); pendingResolve(base); await delayed.flush();
console.log('Profile link client/component checks passed: scoped transport, explicit identity confirmation, reversible association, preserved source permissions, and unmount guards. Synthetic data only.');
