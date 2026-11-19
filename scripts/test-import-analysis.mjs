import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { loadIcon } from './frontend-icon-test-loader.mjs';
import { loadHelpDetails } from './frontend-help-test-loader.mjs';
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
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm, '') + '\nmodule.exports={importMessages,importTelegram,parseImport,PLATFORM_NAMES};';
let responder = null;
runInNewContext(clientSource, { module, TextEncoder, URLSearchParams, AbortController, DOMException, transport: { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError },
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

function harness() {
  const slots = []; let cursor = 0, dirty = true, effects = [], tree, unmounted = false;
  const hooks = {
    useState(initial) { const i = cursor++; if (!(i in slots)) slots[i] = initial; return [slots[i], (value) => { assert.equal(unmounted, false, 'late operation updated an unmounted panel'); const next = typeof value === 'function' ? value(slots[i]) : value; if (!Object.is(slots[i], next)) { slots[i] = next; dirty = true; } }]; },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { current: initial }; return slots[i]; },
    useEffect(callback, deps) { const i = cursor++, prior = slots[i]; if (!prior || deps.some((x, j) => !Object.is(x, prior.deps[j]))) effects.push(() => { prior?.cleanup?.(); slots[i] = { deps, cleanup: callback() }; }); },
  };
  const props = { token: 'synthetic-local-token', limit: 1000, onClose() {}, onImported() {}, onAuthFailure() { throw Error('unexpected auth failure'); } };
  const component = { exports: {} };
  const jsx = (type, supplied, ...children) => {
    const props = { ...(supplied ?? {}), children };
    return typeof type === 'function' && (type === Icon || type === HelpDetails) ? type(props) : { type, props };
  };
  const Icon = loadIcon(jsx);
  const HelpDetails = loadHelpDetails(jsx);
  runInNewContext(compiled.code + '\nmodule.exports=ImportPanel;', { ...actualClient, ...hooks, useLayoutEffect: hooks.useEffect, Error, ServiceError, AbortController, jsx, Icon, HelpDetails, Fragment: 'fragment', module: component,
    getProfileStatus: async () => structuredClone(currentModel),
  });
  return { get tree() { return tree; }, async flush() { for (let pass = 0; pass < 25; pass++) { if (dirty) { cursor = 0; dirty = false; tree = component.exports(props); const ready = effects; effects = []; for (const effect of ready) effect(); } await Promise.resolve(); } }, unmount() { for (const slot of slots) slot?.cleanup?.(); unmounted = true; } };
}
function nodes(value) { return value && typeof value === 'object' ? [value, ...value.props.children.flat(Infinity).flatMap(nodes)] : []; }
function text(value) { if (Array.isArray(value)) return value.map(text).join(''); return value && typeof value === 'object' ? value.props.children.map(text).join('') : String(value ?? ''); }
function defaultClosedText(value) {
  if (Array.isArray(value)) return value.map(defaultClosedText).join('');
  if (!value || typeof value !== 'object') return String(value ?? '');
  const children = value.props.children.flat(Infinity);
  return value.type === 'details' && !value.props.open
    ? children.filter((node) => node?.type === 'summary').map(text).join('')
    : children.map(defaultClosedText).join('');
}
function find(panel, match) { const result = nodes(panel.tree).find(match); assert.ok(result, 'import control missing'); return result; }
async function preview(panel) {
  await panel.flush();
  find(panel, n => n.type === 'input' && n.props.type === 'file').props.onChange({ target: { files: [{ size: 200, text: async () => JSON.stringify(sample) }] } });
  await panel.flush();
  await find(panel, n => n.type === 'form').props.onSubmit({ preventDefault() {} });
  await panel.flush();
}
const confirm = panel => find(panel, n => n.type === 'button' && text(n).includes('确认导入'));
const checkbox = panel => find(panel, n => n.type === 'input' && n.props.type === 'checkbox');

const manual = harness();
await preview(manual);
assert.ok(nodes(manual.tree).some((node) => node.type === 'svg' && node.props['data-icon'] === 'import' && node.props['aria-hidden'] === 'true' && node.props.focusable === 'false'), 'ImportPanel must render the shipped decorative SVG');
assert.equal(checkbox(manual).props.checked, false, 'AI must default off');
const beforeManual = requests.length;
await confirm(manual).props.onClick();
await manual.flush();
assert.equal(requests.length, beforeManual + 1);
assert.equal(JSON.parse(requests.at(-1).body).ai_organize, undefined, 'manual imports must not authorize AI');
assert.ok(text(manual.tree).includes('已导入 1 条'), 'import counts remain available on demand');
assert.ok(!defaultClosedText(manual.tree).includes('已导入 1 条'), 'successful import counts start folded');
assert.ok(!defaultClosedText(manual.tree).includes('AI 整理已排队'), 'manual import must not imply background AI work');
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
assert.ok(defaultClosedText(panel.tree).includes('AI 整理已排队：1 个会话；进度见联系人档案。'), 'queued AI work remains visible outside successful import details');
assert.ok(!defaultClosedText(panel.tree).includes('后台会分批读取完整历史'), 'supplementary processing guidance remains on demand');
panel.unmount();

const zeroQueue = harness();
await preview(zeroQueue);
checkbox(zeroQueue).props.onChange({ target: { checked: true } });
await zeroQueue.flush();
response = { ...response, ai_queued: 0 };
await confirm(zeroQueue).props.onClick();
await zeroQueue.flush();
assert.ok(text(zeroQueue.tree).includes('0 个会话的后台分析任务'));
assert.ok(!defaultClosedText(zeroQueue.tree).includes('AI 整理已排队'), 'zero queued tasks must not claim pending work');
zeroQueue.unmount();

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
console.log('Import analysis synthetic checks passed: explicit model-bound scope, default-off consent, one atomic request, visible queue result, no stale retry, and model-change reconfirmation.');
