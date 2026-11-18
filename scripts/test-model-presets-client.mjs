import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';

// Execute only the shipped typed API client + real auth passthrough with local
// synthetic responses. No UI, account, saved configuration or provider IO.
class ServiceError extends Error {
  constructor(message, code, status) { super(message); this.code = code; this.status = status; }
}
const ref = 'a'.repeat(40);
const fixture = {
  protocol: 'openai_chat_completions', model_id_required: true, live_verification_performed: false,
  items: [
    ['openai', 'https://api.openai.com/v1', true, 'protocol_supported'],
    ['deepseek', 'https://api.deepseek.com', true, 'protocol_supported'],
    ['gemini', 'https://generativelanguage.googleapis.com/v1beta/openai', true, 'protocol_supported'],
    ['dashscope', 'https://dashscope.aliyuncs.com/compatible-mode/v1', true, 'protocol_supported'],
    ['custom', '', true, 'template_only'],
    ['loopback', 'http://127.0.0.1:11434/v1', false, 'template_only'],
  ].map(([id, base_url, api_key_required, implementation_status]) => ({
    id, display_name: `Synthetic ${id}`, base_url, api_key_required, implementation_status,
    source_url: `https://github.com/synthetic/fixture/blob/${ref}/README.md`, source_ref: ref, source_license: 'Apache-2.0',
  })),
};
const requests = [], timers = new Map();
let nextTimer = 1, response = fixture, status = 200;
const defaultFetch = async () => ({ ok: status >= 200 && status < 300, status, json: async () => structuredClone(response) });
let fetchImpl = defaultFetch;
const transport = { SERVICE: { url: 'http://127.0.0.1:17890', error: null }, ServiceError };
const source = stripTypeScriptTypes(readFileSync(new URL('../apps/desktop/src/model-config-api.ts', import.meta.url), 'utf8'))
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource)
  .replace(/^export /gm, '') + '\nmodule.exports={getModelPresets,getModelConfig,saveModelConfig};';
const module = { exports: {} };
runInNewContext(source, {
  module, transport, AbortController, DOMException, URL,
  window: {
    setTimeout(callback, milliseconds) { const id = nextTimer++; timers.set(id, { callback, milliseconds }); return id; },
    clearTimeout(id) { assert.ok(timers.delete(id), 'request timer must be removed exactly once'); },
  },
  fetch: async (url, options) => {
    if (options.signal.aborted) throw new DOMException('synthetic aborted request', 'AbortError');
    requests.push({ url, ...options });
    return fetchImpl(url, options);
  },
});
const api = module.exports;
const signal = () => new AbortController().signal;
const get = () => api.getModelPresets('synthetic-local-token', signal());
assert.deepEqual(await get(), fixture);
assert.equal(timers.size, 0);
const request = requests.at(-1);
assert.equal(request.url, 'http://127.0.0.1:17890/api/v1/ai/model-presets');
assert.equal(request.method, 'GET');
assert.equal(request.headers.Authorization, 'Bearer synthetic-local-token');
assert.equal(request.headers.Accept, 'application/json');
assert.equal(request.headers['Content-Type'], undefined);
assert.equal(request.body, undefined);
assert.equal(request.credentials, 'omit');
assert.equal(request.cache, 'no-store');
assert.equal(request.redirect, 'error');
assert.equal(new URL(request.url).search, '');
await api.getModelPresets('synthetic-local-token', signal(), { api_key: 'synthetic-forbidden-key', base_url: 'https://example.com' });
assert.equal(requests.at(-1).method, 'GET');
assert.equal(requests.at(-1).body, undefined, 'preset reader must never serialize an extra credential/config argument');

const corruptions = [
  value => { value.protocol = 'anthropic_messages'; },
  value => { value.model_id_required = false; },
  value => { value.live_verification_performed = true; },
  value => { value.connected = true; },
  value => { value.api_key = 'synthetic-secret-must-not-render'; },
  value => { delete value.protocol; },
  value => { value.items = null; },
  value => { value.items = []; },
  value => { value.items = Array.from({ length: 17 }, (_, i) => ({ ...value.items[0], id: `synthetic-${i}` })); },
  value => { value.items[1].id = value.items[0].id; },
  value => { value.items[0].id = '../outside'; },
  value => { value.items[0].display_name = ''; },
  value => { value.items[0].display_name = 'a'.repeat(81); },
  value => { value.items[0].display_name = 'name\u0000hidden'; },
  value => { value.items[0].base_url = 'a'.repeat(2049); },
  value => { value.items[0].base_url = 'javascript:alert(1)'; },
  value => { value.items[0].base_url = 'http://example.com/v1'; },
  value => { value.items[0].base_url = 'http://localhost:11434/v1'; },
  value => { value.items[0].base_url = 'https://user:synthetic-secret@example.com/v1'; },
  value => { value.items[0].base_url = 'https://example.com/v1?api_key=synthetic-secret'; },
  value => { value.items[0].base_url = 'https://example.com/v1#hidden'; },
  value => { value.items[0].base_url = 'https://example.com/%2e'; },
  value => { value.items[0].base_url = 'https://example.com/v1?'; },
  value => { value.items[0].base_url = 'https://example.com/v1#'; },
  value => { value.items[0].base_url = 'https://example.com\\\\v1'; },
  value => { value.items[0].base_url = 'http://2130706433:11434/v1'; },
  value => { value.items[0].base_url = 'http://0x7f000001:11434/v1'; },
  value => { value.items[0].base_url = 'http://127.0.0.1:011434/v1'; },
  value => { value.items[0].api_key_required = 'true'; },
  value => { value.items[0].implementation_status = 'connected'; },
  value => { value.items[0].api_key = 'synthetic-secret-must-not-render'; },
  value => { value.items[0].model = 'invented-model-id'; },
  value => { value.items[0].source_url = 'javascript:alert(1)'; },
  value => { value.items[0].source_url = `http://github.com/synthetic/fixture/blob/${ref}`; },
  value => { value.items[0].source_url = `https://github.com.evil.example/synthetic/fixture/blob/${ref}`; },
  value => { value.items[0].source_url = `https://synthetic-secret@github.com/synthetic/fixture/blob/${ref}`; },
  value => { value.items[0].source_url += '?secret=synthetic-secret'; },
  value => { value.items[0].source_url += '#hidden'; },
  value => { value.items[0].source_url += '?'; },
  value => { value.items[0].source_url += '#'; },
  value => { value.items[0].source_url = `https://github.com/synthetic/fixture/blob/${ref}/%2e/README.md`; },
  value => { value.items[0].source_url = 'https://github.com/synthetic/fixture/blob/main/README.md'; },
  value => { value.items[0].source_ref = 'b'.repeat(40); },
  value => { value.items[0].source_ref = 'g'.repeat(40); },
  value => { value.items[0].source_ref = 'a'.repeat(41); },
  value => { value.items[0].source_license = ''; },
  value => { delete value.items[0].source_license; },
  value => { value.items[0] = null; },
];
for (const [index, corrupt] of corruptions.entries()) {
  response = structuredClone(fixture); corrupt(response);
  await assert.rejects(get(), error => error.code === 'INVALID_RESPONSE' && !error.message.includes('synthetic-secret'), `malformed preset fixture ${index}`);
  assert.equal(timers.size, 0);
}
for (const invalid of [null, [], 'synthetic-secret', true]) {
  response = invalid;
  await assert.rejects(get(), error => error.code === 'INVALID_RESPONSE');
}
for (const baseURL of ['http://[::1]:11434/v1', 'http://127.0.0.1:80/v1']) {
  response = structuredClone(fixture);
  response.items[5].base_url = baseURL;
  assert.equal((await get()).items[5].base_url, baseURL);
}
response = structuredClone(fixture);
response.items.push({ ...response.items[4], id: 'synthetic-future-reference', implementation_status: 'unsupported' });
assert.equal((await get()).items.at(-1).implementation_status, 'unsupported', 'unsupported must stay distinct from supported and template-only');

for (const [failureStatus, code, expected] of [
  [400, 'AI_PRESETS_INVALID', 'AI_PRESETS_INVALID'],
  [401, 'DEVICE_UNAUTHORIZED', 'UNAUTHORIZED'],
  [403, 'SCOPE_DENIED', 'SCOPE_DENIED'],
  [409, 'ASSISTANT_CONFIG_CHANGED', 'ASSISTANT_CONFIG_CHANGED'],
  [503, 'SYNTHETIC_UNKNOWN', 'SYNTHETIC_UNKNOWN'],
]) {
  status = failureStatus; response = { error: { code, message: 'synthetic-secret-raw-provider-response' } };
  const count = requests.length;
  await assert.rejects(get(), error => error.code === expected && error.status === failureStatus && !error.message.includes('synthetic-secret'));
  assert.equal(requests.length, count + 1, 'preset reads must not retry auth/scope/config/backend failures');
}
status = 200; response = fixture;
fetchImpl = async () => ({ ok: true, status: 200, json: async () => { throw new Error('synthetic-secret-invalid-JSON'); } });
await assert.rejects(get(), error => error.code === 'INVALID_RESPONSE' && !error.message.includes('synthetic-secret'));
fetchImpl = async () => { throw new Error('synthetic-secret-network-error'); };
await assert.rejects(get(), error => error.code === 'NETWORK_ERROR' && !error.message.includes('synthetic-secret'));
fetchImpl = defaultFetch;
transport.SERVICE.error = 'Synthetic local service address misconfiguration';
const beforeConfigError = requests.length;
await assert.rejects(get(), error => error.code === 'CONFIG_ERROR');
assert.equal(requests.length, beforeConfigError);
transport.SERVICE.error = null;

const aborted = new AbortController(); aborted.abort();
await assert.rejects(api.getModelPresets('synthetic-local-token', aborted.signal), error => error.code === 'ABORTED');
async function until(predicate) {
  for (let i = 0; i < 20 && !predicate(); i++) await Promise.resolve();
  assert.ok(predicate(), 'synthetic async boundary was not reached');
}
for (const mode of ['abort', 'timeout']) {
  let releaseJSON;
  fetchImpl = async () => ({ ok: true, status: 200, json: () => new Promise(resolve => { releaseJSON = resolve; }) });
  const controller = new AbortController();
  const pending = api.getModelPresets('synthetic-local-token', controller.signal);
  await until(() => releaseJSON !== undefined);
  assert.equal(timers.size, 1);
  const timer = [...timers.values()][0];
  assert.equal(timer.milliseconds, 12_000);
  if (mode === 'abort') controller.abort(); else timer.callback();
  releaseJSON(structuredClone(fixture));
  await assert.rejects(pending, error => error.code === (mode === 'abort' ? 'ABORTED' : 'TIMEOUT'));
  assert.equal(timers.size, 0, 'late parsed responses must not escape cancellation or leak timers');
}
fetchImpl = defaultFetch;
// The shared transport refactor must preserve the established config methods.
response = { base_url: 'https://api.deepseek.com', model: 'synthetic-model', daily_limit: 50, key_configured: true, revision: 1, configured: true, model_config_id: 'b'.repeat(64), sending_enabled: false };
assert.equal((await api.getModelConfig('synthetic-local-token', signal())).revision, 1);
assert.equal(requests.at(-1).url, 'http://127.0.0.1:17890/api/v1/ai/model-config');
const save = { expected_revision: 1, base_url: response.base_url, model: response.model, api_key: 'synthetic-ephemeral-key', daily_limit: 50, confirmed: true };
await api.saveModelConfig('synthetic-local-token', signal(), { ...save, source_url: 'must-not-be-submitted' });
assert.equal(requests.at(-1).method, 'PATCH');
assert.deepEqual(JSON.parse(requests.at(-1).body), save);
assert.equal(timers.size, 0);
console.log('Model preset API checks passed: six static references, strict schema/provenance, authenticated GET-only transport, no credential serialization, failure/cancellation/timeout safety, and legacy config GET/PATCH. Synthetic data only.');
