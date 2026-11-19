import assert from 'node:assert/strict';
import { deviceAuthSource } from './frontend-auth-test-loader.mjs';
import { loadIcon } from './frontend-icon-test-loader.mjs';
import { loadHelpDetails } from './frontend-help-test-loader.mjs';
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
const jsx = (type, supplied, ...children) => {
  const props = { ...supplied, children };
  return typeof type === 'function' && (type === Icon || type === HelpDetails) ? type(props) : { type, props };
};
const Icon = loadIcon(jsx);
const HelpDetails = loadHelpDetails(jsx);
runInNewContext(transformedApp.code + '\nmodule.exports={Settings,PLANNED_CONNECTORS};', {
  module: appModule, jsx, Icon, HelpDetails, Fragment: 'fragment',
  MilkyPanel, TelegramBotPanel: () => null, AIModelPanel: () => null, DigestPanel: () => null,
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
assert.ok(descendants.some((node) => node.type === 'svg' && node.props['data-icon'] === 'plug' && node.props['aria-hidden'] === 'true' && node.props.focusable === 'false'), 'Settings must render the shipped decorative SVG');
assert.equal(descendants.filter((node) => node.type === MilkyPanel).length, 1, 'Settings must mount the Milky panel once');
assert.equal(descendants.filter((node) => node.props?.['aria-labelledby'] === 'qq-settings-title').length, 0, 'the retired QQ identity panel must not render');

const source = stripTypeScriptTypes(
  readFileSync(new URL('../apps/desktop/src/milky-api.ts', import.meta.url), 'utf8'),
)
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^import .* from '\.\/device-auth';\r?\n/m, () => deviceAuthSource).replace(/^export /gm, '')
  .concat('\nmodule.exports={getMilkyStatus,configureMilkyMonitor};');
const module = { exports: {} };
const requests = [];
const response = {
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
    return { ok: true, status: 200, json: async () => structuredClone(response) };
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

// Render already-read server snapshots without running connection effects.
// Exercise the actual button handlers; network and all account data are inert.
const renderablePanel = transformSync('MilkyPanel.tsx', panel
  .replace(/^import[\s\S]*?from ['"][^'"]+['"];?\r?$/gm, '')
  .replace(/^export /gm, ''), {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
function monitorFixture({ enabled, state, accountState = 'identity_verified', busy = null }) {
  const initial = {
    ...structuredClone(response), state: accountState,
    monitor: { ...response.monitor, enabled, state, peer_ids: ['987654321'] },
  };
  const values = [initial, null, null, busy, '', '987654321'];
  const refs = [];
  let stateIndex = 0, refIndex = 0, release;
  const operations = [];
  const component = { exports: {} };
  runInNewContext(renderablePanel.code + '\nmodule.exports=MilkyPanel;', {
    module: component, jsx, HelpDetails, AbortController,
    useState(initialValue) {
      const index = stateIndex++;
      if (!(index in values)) values[index] = initialValue;
      return [values[index], (value) => {
        values[index] = typeof value === 'function' ? value(values[index]) : value;
      }];
    },
    useRef(initialValue) {
      const index = refIndex++;
      return refs[index] ??= { current: initialValue };
    },
    useCallback: (callback) => callback,
    useEffect() {},
    ServiceError: class ServiceError extends Error {},
    configureMilkyMonitor(_token, _signal, nextEnabled, peers) {
      operations.push({ enabled: nextEnabled, peer_ids: Array.from(peers) });
      return new Promise((resolve) => {
        release = () => resolve({ ...initial, monitor: { ...initial.monitor,
          enabled: nextEnabled, state: nextEnabled ? 'running' : 'disabled',
        } });
      });
    },
  });
  const render = () => {
    stateIndex = refIndex = 0;
    return component.exports({ token: 'synthetic-token', onSystemUpdate() {}, onAuthFailure() {} });
  };
  const monitorButtons = () => {
    const all = [];
    const walk = (node) => {
      if (Array.isArray(node)) return node.forEach(walk);
      if (!node || typeof node !== 'object') return;
      if (node.type === 'button' && /后台接收/.test(label(node))) all.push(node);
      walk(node.props?.children);
    };
    walk(render());
    return all;
  };
  return { monitorButtons, operations, release: () => release() };
}
function label(node) {
  if (Array.isArray(node)) return node.map(label).join('');
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  return node && typeof node === 'object' ? label(node.props?.children) : '';
}
for (const state of ['running', 'pending_verification']) {
  for (const busy of [null, 'verify']) {
    const fixture = monitorFixture({ enabled: true, state, busy,
      accountState: state === 'running' ? 'identity_verified' : 'configured' });
    const controls = fixture.monitorButtons();
    assert.equal(controls.length, 1, 'saved authorization keeps one visible stop/clear action');
    assert.equal(label(controls[0]), state === 'running' ? '停止后台接收' : '清除已保存的后台接收');
    assert.equal(controls[0].props.disabled, busy !== null, 'in-flight requests retain the visible authorized stop with the existing busy guard');
  }
}
const startFixture = monitorFixture({ enabled: false, state: 'disabled' });
assert.equal(startFixture.monitorButtons().length, 1);
assert.equal(label(startFixture.monitorButtons()[0]), '开启后台接收');
startFixture.monitorButtons()[0].props.onClick();
assert.deepEqual(startFixture.operations, [{ enabled: true, peer_ids: ['987654321'] }]);
startFixture.release();
for (let i = 0; i < 10; i++) await Promise.resolve();
assert.equal(label(startFixture.monitorButtons()[0]), '停止后台接收');
const clearFixture = monitorFixture({ enabled: true, state: 'pending_verification', accountState: 'configured' });
clearFixture.monitorButtons()[0].props.onClick();
assert.equal(label(clearFixture.monitorButtons()[0]), '清除已保存的后台接收');
assert.equal(clearFixture.monitorButtons()[0].props.disabled, true);
assert.deepEqual(clearFixture.operations, [{ enabled: false, peer_ids: ['987654321'] }]);
clearFixture.release();
for (let i = 0; i < 10; i++) await Promise.resolve();
assert.equal(label(clearFixture.monitorButtons()[0]), '开启后台接收');
assert.equal(clearFixture.monitorButtons()[0].props.disabled, true, 'clearing pending authorization does not bypass account verification');
console.log('Milky client checks passed: single QQ catalog/settings integration, one state-aware monitor action, visible guarded stop during in-flight requests, pending clear without re-verification, persisted peers, safe payloads, and component compilation. Synthetic data only.');
