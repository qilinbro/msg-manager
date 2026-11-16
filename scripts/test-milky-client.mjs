import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { stripTypeScriptTypes } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from '../apps/desktop/node_modules/rolldown/dist/experimental-index.mjs';

// Synthetic client smoke checks only: no browser, service, account or token.
const panel = readFileSync(new URL('../apps/desktop/src/MilkyPanel.tsx', import.meta.url), 'utf8');
assert.match(panel, /next\.monitor\.peer_ids\.join/);
assert.match(panel, /pending_verification/);
assert.match(panel, /已保存，待重新核验/);
assert.match(panel, /清除已保存的后台接收/);
const transformedPanel = transformSync('MilkyPanel.tsx', panel, {
  jsx: { runtime: 'classic', pragma: 'jsx', pragmaFrag: 'Fragment' },
});
assert.equal(transformedPanel.errors.length, 0, 'MilkyPanel must compile in the synthetic client check');

const source = stripTypeScriptTypes(
  readFileSync(new URL('../apps/desktop/src/milky-api.ts', import.meta.url), 'utf8'),
)
  .replace(/^import .* from '\.\/api';\r?\n/m, 'const {SERVICE,ServiceError}=transport;\n')
  .replace(/^export /gm, '')
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
  gaps: [],
  monitor: {
    enabled: true,
    state: 'pending_verification',
    peer_ids: ['987654321'],
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
assert.deepEqual(status.monitor.peer_ids, ['987654321']);
await api.configureMilkyMonitor('synthetic-token', signal(), false, ['987654321']);
assert.equal(requests.at(-1).method, 'POST');
assert.deepEqual(JSON.parse(requests.at(-1).body), {
  enabled: false,
  peer_ids: ['987654321'],
});
assert.equal(requests.at(-1).credentials, 'omit');
console.log('Milky client checks passed: pending verification, persisted peer display, safe stop payload, and component compilation. Synthetic data only.');
