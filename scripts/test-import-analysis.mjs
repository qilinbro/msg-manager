import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Exercise the shipped import client and the real ImportPanel with synthetic
// files, responses and hooks. No browser, local service, credential or chat.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const sample = { platform: 'qq', account_id: 'synthetic-self', account_name: '合成账号', messages: [{ external_id: 'import-one', peer_id: 'synthetic-peer', peer_name: '合成昵称', direction: 'incoming', body: '合成工作资料', occurred_at: '2026-01-01T00:00:00Z' }] };
const configured = { model_configured: true, model_config_id: 'synthetic-model-1', provider: '合成模型', model: 'synthetic', endpoint_host: 'model.example.test' };
let currentModel = { ...configured };
let response = { inserted: 1, duplicates: 0, conversations: 1, conversation_ids: ['conversation-1'], ai_queued: 1 };
let responseStatus = 200;
const requests = [];
const module = { exports: {} };
const clientSource = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/inbox-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/profile-api';\r?\n/m, 'const isProfileCategory=()=>true;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm, '') + '\nmodule.exports={importMessages,importTelegram,parseImport,PLATFORM_NAMES,isShoppingPlatform,getInboxInfo,getConversations};';
let responder = null;
runInNewContext(clientSource, { module, Error, TextEncoder, URLSearchParams, AbortController, DOMException, transport: { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError },
  window: { setTimeout: () => 1, clearTimeout() {} }, fetch: async (url, options) => {
    requests.push({ url, ...options });
    if (responder) return responder(url, options);
    return { ok: responseStatus === 200, status: responseStatus, json: async () => structuredClone(response) };
  },
});
const actualClient = module.exports;
const source = readFileSync(new URL('../apps/desktop/src/Inbox.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
  .replace('export default function Inbox', 'function Inbox');
const compiled = transformSync('Inbox.tsx', source, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
assert.equal(compiled.errors.length, 0);

function harness(overrides = {}) {
  const slots = []; let cursor = 0, dirty = true, effects = [], tree, unmounted = false;
  const hooks = {
    useState(initial) { const i = cursor++; if (!(i in slots)) slots[i] = initial; return [slots[i], (value) => { assert.equal(unmounted, false, 'late operation updated an unmounted panel'); const next = typeof value === 'function' ? value(slots[i]) : value; if (!Object.is(slots[i], next)) { slots[i] = next; dirty = true; } }]; },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { current: initial }; return slots[i]; },
    useEffect(callback, deps) { const i = cursor++, prior = slots[i]; if (!prior || deps.some((x, j) => !Object.is(x, prior.deps[j]))) effects.push(() => { prior?.cleanup?.(); slots[i] = { deps, cleanup: callback() }; }); },
  };
  const props = { token: 'synthetic-local-token', limit: 1000, onClose() {}, onImported() {}, onAuthFailure() { throw Error('unexpected auth failure'); }, ...overrides };
  const component = { exports: {} };
  runInNewContext(compiled.code + '\nmodule.exports=ImportPanel;', { ...actualClient, ...hooks, useLayoutEffect: hooks.useEffect, Error, ServiceError, AbortController, jsx: (type, supplied, ...children) => ({ type, props: { ...(supplied ?? {}), children } }), Fragment: 'fragment', module: component,
    getProfileStatus: async () => structuredClone(currentModel),
  });
  return { get tree() { return tree; }, async flush() { for (let pass = 0; pass < 25; pass++) { if (dirty) { cursor = 0; dirty = false; tree = component.exports(props); const ready = effects; effects = []; for (const effect of ready) effect(); } await Promise.resolve(); } }, unmount() { for (const slot of slots) slot?.cleanup?.(); unmounted = true; } };
}
function nodes(value) { return value && typeof value === 'object' ? [value, ...value.props.children.flat(Infinity).flatMap(nodes)] : []; }
function text(value) { if (Array.isArray(value)) return value.map(text).join(''); return value && typeof value === 'object' ? value.props.children.map(text).join('') : String(value ?? ''); }
function find(panel, match) { const result = nodes(panel.tree).find(match); assert.ok(result, 'import control missing'); return result; }
async function preview(panel, input = sample) {
  await panel.flush();
  find(panel, n => n.type === 'input' && n.props.type === 'file').props.onChange({ target: { files: [{ size: 200, text: async () => JSON.stringify(input) }] } });
  await panel.flush();
  await find(panel, n => n.type === 'form').props.onSubmit({ preventDefault() {} });
  await panel.flush();
}
const confirm = panel => find(panel, n => n.type === 'button' && text(n).includes('确认导入'));
const checkbox = panel => find(panel, n => n.type === 'input' && n.props.type === 'checkbox');

const manual = harness();
await preview(manual);
assert.equal(checkbox(manual).props.checked, false, 'AI must default off');
const beforeManual = requests.length;
await confirm(manual).props.onClick();
await manual.flush();
assert.equal(requests.length, beforeManual + 1);
assert.equal(JSON.parse(requests.at(-1).body).ai_organize, undefined, 'manual imports must not authorize AI');
manual.unmount();

const panel = harness();
await preview(panel);
assert.ok(text(panel.tree).includes('model.example.test'));
assert.ok(text(panel.tree).includes('平台账号ID'));
checkbox(panel).props.onChange({ target: { checked: true } });
await panel.flush();
const before = requests.length;
await confirm(panel).props.onClick();
await panel.flush();
assert.equal(requests.length, before + 1, 'messages and profile jobs must use one atomic request');
assert.equal(requests.at(-1).url.endsWith('/imports/messages'), true);
assert.equal(requests.at(-1).headers.Authorization, 'Bearer synthetic-local-token');
assert.deepEqual(JSON.parse(requests.at(-1).body).ai_organize, { confirmed: true, model_config_id: configured.model_config_id });
assert.ok(text(panel.tree).includes('1 个会话的后台分析任务'));
panel.unmount();

const stale = harness();
await preview(stale);
checkbox(stale).props.onChange({ target: { checked: true } });
await stale.flush();
responseStatus = 409; response = { error: { code: 'AI_CONSENT_REQUIRED', message: 'private upstream text' } };
const staleBefore = requests.length;
await confirm(stale).props.onClick();
await stale.flush();
assert.equal(requests.length, staleBefore + 1, 'failed/uncertain imports must not retry');
assert.ok(text(stale.tree).includes('重新检查模型'));
assert.equal(text(stale.tree).includes('private upstream text'), false);
currentModel = { ...configured, model_config_id: 'synthetic-model-2', endpoint_host: 'changed.example.test' };
find(stale, n => n.type === 'button' && text(n) === '检查模型状态').props.onClick();
await stale.flush();
assert.equal(checkbox(stale).props.checked, false, 'a new model target needs fresh confirmation');
stale.unmount();

responseStatus = 200;
response = { inserted: 1, duplicates: 0, conversations: 1 };
await assert.rejects(actualClient.importMessages('synthetic-token', new AbortController().signal, { ...sample, ai_organize: { confirmed: true, model_config_id: configured.model_config_id } }), error => error.code === 'INVALID_RESPONSE');
const shoppingIDs = ['xianyu', 'taobao', 'jd', 'pinduoduo'];
for (const platform of shoppingIDs) {
  const shop = harness({ initialPlatform: platform, availablePlatforms: shoppingIDs });
  const data = { ...sample, platform, account_id: `self:${platform}`, messages: [{ ...sample.messages[0], external_id: `${platform}:message-1`, peer_id: `${platform}:peer-1` }] };
  const beforeShop = requests.length;
  await preview(shop, data);
  assert.equal(checkbox(shop).props.checked, false);
  assert.ok(text(shop.tree).includes('不需要登录凭据'));
  assert.equal(requests.length, beforeShop, 'file preview cannot import or log in');
  await confirm(shop).props.onClick(); await shop.flush();
  const body = JSON.parse(requests.at(-1).body);
  assert.equal(requests.length, beforeShop + 1);
  assert.equal(requests.at(-1).url, 'http://127.0.0.1:17890/api/v1/imports/messages');
  assert.equal(body.platform, platform); assert.equal(body.account_id, data.account_id);
  assert.equal(body.messages[0].peer_id, data.messages[0].peer_id);
  assert.equal(body.messages[0].external_id, data.messages[0].external_id);
  assert.equal(body.messages[0].conversation_kind, 'private');
  assert.equal(body.ai_organize, undefined);
  shop.unmount();
}
const oldBackend = harness({ initialPlatform: 'xianyu', availablePlatforms: ['qq'] });
const beforeOld = requests.length;
await preview(oldBackend, { ...sample, platform: 'xianyu' });
assert.ok(text(oldBackend.tree).includes('更新并重启本地服务'));
assert.equal(nodes(oldBackend.tree).some(n => n.type === 'button' && text(n).includes('确认导入')), false);
assert.equal(requests.length, beforeOld, 'unsupported old backend never receives a shopping import');
oldBackend.unmount();
for (const mismatched of [{ ...sample, platform: 'taobao' }, { type: 'personal_chat', messages: [] }]) {
  const mismatch = harness({ initialPlatform: 'xianyu', availablePlatforms: shoppingIDs });
  const beforeMismatch = requests.length;
  await preview(mismatch, mismatched);
  assert.ok(text(mismatch.tree).includes('文件来源与所选闲鱼不一致'));
  assert.equal(nodes(mismatch.tree).some(n => n.type === 'button' && text(n).includes('确认导入')), false);
  assert.equal(requests.length, beforeMismatch);
  mismatch.unmount();
}
const shoppingGroup = harness({ initialPlatform: 'xianyu', availablePlatforms: shoppingIDs });
await preview(shoppingGroup, { ...sample, platform: 'xianyu', messages: [{ ...sample.messages[0], conversation_kind: 'group' }] });
assert.ok(text(shoppingGroup.tree).includes('仅支持私聊文本'), text(shoppingGroup.tree));
assert.equal(nodes(shoppingGroup.tree).some(n => n.type === 'button' && text(n).includes('确认导入')), false);
shoppingGroup.unmount();
const explicitChange = harness({ initialPlatform: 'xianyu', availablePlatforms: shoppingIDs });
await explicitChange.flush();
find(explicitChange, n => n.type === 'select' && n.props.value === 'xianyu').props.onChange({ target: { value: 'taobao' } });
await explicitChange.flush();
await preview(explicitChange, { ...sample, platform: 'taobao' });
assert.ok(confirm(explicitChange), 'explicitly changing the selected platform permits its matching file');
explicitChange.unmount();
response = { storage_enabled: true, source_mode: 'import', platforms: shoppingIDs.map((id) => ({ id, name: actualClient.PLATFORM_NAMES[id], live_status: 'import_only', message_count: 0 })), import_limit: 1000 };
assert.equal((await actualClient.getInboxInfo('synthetic', new AbortController().signal)).platforms.length, 4);
response = { items: [], next_cursor: null };
for (const platform of shoppingIDs) {
  await actualClient.getConversations('synthetic', new AbortController().signal, { platform, archived: false });
  assert.equal(new URL(requests.at(-1).url).searchParams.get('platform'), platform);
  assert.equal(requests.at(-1).method, 'GET');
  assert.equal(requests.at(-1).body, undefined);
}
console.log('Import analysis synthetic checks passed: explicit model-bound scope, default-off consent, one atomic request, visible queue result, no stale retry, and model-change reconfirmation.');
