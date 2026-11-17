import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Synthetic client smoke checks only: no browser, service, account or token.
const panel = readFileSync(new URL('../apps/desktop/src/MilkyPanel.tsx', import.meta.url), 'utf8');
assert.match(panel, /next\.monitor\.peer_ids \?\? \[\]\)\.join/);
assert.match(panel, /pending_verification/);
assert.match(panel, /已保存，待重新核验/);
assert.match(panel, /清除已保存的后台接收/);
const transformedPanel = transformSync('MilkyPanel.tsx', panel, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(transformedPanel.errors.length, 0, 'MilkyPanel must compile in the synthetic client check');

// Render the shipped Settings tree and disconnected catalog with inert child
// components. A user must see exactly one QQ integration, even before connecting.
const appSource = readFileSync(new URL('../apps/desktop/src/App.tsx', import.meta.url), 'utf8')
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export default /gm, '')
  .replace(/^export /gm, '');
const transformedApp = transformSync('App.tsx', appSource, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(transformedApp.errors.length, 0);
const appModule = { exports: {} };
const MilkyPanel = () => null;
const jsx = (type, props, ...children) => ({ type, props: { ...props, children } });
runInNewContext(transformedApp.code + '\nmodule.exports={Settings,PLANNED_CONNECTORS};', {
  module: appModule, jsx, Fragment: 'fragment',
  MilkyPanel, AIModelPanel: () => null, DigestPanel: () => null,
  SERVICE: { url: 'http://127.0.0.1:17890', error: null },
});
assert.deepEqual(
  Array.from(appModule.exports.PLANNED_CONNECTORS.filter((item) => item.platform === 'qq'), (item) => item.id),
  ['qq-milky'],
  'the disconnected catalog must expose only the Milky QQ connector',
);
const settingsTree = appModule.exports.Settings({
  session: { status: 'disconnected', system: null }, activeToken: '', tokenDraft: '',
  hasToken: false, storageAvailable: true, rememberDevice: false, remembered: false,
  deviceExpiresAt: null, persistenceNotice: null, forgetting: false,
});
const descendants = [];
function visit(node) {
  if (Array.isArray(node)) return node.forEach(visit);
  if (!node || typeof node !== 'object') return;
  descendants.push(node);
  visit(node.props?.children);
}
visit(settingsTree);
assert.equal(descendants.filter((node) => node.type === MilkyPanel).length, 1, 'Settings must mount the Milky panel once');
assert.equal(descendants.filter((node) => node.props?.['aria-labelledby'] === 'qq-settings-title').length, 0, 'the retired QQ identity panel must not render');

const source = stripTypeScriptTypes(
  readFileSync(new URL('../apps/desktop/src/milky-api.ts', import.meta.url), 'utf8'),
)
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm, '')
  .concat('\nmodule.exports={getMilkyStatus,configureMilkyMonitor,getMilkyConfig,saveMilkyConfig,testMilkyConfig};');
const module = { exports: {} };
const requests = [];
let response = {
  configured: true,
  state: 'configured',
  account_id: '123456789',
  nickname: 'synthetic',
  milky_version: '1.3',
  checked_at: '',
  receiving_enabled: false,
  sending_enabled: false,
  read_only: true,
  last_sync_at: '',
  last_sync_result: '',
  skipped: 0,
  gaps: null,
  monitor: {
    enabled: true,
    state: 'pending_verification',
    peer_ids: null,
    read_only: true,
    last_error: '',
  },
};
let responseStatus = 200;
runInNewContext(source, {
  module,
  AbortController,
  DOMException,
  transport: {
    SERVICE: { url: 'http://127.0.0.1:17890', error: null },
    ServiceError: class ServiceError extends Error {
      constructor(message, code, status) {
        super(message);
        this.code = code;
        this.status = status;
      }
    },
  },
  window: { setTimeout: () => 1, clearTimeout() {} },
  fetch: async (url, options) => {
    requests.push({ url, ...options });
    return { ok: responseStatus === 200, status: responseStatus, json: async () => structuredClone(response) };
  },
});
const api = module.exports;
const signal = () => new AbortController().signal;
const status = await api.getMilkyStatus('synthetic-token', signal());
assert.equal(status.monitor.state, 'pending_verification');
assert.equal(status.monitor.peer_ids.length, 0);
assert.equal(status.gaps.length, 0);
await api.configureMilkyMonitor('synthetic-token', signal(), false, ['987654321']);
assert.equal(requests.at(-1).method, 'POST');
assert.deepEqual(JSON.parse(requests.at(-1).body), {
  enabled: false,
  peer_ids: ['987654321'],
});
assert.equal(requests.at(-1).credentials, 'omit');

const emptyConfig = { url: '', account_id: '', history_limit: 20, token_configured: false, revision: 0, configured: false, sending_enabled: false, source: 'none' };
const configInput = { url: 'http://127.0.0.1:3010', account_id: '123456789', history_limit: 20, access_token: 'synthetic-protocol-key', expected_revision: 0 };
const savedConfig = { ...emptyConfig, url: configInput.url, account_id: configInput.account_id, token_configured: true, configured: true, revision: 1, source: 'saved' };
const verification = { connection_id: 'qq-milky', state: 'identity_verified', account_id: configInput.account_id, nickname: '合成账号', impl_name: 'synthetic', impl_version: '1', milky_version: '1.3', checked_at: '2026-11-17T10:00:00Z', receiving_enabled: false, sending_enabled: false };
response = structuredClone(emptyConfig);
assert.equal((await api.getMilkyConfig('synthetic-token', signal())).source, 'none');
assert.equal(requests.at(-1).method, 'GET');
response = structuredClone(verification);
await api.testMilkyConfig('synthetic-token', signal(), { ...configInput, arbitrary_url: 'never-send' });
assert.equal(requests.at(-1).url, 'http://127.0.0.1:17890/api/v1/connections/qq-milky/config/test');
assert.equal(requests.at(-1).method, 'POST');
assert.deepEqual(JSON.parse(requests.at(-1).body), configInput, 'test accepts only the documented settings');
response = structuredClone(savedConfig);
await api.saveMilkyConfig('synthetic-token', signal(), { ...configInput, confirmed: true, password: 'must-not-be-sent' });
assert.equal(requests.at(-1).method, 'PATCH');
assert.deepEqual(JSON.parse(requests.at(-1).body), { ...configInput, confirmed: true });
assert.equal(requests.at(-1).credentials, 'omit');
assert.equal(new URL(requests.at(-1).url).search, '');
const beforeInvalid = requests.length;
for (const patch of [{ url: 'https://remote.example:3010' }, { url: 'http://127.0.0.1:3010/api' }, { url: 'http://user:key@127.0.0.1:3010' }, { account_id: 'not-an-id' }, { access_token: 'key\nline' }, { history_limit: 31 }, { expected_revision: -1 }]) {
  await assert.rejects(api.testMilkyConfig('synthetic-token', signal(), { ...configInput, ...patch }), error => error.code === 'MILKY_CONFIG_INVALID');
}
await assert.rejects(api.saveMilkyConfig('synthetic-token', signal(), { ...configInput, confirmed: false }), error => error.code === 'MILKY_CONFIG_CONFIRMATION_REQUIRED');
assert.equal(requests.length, beforeInvalid, 'invalid settings must never issue HTTP');
for (const patch of [{ access_token: 'must-never-return' }, { sending_enabled: true }, { source: 'unknown' }, { revision: -1 }, { history_limit: 31 }]) {
  response = { ...savedConfig, ...patch };
  await assert.rejects(api.getMilkyConfig('synthetic-token', signal()), error => error.code === 'INVALID_RESPONSE');
}
response = { ...verification, account_id: '987654321' };
await assert.rejects(api.testMilkyConfig('synthetic-token', signal(), configInput), error => error.code === 'INVALID_RESPONSE');
responseStatus = 409;
response = { error: { code: 'MILKY_CONFIG_STALE', message: 'private provider raw data' } };
await assert.rejects(api.saveMilkyConfig('synthetic-token', signal(), { ...configInput, confirmed: true }), error => error.code === 'MILKY_CONFIG_STALE' && !error.message.includes('private'));

// Exercise the shipped setup component with a synthetic React lifecycle.
// Setters fail after unmount, so late network completions cannot pass silently.
class SetupServiceError extends Error { constructor(message, code) { super(message); this.code = code; } }
function setupHarness(bindings, initialToken = 'synthetic-ui-token') {
  const slots = [];
  let cursor = 0, dirty = true, tree, effects = [], unmounted = false, activeToken = initialToken;
  const busyChanges = [], saves = [], authFailures = [], historyChanges = [];
  const hooks = {
    useState(initial) { const index = cursor++; if (!(index in slots)) slots[index] = initial; return [slots[index], value => { assert.equal(unmounted, false, 'state write after unmount'); const next = typeof value === 'function' ? value(slots[index]) : value; if (!Object.is(next, slots[index])) { slots[index] = next; dirty = true; } }]; },
    useRef(initial) { const index = cursor++; if (!(index in slots)) slots[index] = { current: initial }; return slots[index]; },
    useEffect(callback, deps) { const index = cursor++, old = slots[index]; if (!old || deps.some((value, i) => !Object.is(value, old.deps[i]))) effects.push(() => { old?.cleanup?.(); slots[index] = { deps, cleanup: callback() }; }); },
  };
  const raw = readFileSync(new URL('../apps/desktop/src/MilkySetupPanel.tsx', import.meta.url), 'utf8')
    .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
    .replace(/^import ['"][^'"]+['"];?\r?$/gm, '')
    .replace('export function MilkySetupPanel', 'function MilkySetupPanel');
  const compiled = transformSync('MilkySetupPanel.tsx', raw, { jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' } });
  assert.equal(compiled.errors.length, 0);
  const component = { exports: {} };
  runInNewContext(compiled.code + '\nmodule.exports=MilkySetupPanel;', {
    module: component, ...hooks, ...bindings, ServiceError: SetupServiceError, AbortController,
    jsx: (type, props, ...children) => ({ type, props: { ...props, children } }), Fragment: 'fragment',
  });
  return {
    get tree() { return tree; }, busyChanges, saves, authFailures, historyChanges,
    setToken(value) { activeToken = value; dirty = true; },
    async flush() { for (let i = 0; i < 35; i++) { if (dirty && !unmounted) { cursor = 0; dirty = false; tree = component.exports({ token: activeToken, disabled: false, monitorEnabled: true, onBusyChange(value) { busyChanges.push(value); }, onSaved() { saves.push(true); }, onAuthFailure() { authFailures.push(true); }, onHistoryLimitChange(limit) { historyChanges.push(limit); } }); const queue = effects; effects = []; for (const effect of queue) effect(); } await Promise.resolve(); } },
    unmount() { for (const slot of slots) slot?.cleanup?.(); unmounted = true; },
  };
}
function setupNodes(node) { if (!node || typeof node !== 'object') return []; if (Array.isArray(node)) return node.flatMap(setupNodes); return [node, ...setupNodes(node.props?.children)]; }
function setupField(harness, id) { const result = setupNodes(harness.tree).find(node => node.props?.id === id); assert.ok(result, 'Missing setup control: ' + id); return result; }
const submit = { preventDefault() {} };
let setupRead = structuredClone(emptyConfig), failSetupSave = false;
const testedInputs = [], savedInputs = [];
const setup = setupHarness({
  getMilkyConfig: async () => structuredClone(setupRead),
  testMilkyConfig: async (_token, _signal, input) => { testedInputs.push(structuredClone(input)); return structuredClone(verification); },
  saveMilkyConfig: async (_token, _signal, input) => { savedInputs.push(structuredClone(input)); if (failSetupSave) throw new SetupServiceError('保存失败，输入保留', 'MILKY_CONFIG_UNAVAILABLE'); return structuredClone(savedConfig); },
});
await setup.flush();
assert.equal(setupField(setup, 'milky-url').props.value, 'http://127.0.0.1:3010');
assert.equal(setupField(setup, 'milky-access-token').props.type, 'password');
assert.equal(setupField(setup, 'milky-save-config').props.disabled, true);
assert.ok(setupNodes(setup.tree).some(node => node.type === 'a' && node.props.href === 'https://luckylillia.com/guide/choice_install'));
setupField(setup, 'milky-account-id').props.onChange({ target: { value: configInput.account_id } });
setupField(setup, 'milky-access-token').props.onChange({ target: { value: 'synthetic-ui-key' } });
await setup.flush();
await setupField(setup, 'milky-test-connection').props.onClick(); await setup.flush();
assert.equal(testedInputs.length, 1);
assert.equal(savedInputs.length, 0, 'testing must not persist configuration');
assert.equal(setupField(setup, 'milky-save-config').props.disabled, true, 'a successful test still needs explicit confirmation');
setupField(setup, 'milky-confirm-save').props.onChange({ target: { checked: true } }); await setup.flush();
assert.equal(setupField(setup, 'milky-save-config').props.disabled, false);
setupField(setup, 'milky-url').props.onChange({ target: { value: 'http://127.0.0.1:3011' } }); await setup.flush();
assert.equal(setupField(setup, 'milky-save-config').props.disabled, true, 'editing invalidates a prior successful test');
setupField(setup, 'milky-url').props.onChange({ target: { value: configInput.url } }); await setup.flush();
await setupField(setup, 'milky-test-connection').props.onClick(); await setup.flush();
setupField(setup, 'milky-confirm-save').props.onChange({ target: { checked: true } }); await setup.flush();
setupNodes(setup.tree).find(node => node.type === 'form').props.onSubmit(submit); await setup.flush();
assert.equal(savedInputs.at(-1).access_token, 'synthetic-ui-key');
assert.equal(savedInputs.at(-1).confirmed, true);
assert.equal(setupField(setup, 'milky-access-token').props.value, '', 'acknowledged save must clear secret input');
assert.equal(setup.saves.length, 1);
assert.equal(setup.historyChanges.at(-1), 20);
failSetupSave = true;
setupField(setup, 'milky-access-token').props.onChange({ target: { value: 'synthetic-retry-key' } }); await setup.flush();
await setupField(setup, 'milky-test-connection').props.onClick(); await setup.flush();
setupField(setup, 'milky-confirm-save').props.onChange({ target: { checked: true } }); await setup.flush();
setupNodes(setup.tree).find(node => node.type === 'form').props.onSubmit(submit); await setup.flush();
assert.equal(setupField(setup, 'milky-access-token').props.value, 'synthetic-retry-key', 'failed save preserves ephemeral retry input');
assert.equal(setupField(setup, 'milky-confirm-save').props.checked, false);
setupField(setup, 'milky-url').props.onChange({ target: { value: 'http://127.0.0.1:3020' } }); await setup.flush();
setupRead = { ...savedConfig, revision: 2, url: 'http://127.0.0.1:3030' };
setupField(setup, 'milky-reload-config').props.onClick(); await setup.flush();
assert.equal(setupField(setup, 'milky-url').props.value, 'http://127.0.0.1:3020', 'reload must retain dirty edits');
assert.equal(setupField(setup, 'milky-access-token').props.value, 'synthetic-retry-key');
assert.ok(setupNodes(setup.tree).some(node => node.type === 'p' && node.props.children.flat(Infinity).some(value => typeof value === 'string' && value.includes('当前输入已保留'))));
setup.unmount();

let finishTest;
const late = setupHarness({ getMilkyConfig: async () => structuredClone(emptyConfig), testMilkyConfig: async () => new Promise(resolve => { finishTest = resolve; }), saveMilkyConfig: async () => { throw new Error('must not save'); } });
await late.flush();
setupField(late, 'milky-account-id').props.onChange({ target: { value: configInput.account_id } });
setupField(late, 'milky-access-token').props.onChange({ target: { value: 'synthetic-late-key' } }); await late.flush();
setupField(late, 'milky-test-connection').props.onClick(); await late.flush();
late.unmount(); finishTest(structuredClone(verification)); await late.flush();
assert.equal(late.saves.length, 0);
console.log('Milky client checks passed: single QQ settings entry, guided installation, strict config GET/test/PATCH, no secret persistence, explicit test/save separation, dirty-edit retention, save retry, and unmount protection. Synthetic data only.');
