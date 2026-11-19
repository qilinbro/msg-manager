import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { loadIcon } from './frontend-icon-test-loader.mjs';
import { loadHelpDetails } from './frontend-help-test-loader.mjs';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Shipped client/component, synthetic data only. Never call a model or platform.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const value = {
  base_url: 'https://api.deepseek.com', model: 'deepseek-chat', daily_limit: 50,
  key_configured: false, revision: 0, configured: false, model_config_id: '', sending_enabled: false,
};
let response = structuredClone(value), code = 200;
const requests = [];
const source = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/model-config-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource)
  .replace(/^export /gm, '') + '\nmodule.exports={getModelConfig,saveModelConfig};';
const module = { exports: {} };
runInNewContext(source, {
  module, transport: { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError },
  AbortController, DOMException, URL, window: { setTimeout: () => 1, clearTimeout() {} },
  fetch: async (url, options) => {
    requests.push({ url, ...options });
    return { ok: code === 200, status: code, json: async () => structuredClone(response) };
  },
});
const api = module.exports, signal = () => new AbortController().signal;
assert.equal((await api.getModelConfig('synthetic-token', signal())).revision, 0);
assert.equal(requests.at(-1).headers.Authorization, 'Bearer synthetic-token');
assert.equal(requests.at(-1).method, 'GET');
const input = { expected_revision: 0, base_url: value.base_url, model: value.model, daily_limit: 50, api_key: 'synthetic-ephemeral-key', confirmed: true };
await api.saveModelConfig('synthetic-token', signal(), { ...input, platform_token: 'must-not-enter-request' });
assert.equal(requests.at(-1).method, 'PATCH');
assert.deepEqual(JSON.parse(requests.at(-1).body), input);
assert.equal(requests.at(-1).credentials, 'omit');
assert.equal(new URL(requests.at(-1).url).search, '');
response = { ...value, api_key: 'must-never-be-returned' };
await assert.rejects(api.getModelConfig('synthetic-token', signal()), e => e.code === 'INVALID_RESPONSE');
code = 409; response = { error: { code: 'AI_CONFIG_STALE', message: 'private provider raw data' } };
await assert.rejects(api.saveModelConfig('synthetic-token', signal(), input), e => e.code === 'AI_CONFIG_STALE' && !e.message.includes('private'));

const presetValue = {
  protocol: 'openai_chat_completions', model_id_required: true, live_verification_performed: false,
  items: [
    { id: 'openai', display_name: 'OpenAI', base_url: 'https://api.openai.com/v1', api_key_required: true, implementation_status: 'protocol_supported', source_url: 'https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md', source_ref: 'd5292055f5d00d99d949017745a81d31cd186222', source_license: 'Apache-2.0' },
    { id: 'gemini', display_name: 'Gemini（OpenAI 兼容）', base_url: 'https://generativelanguage.googleapis.com/v1beta/openai', api_key_required: true, implementation_status: 'protocol_supported', source_url: 'https://github.com/google-gemini/cookbook/blob/853a69ac49e21a917e7cdd178cadac193652c627/quickstarts/Get_started_OpenAI_Compatibility.ipynb', source_ref: '853a69ac49e21a917e7cdd178cadac193652c627', source_license: 'Apache-2.0' },
    { id: 'loopback', display_name: '本机兼容服务', base_url: 'http://127.0.0.1:11434/v1', api_key_required: false, implementation_status: 'template_only', source_url: 'https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md', source_ref: 'd5292055f5d00d99d949017745a81d31cd186222', source_license: 'Apache-2.0' },
    { id: 'unimplemented', display_name: '未实现的协议（测试）', base_url: 'https://api.example.test/v1', api_key_required: true, implementation_status: 'unsupported', source_url: 'https://github.com/openai/openai-go/blob/d5292055f5d00d99d949017745a81d31cd186222/README.md', source_ref: 'd5292055f5d00d99d949017745a81d31cd186222', source_license: 'Apache-2.0' },
  ],
};
function jsx(type, supplied, ...children) {
  const props = { ...(supplied ?? {}), children };
  return typeof type === 'function' ? type(props) : { type, props };
}
const Icon = loadIcon(jsx, 'fragment');
const raw = readFileSync(new URL('../apps/desktop/src/AIModelPanel.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
  .replace('export function AIModelPanel', 'function AIModelPanel');
const transformed = transformSync('AIModelPanel.tsx', raw, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
assert.equal(transformed.errors.length, 0);
function harness(bindings = {}, initialProps = {}) {
  const slots = [], saved = [], reads = [];
  let cursor = 0, dirty = true, tree, effects = [], unmounted = false, authFailures = 0;
  let props = { token: 'synthetic-token', onAuthFailure() { authFailures++; }, ...initialProps };
  const hooks = {
    useState(initial) {
      const i = cursor++; if (!(i in slots)) slots[i] = typeof initial === 'function' ? initial() : initial;
      return [slots[i], v => {
        assert.equal(unmounted, false, 'state mutation after unmount');
        const next = typeof v === 'function' ? v(slots[i]) : v;
        if (!Object.is(next, slots[i])) { slots[i] = next; dirty = true; }
      }];
    },
    useRef(initial) { const i = cursor++; if (!(i in slots)) slots[i] = { current: initial }; return slots[i]; },
    useEffect(callback, deps) {
      const i = cursor++, old = slots[i];
      if (!old || deps.some((v, j) => !Object.is(v, old.deps[j]))) effects.push(() => { old?.cleanup?.(); slots[i] = { deps, cleanup: callback() }; });
    },
  };
  const component = { exports: {} };
  runInNewContext(transformed.code + '\nmodule.exports=AIModelPanel;', {
    module: component, ...hooks, ServiceError, AbortController, Icon, HelpDetails: loadHelpDetails(jsx), jsx, Fragment: 'fragment',
    getModelConfig: async (token, signal) => { reads.push({ kind: 'config', token, signal }); return structuredClone(value); },
    getModelPresets: async (token, signal) => { reads.push({ kind: 'presets', token, signal }); return structuredClone(presetValue); },
    saveModelConfig: async (token, signal, input) => {
      saved.push({ token, signal, ...structuredClone(input) });
      return { ...value, base_url: input.base_url, model: input.model, daily_limit: input.daily_limit, configured: true, key_configured: true, revision: input.expected_revision + 1, model_config_id: 'synthetic-config' };
    },
    ...bindings,
  });
  return {
    get tree() { return tree; }, get authFailures() { return authFailures; }, saved, reads,
    setProps(next) { props = { ...props, ...next }; dirty = true; },
    async flush() {
      for (let i = 0; i < 30; i++) {
        if (dirty) { cursor = 0; dirty = false; tree = component.exports(props); const queue = effects; effects = []; for (const e of queue) e(); }
        await Promise.resolve();
      }
    },
    unmount() { for (const slot of slots) slot?.cleanup?.(); unmounted = true; },
  };
}
function nodes(t) {
  if (Array.isArray(t)) return t.flatMap(nodes);
  if (!t || typeof t !== 'object') return [];
  return [t, ...nodes(t.props?.children)];
}
function text(t) {
  if (Array.isArray(t)) return t.map(text).join('');
  if (typeof t === 'string' || typeof t === 'number') return String(t);
  return t && typeof t === 'object' ? text(t.props?.children) : '';
}
function field(panel, id) { const n = nodes(panel.tree).find(n => n.props.id === id); assert.ok(n, 'missing ' + id); return n; }
function button(panel, name) {
  const n = nodes(panel.tree).find(n => n.type === 'button' && (n.props['aria-label'] || text(n).trim()) === name);
  assert.ok(n, 'missing button ' + name); return n;
}
function check(panel) { return nodes(panel.tree).find(n => n.type === 'input' && n.props.type === 'checkbox'); }
async function editField(panel, id, value) { field(panel, id).props.onChange({ target: { value } }); await panel.flush(); }
async function consent(panel) { check(panel).props.onChange({ target: { checked: true } }); await panel.flush(); }
async function submit(panel) { await nodes(panel.tree).find(n => n.type === 'form').props.onSubmit({ preventDefault() {} }); await panel.flush(); }
const deferred = () => { let resolve, reject; const promise = new Promise((yes, no) => { resolve = yes; reject = no; }); return { promise, resolve, reject }; };

// No hidden model IDs, no save/probe on metadata arrival, real decorative SVG.
const panel = harness(); await panel.flush();
assert.equal(field(panel, 'ai-model-name').props.value, '', 'an unconfigured default is not a verified current model');
assert.equal(field(panel, 'ai-model-key').props.type, 'password');
assert.equal(panel.reads.filter(r => r.kind === 'presets').length, 1);
assert.equal(panel.reads.find(r => r.kind === 'presets').token, 'synthetic-token');
assert.equal(panel.saved.length, 0);
assert.ok(nodes(panel.tree).some(n => n.type === 'svg' && n.props['data-icon'] === 'save' && n.props['aria-hidden'] === 'true'));
assert.ok(text(panel.tree).includes('协议支持不等于服务已接通'));
assert.equal(field(panel, 'ai-model-preset').props.children.flat().find(n => n?.type === 'option' && n.props.value === 'unimplemented').props.disabled, true);
await editField(panel, 'ai-model-name', 'synthetic-model');
await editField(panel, 'ai-model-key', 'synthetic-ui-key'); await consent(panel); await submit(panel);
assert.equal(panel.saved.at(-1).api_key, 'synthetic-ui-key');
assert.equal(panel.saved.at(-1).expected_revision, 0);
assert.equal(field(panel, 'ai-model-key').props.value, '', 'successful save clears plaintext key');
assert.equal(check(panel).props.checked, false);
await editField(panel, 'ai-model-key', 'must-not-cross-preset'); await consent(panel);
field(panel, 'ai-model-preset').props.onChange({ target: { value: 'gemini' } }); await panel.flush();
assert.equal(field(panel, 'ai-model-url').props.value, presetValue.items[1].base_url);
assert.equal(field(panel, 'ai-model-name').props.value, '');
assert.equal(field(panel, 'ai-model-key').props.value, '');
assert.equal(check(panel).props.checked, false);
assert.equal(panel.saved.length, 1, 'selection must not write or execute a model call');
assert.equal(button(panel, '保存模型配置').props.disabled, true);
const sourceLink = nodes(panel.tree).find(n => n.type === 'a');
assert.equal(sourceLink.props.href, presetValue.items[1].source_url);
assert.equal(sourceLink.props.rel, 'noopener noreferrer');
assert.equal(sourceLink.props.referrerPolicy, 'no-referrer');
await editField(panel, 'ai-model-name', 'synthetic-gemini-model'); await editField(panel, 'ai-model-key', 'new-target-key'); await consent(panel);
await editField(panel, 'ai-model-limit', '60'); assert.equal(check(panel).props.checked, false, 'quota edits revoke save consent');
await consent(panel); await editField(panel, 'ai-model-key', 'changed-key'); assert.equal(check(panel).props.checked, false, 'key edits revoke save consent');
await consent(panel); await editField(panel, 'ai-model-url', 'https://custom.example.test/v1');
assert.equal(field(panel, 'ai-model-preset').props.value, '');
assert.equal(field(panel, 'ai-model-name').props.value, '');
assert.equal(field(panel, 'ai-model-key').props.value, '');
assert.equal(check(panel).props.checked, false);
await consent(panel); await submit(panel); assert.equal(panel.saved.length, 1, 'blank model cannot bypass native required via direct submit');
field(panel, 'ai-model-preset').props.onChange({ target: { value: 'loopback' } }); await panel.flush();
assert.ok(text(panel.tree).includes('仅配置模板，需自行核验兼容性'));
assert.equal(field(panel, 'ai-model-key').props.placeholder, '免密本机服务可留空');
assert.equal(field(panel, 'ai-model-key').props.value, '', 'loopback selection must not inherit the cloud key');
assert.equal(check(panel).props.checked, false);
panel.unmount();

// Failed saves preserve retry input; failures never masquerade as saved state.
let failSave = true; const failures = [];
const retry = harness({ saveModelConfig: async (_token, _signal, input) => {
  failures.push(structuredClone(input));
  if (failSave) throw new ServiceError('synthetic failure', 'AI_CONFIG_UNAVAILABLE');
  return { ...value, configured: true, key_configured: true, model: input.model, revision: 1, model_config_id: 'synthetic-config' };
} });
await retry.flush(); await editField(retry, 'ai-model-name', 'synthetic-retry-model');
await editField(retry, 'ai-model-key', 'synthetic-retry-key'); await consent(retry); await submit(retry);
assert.equal(field(retry, 'ai-model-key').props.value, 'synthetic-retry-key');
assert.equal(failures.at(-1).expected_revision, 0);
assert.ok(nodes(retry.tree).some(n => n.props.role === 'alert' && text(n).includes('synthetic failure')));
failSave = false; await submit(retry);
assert.equal(field(retry, 'ai-model-key').props.value, ''); retry.unmount();

// Metadata is independent: late arrivals cannot replace saved or typed values.
const late = deferred(), presetSignals = [];
const existing = { ...value, base_url: 'https://saved.example.test/v1', model: 'saved-model', configured: true, key_configured: true, revision: 7, model_config_id: 'saved-config', daily_limit: 80 };
const delayed = harness({ getModelConfig: async () => structuredClone(existing), getModelPresets: (_token, signal) => { presetSignals.push(signal); return late.promise; } });
await delayed.flush();
assert.equal(field(delayed, 'ai-model-url').props.value, existing.base_url);
assert.equal(field(delayed, 'ai-model-name').props.value, existing.model);
assert.equal(field(delayed, 'ai-model-preset').props.disabled, true);
await editField(delayed, 'ai-model-url', 'https://typed.example.test/v1');
await editField(delayed, 'ai-model-name', 'typed-model'); await editField(delayed, 'ai-model-key', 'typed-key'); await consent(delayed);
late.resolve(structuredClone(presetValue)); await delayed.flush();
assert.equal(field(delayed, 'ai-model-url').props.value, 'https://typed.example.test/v1');
assert.equal(field(delayed, 'ai-model-name').props.value, 'typed-model');
assert.equal(field(delayed, 'ai-model-key').props.value, 'typed-key');
assert.equal(check(delayed).props.checked, true);
assert.equal(delayed.saved.length, 0);
await submit(delayed); assert.equal(delayed.saved.at(-1).expected_revision, 7, 'preset metadata never changes config CAS');
delayed.unmount(); assert.equal(presetSignals[0].aborted, true);

// Metadata errors allow existing/manual config; retry does not execute a save.
let presetAttempt = 0;
const failedPresets = harness({ getModelConfig: async () => structuredClone(existing), getModelPresets: async () => { if (++presetAttempt === 1) throw new Error('provider diagnostic must not leak'); return structuredClone(presetValue); } });
await failedPresets.flush();
assert.ok(text(failedPresets.tree).includes('预设未能读取'));
assert.ok(!text(failedPresets.tree).includes('provider diagnostic'));
assert.equal(field(failedPresets, 'ai-model-url').props.disabled, false);
await consent(failedPresets); assert.equal(button(failedPresets, '保存模型配置').props.disabled, false);
button(failedPresets, '重新读取预设').props.onClick(); await failedPresets.flush();
assert.equal(presetAttempt, 2); assert.equal(failedPresets.saved.length, 0);
assert.equal(field(failedPresets, 'ai-model-name').props.value, existing.model);
assert.equal(check(failedPresets).props.checked, true);
failedPresets.unmount();

// Auth failures, identity loss, stale metadata and unmount are fail-closed.
const unauthorized = harness({ getModelPresets: async () => { throw new ServiceError('unauthorized', 'UNAUTHORIZED', 401); } });
await unauthorized.flush(); assert.equal(unauthorized.authFailures, 1); unauthorized.unmount();
const pending = deferred(); const staleSignals = [];
const stale = harness({ getModelPresets: (_token, signal) => { staleSignals.push(signal); return pending.promise; } });
await stale.flush(); await editField(stale, 'ai-model-name', 'must-clear-on-disconnect'); await editField(stale, 'ai-model-key', 'must-clear-key');
stale.setProps({ token: '' }); await stale.flush();
assert.equal(staleSignals[0].aborted, true);
assert.equal(field(stale, 'ai-model-url').props.value, '');
assert.equal(field(stale, 'ai-model-name').props.value, '');
assert.equal(field(stale, 'ai-model-key').props.value, '');
pending.resolve(structuredClone(presetValue)); await stale.flush();
assert.equal(field(stale, 'ai-model-preset').props.disabled, true); stale.unmount();
const abandoned = deferred(), abandonedSignals = [];
const unmounted = harness({ getModelPresets: (_token, signal) => { abandonedSignals.push(signal); return abandoned.promise; } });
await unmounted.flush(); unmounted.unmount(); abandoned.resolve(structuredClone(presetValue));
for (let i = 0; i < 8; i++) await Promise.resolve();
assert.equal(abandonedSignals[0].aborted, true);
const disconnected = harness({}, { token: '' }); await disconnected.flush();
assert.equal(disconnected.reads.length, 0); disconnected.unmount();
console.log('AI model checks passed: authenticated GET/PATCH, strict write-only secret/CAS, independent read-only presets, manual model IDs, no auto-save/probe, target isolation, consent reset, error/retry, stale/abort/unmount and real Lucide rendering. Synthetic data only.');
